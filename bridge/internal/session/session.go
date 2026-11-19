// Package session ties one browser's WebRTC connection to one Moonlight
// session on the host. Only one session exists at a time; a new one takes
// over from the current one.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"tack/bridge/internal/moonlight"
	"tack/bridge/internal/nvhttp"
	"tack/internal/agentproto"
)

// Request is what the browser POSTs to start streaming.
type Request struct {
	SDP         string `json:"sdp"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps"`
	BitrateKbps int    `json:"bitrate_kbps"`
}

type Manager struct {
	host *nvhttp.Client
	app  string // app title to launch when nothing is running
	api  *webrtc.API

	mu     sync.Mutex
	cur    *Session
	lastID int
	// live mirrors cur for readers that must not wait on mu (Start holds it
	// for the whole host launch).
	live atomic.Pointer[Session]

	agentMu          sync.Mutex
	agentID          int
	agentSend        func(agentproto.Message) error
	agentClose       func()
	agentMode        string         // last mode the agent reported
	agentCursors     map[string]any // host cursor images for browsers, nil if none
	agentCursorState map[string]any // last host cursor state message

	// cursor records Sunshine's draw-cursor flag (see cursor.go); mu guards it.
	cursor     cursorState
	cursorPath string
}

type Session struct {
	id int
	pc *webrtc.PeerConnection

	mu      sync.Mutex
	control *webrtc.DataChannel
	ended   bool

	clip       strings.Builder // browser clipboard chunks being reassembled
	clipTooBig bool

	wantCursor bool // pointer locked, so the host cursor belongs in the video (m.mu)

	// live gates input: only the current session may send to Moonlight, and
	// never while it is being stopped.
	inputMu sync.RWMutex
	live    bool
}

const (
	reasonKicked    = "kicked"
	reasonHostEnded = "host ended the stream"
	reasonShutdown  = "bridge shutting down"
)

// NewManager prepares WebRTC with all media on one UDP port, so a single
// firewall or port-forward rule covers it.
func NewManager(host *nvhttp.Client, app string, udpPort int, dataDir string) (*Manager, error) {
	mux, err := ice.NewMultiUDPMuxFromPort(udpPort)
	if err != nil {
		return nil, fmt.Errorf("udp port %d: %w", udpPort, err)
	}
	se := webrtc.SettingEngine{}
	se.SetICEUDPMux(mux)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})

	me := &webrtc.MediaEngine{}
	videoFB := []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "ccm", Parameter: "fir"}}
	codecs := []struct {
		typ webrtc.RTPCodecType
		p   webrtc.RTPCodecParameters
	}{
		// Sunshine encodes H.264 High. Offer High first; the constrained
		// baseline entry lets browsers that only advertise baseline (Firefox
		// with OpenH264) negotiate, relying on their decoder to cope.
		{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecParameters{PayloadType: 102, RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=64001f", RTCPFeedback: videoFB}}},
		{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecParameters{PayloadType: 104, RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f", RTCPFeedback: videoFB}}},
		{webrtc.RTPCodecTypeAudio, webrtc.RTPCodecParameters{PayloadType: 111, RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
			SDPFmtpLine: "minptime=10;useinbandfec=1;stereo=1;sprop-stereo=1"}}},
	}
	for _, c := range codecs {
		if err := me.RegisterCodec(c.p, c.typ); err != nil {
			return nil, err
		}
	}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se), webrtc.WithMediaEngine(me))
	m := &Manager{host: host, app: app, api: api, cursorPath: filepath.Join(dataDir, "host-cursor.json")}
	m.loadCursor()
	return m, nil
}

