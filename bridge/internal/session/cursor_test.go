package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCursorRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host-cursor.json")
	m := &Manager{cursorPath: path, cursor: cursorState{Hidden: true, Toggled: time.Now().Add(-time.Hour)}}
	m.saveCursor()

	// A restarted bridge reads the record back.
	m = &Manager{cursorPath: path}
	m.loadCursor()
	if !m.cursor.Hidden {
		t.Fatal("record not reloaded")
	}
	// Sunshine older than the last toggle still has the flag we set.
	m.sunshineUp(2 * time.Hour)
	if !m.cursor.Hidden {
		t.Fatal("record dropped though Sunshine did not restart")
	}
	// Sunshine restarted after the toggle: its flag is back to drawn.
	m.sunshineUp(10 * time.Minute)
	if m.cursor.Hidden {
		t.Fatal("record kept across a Sunshine restart")
	}
	// The next bridge must hide the cursor even if it starts before the agent.
	m = &Manager{cursorPath: path}
	m.loadCursor()
	if m.cursor.Hidden {
		t.Fatal("reloaded stale hidden record after Sunshine restarted")
	}
}
