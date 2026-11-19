// Command tack-bridge relays a Sunshine stream to browsers over WebRTC.
//
//	tack-bridge pair   -host 192.168.1.108   pair with Sunshine (once)
//	tack-bridge serve                       serve the web client
//	tack-bridge apps                        list host apps
//	tack-bridge stream -seconds 10 -out x.h264   spike: record a raw stream
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"tack/bridge/internal/moonlight"
	"tack/bridge/internal/nvhttp"
)

// host is the saved, paired Sunshine host.
type host struct {
	Address    string `json:"address"`
	HTTPSPort  int    `json:"https_port"`
	ServerCert []byte `json:"server_cert"` // DER, pinned at pairing
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: tack-bridge pair|serve|apps|stream [flags]")
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dataDir := fs.String("data", "data", "state directory (identity, paired host)")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch cmd {
	case "pair":
		addr := fs.String("host", "", "Sunshine host address")
		fs.Parse(args)
		err = pair(ctx, *dataDir, *addr)
	case "serve":
		listen := fs.String("listen", ":8443", "HTTPS listen address")
		udpPort := fs.Int("udp-port", 8189, "UDP port for all WebRTC media")
		app := fs.String("app", "Desktop", "host app to launch when none is running")
		fs.Parse(args)
		err = serve(ctx, *dataDir, *listen, *udpPort, *app)
	case "apps":
		fs.Parse(args)
		err = apps(ctx, *dataDir)
	case "stream":
		o := streamOpts{}
		fs.StringVar(&o.app, "app", "Desktop", "app title to launch")
		fs.IntVar(&o.width, "w", 1920, "width")
		fs.IntVar(&o.height, "h", 1080, "height")
		fs.IntVar(&o.fps, "fps", 60, "frames per second")
		fs.IntVar(&o.bitrate, "bitrate", 20000, "bitrate in kbps")
		fs.IntVar(&o.seconds, "seconds", 10, "how long to stream")
		fs.StringVar(&o.out, "out", "", "write the raw video elementary stream here")
		fs.BoolVar(&o.cursorSpike, "cursor-spike", false, "hide the host cursor mid-stream and show it again, logging frame numbers")
		fs.Parse(args)
		err = stream(ctx, *dataDir, o)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func loadHost(dataDir string) (*nvhttp.Client, error) {
	id, err := nvhttp.LoadOrCreateIdentity(dataDir)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dataDir, "host.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("no paired host; run: tack-bridge pair -host <address>")
	}
	if err != nil {
		return nil, err
	}
	var h host
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, err
	}
	c := nvhttp.NewClient(h.Address, id)
	c.HTTPSPort, c.ServerCert = h.HTTPSPort, h.ServerCert
	return c, nil
}

func pair(ctx context.Context, dataDir, addr string) error {
	if addr == "" {
		return errors.New("-host is required")
	}
	id, err := nvhttp.LoadOrCreateIdentity(dataDir)
	if err != nil {
		return err
	}
	c := nvhttp.NewClient(addr, id)
	si, err := c.ServerInfo(ctx)
	if err != nil {
		return err
	}
	pin := nvhttp.NewPIN()
	fmt.Printf("Pairing with %s (Sunshine %s).\nEnter PIN %s in Sunshine's web UI: https://localhost:47990/pin on the host\n", si.Hostname, si.AppVersion, pin)
	pairCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cert, err := c.Pair(pairCtx, si.AppVersion, pin)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(host{Address: addr, HTTPSPort: c.HTTPSPort, ServerCert: cert}, "", "  ")
	if err := os.WriteFile(filepath.Join(dataDir, "host.json"), b, 0o600); err != nil {
		return err
	}
	fmt.Println("Paired.")
	return nil
}

func apps(ctx context.Context, dataDir string) error {
	c, err := loadHost(dataDir)
	if err != nil {
		return err
	}
	list, err := c.AppList(ctx)
	if err != nil {
		return err
	}
	for _, a := range list {
		fmt.Printf("%12d  %s\n", a.ID, a.Title)
	}
	return nil
}

type streamOpts struct {
	app                string
	width, height, fps int
	bitrate, seconds   int
	out                string
	cursorSpike        bool
}