// Start takes over from any current session, launches Moonlight and returns
// the SDP answer once the host stream is up.
func (m *Manager) Start(ctx context.Context, req Request) (string, error) {
	if !strings.Contains(strings.ToUpper(req.SDP), "H264/90000") {
		return "", errors.New("this browser cannot decode H.264 over WebRTC")
	}
	if req.Width <= 0 || req.Height <= 0 || req.Width > 7680 || req.Height > 4320 {
		return "", fmt.Errorf("bad resolution %dx%d", req.Width, req.Height)
	}
	req.Width, req.Height = req.Width&^1, req.Height&^1 // encoders want even sizes
	if req.FPS <= 0 || req.FPS > 240 {
		req.FPS = 60
	}
	if req.BitrateKbps <= 0 {
		req.BitrateKbps = 25000
		if req.Height <= 800 {
			req.BitrateKbps = 12000
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur != nil {
		m.endLocked(m.cur, reasonKicked)
	}

	pc, err := m.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return "", err
	}
	m.lastID++
	s := &Session{id: m.lastID, pc: pc}
	fail := func(err error) (string, error) {
		pc.Close()
		return "", err
	}

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		switch dc.Label() {
		case "control":
			s.mu.Lock()
			s.control = dc
			s.mu.Unlock()
			dc.OnOpen(func() {
				for _, msg := range m.agentState() {
					s.sendControl(msg)
				}
			})
			dc.OnMessage(func(msg webrtc.DataChannelMessage) { m.handleControl(s, msg) })
		case "input":
			dc.OnMessage(func(msg webrtc.DataChannelMessage) { m.handleInput(s, msg) })
		}
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		log.Printf("session %d: peer %s", s.id, st)
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed {
			go m.end(s, "")
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: req.SDP}); err != nil {
		return fail(fmt.Errorf("offer: %w", err))
	}
	video, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000}, "video", "tack")
	if err != nil {
		return fail(err)
	}
	// Separate stream IDs: the browser would otherwise delay video to keep
	// lip sync with audio's larger jitter buffer.
	audio, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "tack-audio")
	if err != nil {
		return fail(err)
	}
	vs, err := pc.AddTrack(video)
	if err != nil {
		return fail(err)
	}
	as, err := pc.AddTrack(audio)
	if err != nil {
		return fail(err)
	}
	go readRTCP(vs, true)
	go readRTCP(as, false)

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return fail(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return fail(err)
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		return fail(ctx.Err())
	}

	if err := m.startMoonlight(ctx, req, s, video, audio); err != nil {
		return fail(err)
	}
	m.cur = s
	m.live.Store(s)
	m.syncCursor() // the browser draws its own cursor, so hide the host's
	return pc.LocalDescription().SDP, nil
}

func (m *Manager) startMoonlight(ctx context.Context, req Request, s *Session, video, audio *webrtc.TrackLocalStaticSample) error {
	si, err := m.host.ServerInfo(ctx)
	if err != nil {
		return fmt.Errorf("host: %w", err)
	}
	if !si.Paired {
		return errors.New("host does not recognise this bridge; pair again")
	}
	// Keep whatever is already running on the host; otherwise launch m.app.
	appID, resume := si.CurrentGame, si.CurrentGame != 0
	if !resume {
		apps, err := m.host.AppList(ctx)
		if err != nil {
			return fmt.Errorf("host: %w", err)
		}
		for _, a := range apps {
			if strings.EqualFold(a.Title, m.app) {
				appID = a.ID
			}
		}
		if appID == 0 {
			return fmt.Errorf("host has no app titled %q", m.app)
		}
	}

	lp := nvhttp.LaunchParams{
		AppID: appID, Width: req.Width, Height: req.Height, FPS: req.FPS,
		SurroundAudioInfo: moonlight.SurroundAudioInfoStereo,
		ExtraQuery:        moonlight.LaunchQuery(),
	}
	lp.RIKey, lp.RIKeyID = nvhttp.NewInputKey()
	rtspURL, err := m.host.Launch(ctx, lp, resume)
	if err != nil {
		return fmt.Errorf("host: %w", err)
	}

	frameDur := time.Second / time.Duration(req.FPS)
	var lastPTS time.Duration
	audioDur := 5 * time.Millisecond

	start := time.Now()
	err = moonlight.Start(moonlight.ServerInfo{
		Address:          m.host.Host,
		AppVersion:       si.AppVersion,
		GfeVersion:       si.GfeVersion,
		RTSPSessionURL:   rtspURL,
		CodecModeSupport: si.CodecModeSupport,
	}, moonlight.StreamConfig{
		Width: req.Width, Height: req.Height, FPS: req.FPS,
		BitrateKbps:  req.BitrateKbps,
		VideoFormats: moonlight.FormatH264,
		RIKey:        lp.RIKey,
		RIKeyID:      lp.RIKeyID,
	}, moonlight.Handlers{
		VideoSetup: func(format, w, h, fps int) {
			log.Printf("session %d: video 0x%x %dx%d@%d, %d kbps", s.id, format, w, h, fps, req.BitrateKbps)
		},
		AudioSetup: func(rate, ch, spf int) {
			audioDur = time.Duration(spf) * time.Second / time.Duration(rate)
		},
		// Runs on the receive thread: packetize and send immediately. The
		// RTP timestamp advances by the host's real frame spacing.
		Video: func(f moonlight.Frame) bool {
			d := frameDur
			if lastPTS != 0 && f.PTS > lastPTS {
				d = f.PTS - lastPTS
			}
			lastPTS = f.PTS
			_ = video.WriteSample(media.Sample{Data: f.Data, Duration: d})
			return true
		},
		Audio: func(p []byte) {
			_ = audio.WriteSample(media.Sample{Data: p, Duration: audioDur})
		},
		Rumble: func(pad, low, high uint16) {
			s.sendControl(map[string]any{"type": "rumble", "pad": pad, "low": low, "high": high})
		},
		Terminated: func(code int) {
			log.Printf("session %d: host ended stream (%d)", s.id, code)
			go m.end(s, reasonHostEnded)
		},
		Log: func(msg string) { log.Print("moonlight: ", strings.TrimRight(msg, "\n")) },
	})
	if err != nil {
		return err
	}
	s.inputMu.Lock()
	s.live = true
	s.inputMu.Unlock()
	log.Printf("session %d: moonlight up in %v (app %d, resume=%v)", s.id, time.Since(start).Round(time.Millisecond), appID, resume)
	return nil
}

