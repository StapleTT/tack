package session

import (
	"encoding/json"
	"log"

	"github.com/pion/webrtc/v4"

	"tack/internal/agentproto"
)

// clipChunk is the clipboard chunk size in characters. Even if every
// character JSON-escapes to six bytes, a chunk stays under pion's default
// 64 KiB data channel message limit.
const clipChunk = 8192

// SetAgent registers the host agent's sender, replacing (and closing) any
// previous agent connection. It returns an id for ClearAgent.
func (m *Manager) SetAgent(send func(agentproto.Message) error, closeConn func()) int {
	m.agentMu.Lock()
	old := m.agentClose
	m.agentID++
	m.agentSend, m.agentClose = send, closeConn
	id := m.agentID
	m.agentMu.Unlock()
	if old != nil {
		old()
	}
	return id
}

// ClearAgent forgets agent id if it is still the current one.
func (m *Manager) ClearAgent(id int) {
	m.agentMu.Lock()
	defer m.agentMu.Unlock()
	if m.agentID == id {
		m.agentSend, m.agentClose, m.agentMode = nil, nil, ""
	}
}

// FromAgent routes one agent message to the current browser.
func (m *Manager) FromAgent(msg agentproto.Message) {
	s := m.live.Load()
	switch msg.Type {
	case agentproto.TypeMode:
		if msg.Mode != agentproto.ModeAbsolute && msg.Mode != agentproto.ModeRelative {
			return
		}
		m.agentMu.Lock()
		m.agentMode = msg.Mode
		m.agentMu.Unlock()
		if s != nil {
			s.sendControl(map[string]string{"type": "mode", "mode": msg.Mode})
		}
	case agentproto.TypeClipboard:
		if s != nil && len(msg.Text) <= agentproto.MaxClipboard {
			chunks := chunkText(msg.Text, clipChunk)
			for i, c := range chunks {
				s.sendControl(map[string]any{"type": "clipboard", "text": c, "more": i < len(chunks)-1})
			}
		}
	case agentproto.TypeClipboardAck:
		if s != nil {
			s.sendControl(map[string]any{"type": "clipboard-ack", "id": msg.ID})
		}
	}
}

func (m *Manager) toAgent(msg agentproto.Message) bool {
	m.agentMu.Lock()
	send := m.agentSend
	m.agentMu.Unlock()
	return send != nil && send(msg) == nil
}

func (m *Manager) mode() string {
	m.agentMu.Lock()
	defer m.agentMu.Unlock()
	return m.agentMode
}

// handleControl takes browser messages on the control channel. Clipboard
// text arrives in chunks; the last one (more=false) carries the paste id.
func (m *Manager) handleControl(s *Session, msg webrtc.DataChannelMessage) {
	if !msg.IsString {
		return
	}
	var c struct {
		Type string `json:"type"`
		Text string `json:"text"`
		More bool   `json:"more"`
		ID   int    `json:"id"`
	}
	if json.Unmarshal(msg.Data, &c) != nil || c.Type != "clipboard" {
		return
	}
	// pion delivers one channel's messages in order on one goroutine, so the
	// buffer needs no lock.
	if !s.clipTooBig && s.clip.Len()+len(c.Text) <= agentproto.MaxClipboard {
		s.clip.WriteString(c.Text)
	} else {
		s.clipTooBig = true
	}
	if c.More {
		return
	}
	text, tooBig := s.clip.String(), s.clipTooBig
	s.clip.Reset()
	s.clipTooBig = false
	if tooBig {
		log.Printf("session %d: clipboard over %d bytes dropped", s.id, agentproto.MaxClipboard)
	}
	// Without an agent (or with oversized text) ack right away so the
	// browser's held paste keystroke still reaches the host.
	if tooBig || !m.toAgent(agentproto.Message{Type: agentproto.TypeClipboard, Text: text, ID: c.ID}) {
		s.sendControl(map[string]any{"type": "clipboard-ack", "id": c.ID})
	}
}

// chunkText splits s into pieces of at most n runes.
func chunkText(s string, n int) []string {
	if s == "" {
		return []string{""}
	}
	var out []string
	runes := []rune(s)
	for len(runes) > n {
		out = append(out, string(runes[:n]))
		runes = runes[n:]
	}
	return append(out, string(runes))
}
