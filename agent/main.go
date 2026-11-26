// Command tack-agent runs in the Hyprland session on the gaming PC. It dials
// out to the bridge and provides what Moonlight cannot: clipboard sync, and a
// mouse-mode signal from the focused window.
//
//	tack-agent -bridge https://192.168.1.50:8443
//
// Needs ~/.config/tack/agent-token and ~/.config/tack/ca.pem, both copied from
// the bridge's data directory.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/websocket"

	"tack/internal/agentproto"
)

func main() {
	log.SetFlags(log.Ltime)
	cfgDir, _ := os.UserConfigDir()
	bridge := flag.String("bridge", "", "bridge URL, e.g. https://192.168.1.50:8443")
	tokenFile := flag.String("token-file", filepath.Join(cfgDir, "tack", "agent-token"), "agent token (bridge data/agent-token)")
	caFile := flag.String("ca", filepath.Join(cfgDir, "tack", "ca.pem"), "bridge CA certificate (bridge data/tls/ca.pem)")
	cursorTheme := flag.String("cursor-theme", envOr("XCURSOR_THEME", "default"), "XCursor theme whose cursors the browser shows")
	cursorSize := flag.Int("cursor-size", envInt("XCURSOR_SIZE", 24), "cursor size in host pixels")
	relative := flag.String("relative-class", `^(steam_app_\d+|gamescope)$`, "regexp on the focused window's class that switches the browser to relative mouse (pointer lock)")
	flag.Parse()

	rule, err := regexp.Compile(*relative)
	if err != nil {
		log.Fatalf("-relative-class: %v", err)
	}
	if *bridge == "" {
		log.Fatal("-bridge is required")
	}
	token, err := os.ReadFile(*tokenFile)
	if err != nil {
		log.Fatal(err)
	}
	caPEM, err := os.ReadFile(*caFile)
	if err != nil {
		log.Fatal(err)
	}
	// The bridge's own CA, plus the system roots for a bridge behind a reverse
	// proxy with a public certificate.
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(caPEM) {
		log.Fatalf("%s: no certificate", *caFile)
	}

	var cursors *agentproto.Message
	theme, err := loadCursorTheme(*cursorTheme)
	if err == nil {
		total := 0
		for _, c := range theme.images {
			total += len(c.PNG)
		}
		if total > agentproto.MaxCursorPNG {
			err = fmt.Errorf("theme images too large (%d bytes)", total)
		}
	}
	if err != nil {
		log.Printf("cursor: %v (the browser keeps its own)", err)
	} else {
		cursors = &agentproto.Message{Type: agentproto.TypeCursors, Cursors: theme.images, Size: *cursorSize}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := &agent{
		bridge:  strings.TrimRight(*bridge, "/"),
		token:   strings.TrimSpace(string(token)),
		roots:   roots,
		modes:   make(chan string, 16),
		clips:   make(chan string, 4),
		suns:    make(chan uint64, 1),
		shapes:  make(chan cursorState, 4),
		cursors: cursors,
	}
	go watchFocus(ctx, rule, a.modes, log.Printf)
	go watchClipboard(ctx, a.clips, log.Printf)
	go watchSunshine(ctx, a.suns)
	if theme != nil {
		frames := make(chan cursorFrame, 16)
		go watchCursor(ctx, frames, log.Printf)
		go matchCursor(ctx, theme.shapes, frames, a.shapes)
	}
	a.run(ctx)
}

type agent struct {
	bridge  string
	token   string
	roots   *x509.CertPool
	modes   chan string
	clips   chan string
	suns    chan uint64
	shapes  chan cursorState
	cursors *agentproto.Message // nil if the theme could not be read

	mode     string      // latest mouse mode
	known    string      // clipboard text both sides already have
	sunStart uint64      // Sunshine's start time, 0 if not seen yet
	shape    cursorState // host cursor state, zero until known
}

func (a *agent) run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		ws, err := a.dial()
		if err != nil {
			log.Printf("bridge: %v (retry in %v)", err, backoff)
			a.idle(ctx, backoff)
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		log.Printf("bridge: connected")
		backoff = time.Second
		err = a.serve(ctx, ws)
		ws.Close()
		if ctx.Err() == nil {
			log.Printf("bridge: disconnected: %v", err)
			a.idle(ctx, backoff)
		}
	}
}