// readRTCP drains a sender's RTCP (required for interceptors to work) and
// turns browser keyframe requests into host IDR requests.
func readRTCP(sender *webrtc.RTPSender, video bool) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		if !video {
			continue
		}
		for _, p := range pkts {
			switch p.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				moonlight.RequestIDR()
			}
		}
	}
}

func (m *Manager) handleInput(s *Session, msg webrtc.DataChannelMessage) {
	if msg.IsString {
		return
	}
	e, err := parseInput(msg.Data)
	if err != nil {
		log.Printf("session %d: dropped input: %v", s.id, err)
		return
	}
	if e.typ == inToggleCursor || e.typ == inHostCursor {
		m.mu.Lock()
		if m.cur == s {
			if e.typ == inHostCursor {
				s.wantCursor = e.down
				m.syncCursor()
			} else if s.live && m.toggleCursor(!s.wantCursor) {
				log.Printf("session %d: manual host cursor fix", s.id)
			}
		}
		m.mu.Unlock()
		return
	}
	s.inputMu.RLock()
	if s.live {
		dispatch(e)
	}
	s.inputMu.RUnlock()
}

func (s *Session) sendControl(v any) {
	s.mu.Lock()
	dc := s.control
	s.mu.Unlock()
	if dc == nil {
		return
	}
	b, _ := json.Marshal(v)
	_ = dc.SendText(string(b))
}

// Streaming reports whether a browser currently owns the stream.
func (m *Manager) Streaming() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur != nil
}

// Close ends the current session, restoring the host cursor.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur != nil {
		m.endLocked(m.cur, reasonShutdown)
	}
}

func (m *Manager) end(s *Session, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.endLocked(s, reason)
}

// endLocked stops s, telling the browser why when reason is set. Only the
// current session owns the Moonlight connection.
func (m *Manager) endLocked(s *Session, reason string) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	dc := s.control
	s.mu.Unlock()

	if m.cur == s {
		s.inputMu.Lock()
		s.live = false
		s.inputMu.Unlock()
		// Show the host cursor again unless another session takes over right
		// away, or the host connection is already gone. The pause lets the
		// toggle keystrokes leave before the input stream shuts down.
		if m.cursor.Hidden && reason != reasonKicked && reason != reasonHostEnded && m.toggleCursor(false) {
			time.Sleep(100 * time.Millisecond)
		}
		moonlight.Stop()
		m.cur = nil
		m.live.Store(nil)
	}
	if dc != nil && reason != "" {
		typ := "ended"
		if reason == reasonKicked {
			typ = "kicked"
		}
		b, _ := json.Marshal(map[string]string{"type": typ, "reason": reason})
		_ = dc.SendText(string(b))
	}
	// Give the control message a moment to leave before closing.
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.pc.Close()
	}()
	log.Printf("session %d: ended (%s)", s.id, reason)
}