func stream(ctx context.Context, dataDir string, o streamOpts) error {
	c, err := loadHost(dataDir)
	if err != nil {
		return err
	}
	si, err := c.ServerInfo(ctx)
	if err != nil {
		return err
	}
	if !si.Paired {
		return errors.New("host says we are not paired; pair again")
	}
	list, err := c.AppList(ctx)
	if err != nil {
		return err
	}
	appID := 0
	for _, a := range list {
		if strings.EqualFold(a.Title, o.app) {
			appID = a.ID
		}
	}
	if appID == 0 {
		return fmt.Errorf("no app titled %q on host", o.app)
	}

	var lp nvhttp.LaunchParams
	lp.RIKey, lp.RIKeyID = nvhttp.NewInputKey()
	lp.AppID, lp.Width, lp.Height, lp.FPS = appID, o.width, o.height, o.fps
	lp.SurroundAudioInfo = moonlight.SurroundAudioInfoStereo
	lp.ExtraQuery = moonlight.LaunchQuery()

	resume := si.CurrentGame == appID
	if si.CurrentGame != 0 && !resume {
		return fmt.Errorf("another app (%d) is running on the host", si.CurrentGame)
	}
	rtspURL, err := c.Launch(ctx, lp, resume)
	if err != nil {
		return err
	}
	log.Printf("launched app %d (resume=%v), rtsp %s", appID, resume, rtspURL)

	var out *os.File
	if o.out != "" {
		os.MkdirAll(filepath.Dir(o.out), 0o755)
		if out, err = os.Create(o.out); err != nil {
			return err
		}
		defer out.Close()
	}

	// Frames are handed off to a writer goroutine so the receive thread never
	// blocks on disk; a full queue drops the frame and requests an IDR.
	frames := make(chan moonlight.Frame, 64)
	var nFrames, nBytes, nAudio atomic.Int64
	var firstFrame atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for f := range frames {
			if out != nil {
				out.Write(f.Data)
			}
		}
	}()

	terminated := make(chan int, 1)
	start := time.Now()
	err = moonlight.Start(moonlight.ServerInfo{
		Address:          c.Host,
		AppVersion:       si.AppVersion,
		GfeVersion:       si.GfeVersion,
		RTSPSessionURL:   rtspURL,
		CodecModeSupport: si.CodecModeSupport,
	}, moonlight.StreamConfig{
		Width: o.width, Height: o.height, FPS: o.fps,
		BitrateKbps:  o.bitrate,
		VideoFormats: moonlight.FormatH264,
		RIKey:        lp.RIKey,
		RIKeyID:      lp.RIKeyID,
	}, moonlight.Handlers{
		VideoSetup: func(format, w, h, fps int) { log.Printf("video: format 0x%x %dx%d@%d", format, w, h, fps) },
		AudioSetup: func(rate, ch, spf int) { log.Printf("audio: opus %d Hz, %d ch, %d samples/frame", rate, ch, spf) },
		Video: func(f moonlight.Frame) bool {
			firstFrame.CompareAndSwap(0, int64(time.Since(start)))
			select {
			case frames <- f:
				nFrames.Add(1)
				nBytes.Add(int64(len(f.Data)))
				return true
			default:
				return false
			}
		},
		Audio:      func(p []byte) { nAudio.Add(1) },
		Terminated: func(code int) { terminated <- code },
		Log:        func(m string) { log.Print("mcc: ", strings.TrimRight(m, "\n")) },
	})
	if err != nil {
		return err
	}
	log.Printf("connected in %v", time.Since(start).Round(time.Millisecond))

	if o.cursorSpike {
		go cursorSpike(ctx, o, &nFrames)
	}

	select {
	case <-time.After(time.Duration(o.seconds) * time.Second):
	case <-ctx.Done():
	case code := <-terminated:
		log.Printf("host ended session (%d)", code)
	}
	elapsed := time.Since(start)
	rtt, rttVar, _ := moonlight.RTT()
	moonlight.Stop()
	close(frames)
	<-done

	log.Printf("frames %d (%.1f fps), video %.1f Mbps, audio packets %d, first frame after %v, rtt %v±%v",
		nFrames.Load(), float64(nFrames.Load())/elapsed.Seconds(),
		float64(nBytes.Load())*8/elapsed.Seconds()/1e6, nAudio.Load(),
		time.Duration(firstFrame.Load()).Round(time.Millisecond), rtt, rttVar)

	if err := c.Cancel(context.Background()); err != nil {
		log.Printf("cancel: %v", err)
	}
	return nil
}

// cursorSpike moves the host cursor to the centre (so it is visible), hides
// it, nudges it, then shows it again, logging the frame count at each step so
// frames can be pulled out of the capture and compared.
func cursorSpike(ctx context.Context, o streamOpts, n *atomic.Int64) {
	w, h := int16(o.width), int16(o.height)
	step := func(at time.Duration, what string, f func()) {
		select {
		case <-time.After(at):
		case <-ctx.Done():
			return
		}
		f()
		log.Printf("cursor-spike: %s (after frame %d)", what, n.Load())
	}
	start := time.Now()
	at := func(d time.Duration) time.Duration { return max(0, d-time.Since(start)) }
	step(at(1*time.Second), "cursor to centre", func() { moonlight.MousePosition(w/2, h/2, w, h) })
	step(at(3*time.Second), "toggle cursor (hide)", func() { moonlight.ToggleHostCursor() })
	step(at(4*time.Second), "nudge cursor", func() { moonlight.MousePosition(w/2+40, h/2+40, w, h) })
	step(at(6*time.Second), "toggle cursor (show)", func() { moonlight.ToggleHostCursor() })
	step(at(7*time.Second), "nudge cursor", func() { moonlight.MousePosition(w/2, h/2, w, h) })
}