func (a *agent) dial() (*websocket.Conn, error) {
	u := "wss" + strings.TrimPrefix(a.bridge, "https") + "/agent"
	cfg, err := websocket.NewConfig(u, a.bridge)
	if err != nil {
		return nil, err
	}
	cfg.TlsConfig = &tls.Config{RootCAs: a.roots}
	cfg.Header = http.Header{"Authorization": {"Bearer " + a.token}}
	cfg.Dialer = &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
	return websocket.DialConfig(cfg)
}

// idle waits out a backoff while keeping track of state changes.
func (a *agent) idle(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case a.mode = <-a.modes:
		case a.known = <-a.clips:
		case a.sunStart = <-a.suns:
		case a.shape = <-a.shapes:
		case <-t.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (a *agent) serve(ctx context.Context, ws *websocket.Conn) error {
	send := func(m agentproto.Message) error {
		ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return websocket.JSON.Send(ws, m)
	}
	sendSunshine := func() error {
		up := sunshineUptime(a.sunStart)
		if a.sunStart == 0 || up <= 0 {
			return nil
		}
		return send(agentproto.Message{Type: agentproto.TypeSunshine, UptimeMs: up.Milliseconds()})
	}
	if a.mode != "" {
		if err := send(agentproto.Message{Type: agentproto.TypeMode, Mode: a.mode}); err != nil {
			return err
		}
	}
	if err := sendSunshine(); err != nil {
		return err
	}
	if a.cursors != nil {
		if err := send(*a.cursors); err != nil {
			return err
		}
	}
	sendShape := func() error {
		if a.shape.name == "" {
			return nil
		}
		return send(agentproto.Message{Type: agentproto.TypeCursor, State: a.shape.name, Px: a.shape.px, ScreenW: screenWidth()})
	}
	if err := sendShape(); err != nil {
		return err
	}

	incoming := make(chan agentproto.Message)
	readErr := make(chan error, 1)
	go func() {
		for {
			var m agentproto.Message
			if err := websocket.JSON.Receive(ws, &m); err != nil {
				readErr <- err
				return
			}
			incoming <- m
		}
	}()

	for {
		select {
		case mode := <-a.modes:
			if mode == a.mode {
				continue
			}
			a.mode = mode
			log.Printf("mode: %s", mode)
			if err := send(agentproto.Message{Type: agentproto.TypeMode, Mode: mode}); err != nil {
				return err
			}
		case a.shape = <-a.shapes:
			if err := sendShape(); err != nil {
				return err
			}
		case a.sunStart = <-a.suns:
			log.Printf("sunshine: new process")
			if err := sendSunshine(); err != nil {
				return err
			}
		case text := <-a.clips:
			if text == a.known {
				continue // our own wl-copy, or unchanged
			}
			a.known = text
			if err := send(agentproto.Message{Type: agentproto.TypeClipboard, Text: text}); err != nil {
				return err
			}
		case m := <-incoming:
			if m.Type != agentproto.TypeClipboard || len(m.Text) > agentproto.MaxClipboard {
				continue
			}
			a.known = m.Text
			if err := setClipboard(ctx, m.Text); err != nil {
				log.Print(err)
			}
			// Ack even on failure so the browser's held paste key goes through.
			if err := send(agentproto.Message{Type: agentproto.TypeClipboardAck, ID: m.ID}); err != nil {
				return err
			}
		case err := <-readErr:
			return err
		case <-ctx.Done():
			return errors.New("shutting down")
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	return def
}
