package session

import (
	"context"
	"encoding/binary"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"tack/bridge/internal/moonlight"
)

// WebSocket transport, for browsers that can only reach the bridge through
// an HTTP reverse proxy. Text messages carry the control channel's JSON and
// binary messages the input channel's format, both unchanged. Media goes the
// other way as binary messages (docs/protocol.md):
//
//	0x01 flags u8 (bit 0: IDR) pts_us u64 BE, then the Annex B frame
//	0x02 then one Opus packet
const (
	wsVideo = 1
	wsAudio = 2
)

// wsQueue bounds how much media may wait for a slow connection: about a
// quarter second of video at 60 fps. A full queue drops the frame, and
// Moonlight then skips ahead to a fresh IDR frame.
const wsQueue = 16

// rawFrame keeps a received message's payload type, which the stock codecs hide.
type rawFrame struct {
	text bool
	data []byte
}

var rawCodec = websocket.Codec{Unmarshal: func(data []byte, payloadType byte, v any) error {
	f := v.(*rawFrame)
	f.text, f.data = payloadType == websocket.TextFrame, data
	return nil
}}

// StartWS takes over from any current session and streams over ws. Once
// Moonlight is up it returns a channel that closes when the session ends.
// The caller must keep the handler running until then.
func (m *Manager) StartWS(ctx context.Context, req Request, ws *websocket.Conn) (<-chan struct{}, error) {
	if err := req.normalize(); err != nil {
		return nil, err
	}
	out := make(chan any, wsQueue) // string: text message, []byte: binary
	done := make(chan struct{})
	stop := sync.OnceFunc(func() { close(done) })
	go func() {
		for {
			select {
			case v := <-out:
				ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if websocket.Message.Send(ws, v) != nil {
					ws.Close() // the read loop then ends the session
					return
				}
			case <-done:
				return
			}
		}
	}()

	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.takeOverLocked(req)
	if err != nil {
		stop()
		return nil, err
	}
	s.closeT = func() { stop(); ws.Close() }
	// Control messages wait for room; only media is dropped.
	s.sendText = func(t string) {
		select {
		case out <- t:
		case <-done:
		case <-time.After(time.Second):
		}
	}
	h := moonlight.Handlers{
		Video: func(f moonlight.Frame) bool {
			b := make([]byte, 10+len(f.Data))
			b[0] = wsVideo
			if f.IDR {
				b[1] = 1
			}
			binary.BigEndian.PutUint64(b[2:], uint64(f.PTS/time.Microsecond))
			copy(b[10:], f.Data)
			select {
			case out <- b:
				return true
			default:
				return false // Moonlight drops frames until the next IDR
			}
		},
		Audio: func(p []byte) {
			select {
			case out <- append([]byte{wsAudio}, p...):
			default:
			}
		},
	}
	if err := m.launchLocked(ctx, req, s, h); err != nil {
		stop()
		return nil, err
	}
	s.sendText(`{"type":"started"}`)
	for _, msg := range m.agentState() {
		s.sendControl(msg)
	}

	ended := make(chan struct{})
	go func() {
		defer close(ended)
		ws.MaxPayloadBytes = 64 << 10 // clipboard chunks stay below this, as on the data channel
		for {
			var f rawFrame
			if err := rawCodec.Receive(ws, &f); err != nil {
				m.end(s, "")
				return
			}
			if f.text {
				m.handleControl(s, f.data)
			} else {
				m.handleInput(s, f.data)
			}
		}
	}()
	return ended, nil
}
