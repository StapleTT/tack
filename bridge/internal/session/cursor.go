package session

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"tack/bridge/internal/moonlight"
)

// Sunshine draws the host cursor into the video according to one global
// flag that only flips via its Ctrl+Alt+Shift+N shortcut, survives sessions,
// and resets to "drawn" when Sunshine restarts. The bridge cannot read it, so
// it keeps a record on disk (a bridge crash must not invert it) and drops the
// record when the agent reports a Sunshine restart since the last toggle.
//
// Wanted state: hidden while the browser draws its own cursor, drawn while
// the pointer is locked. Hidden across takeovers; drawn when idle.

type cursorState struct {
	Hidden  bool      `json:"hidden"`
	Toggled time.Time `json:"toggled"` // last time the flag flipped
}

func (m *Manager) loadCursor() {
	b, err := os.ReadFile(m.cursorPath)
	if err == nil {
		err = json.Unmarshal(b, &m.cursor)
	}
	if err != nil && !os.IsNotExist(err) {
		log.Printf("host cursor state: %v (assuming drawn)", err)
	}
}

// toggleCursor flips Sunshine's flag and records the resulting state. m.mu must be held and
// the current session live; nothing changes if the keystroke wasn't sent.
func (m *Manager) toggleCursor(hidden bool) bool {
	if !moonlight.ToggleHostCursor() {
		log.Print("host cursor toggle not sent")
		return false
	}
	m.cursor = cursorState{Hidden: hidden, Toggled: time.Now()}
	m.saveCursor()
	return true
}

func (m *Manager) saveCursor() {
	b, _ := json.Marshal(m.cursor)
	if err := os.WriteFile(m.cursorPath, b, 0o600); err != nil {
		log.Printf("host cursor state: %v", err)
	}
}

// syncCursor makes Sunshine's flag match what the current session wants.
// m.mu must be held.
func (m *Manager) syncCursor() {
	s := m.cur
	if s == nil {
		return
	}
	s.inputMu.RLock()
	live := s.live
	s.inputMu.RUnlock()
	if live && m.cursor.Hidden == s.wantCursor {
		m.toggleCursor(!s.wantCursor)
	}
}

// sunshineUp handles the agent's report that Sunshine has run for uptime.
// If it restarted after our last toggle, its flag is back to "drawn".
func (m *Manager) sunshineUp(uptime time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cursor.Hidden && m.cursor.Toggled.Before(time.Now().Add(-uptime)) {
		log.Print("host cursor: Sunshine restarted since the last toggle, so it draws the cursor again")
		m.cursor.Hidden = false
		m.saveCursor()
		m.syncCursor()
	}
}
