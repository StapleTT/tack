package session

import (
	"testing"
	"time"
)

func TestResumeOwnership(t *testing.T) {
	now := time.Now()
	m := Manager{owner: "first", cur: &Session{}}
	if !m.canResume("first", now) || m.canResume("second", now) {
		t.Fatal("incorrect live owner")
	}
	m.resumeUntil = now.Add(30 * time.Second)
	m.cur = nil
	if !m.canResume("first", now) || m.canResume("second", now) || m.canResume("first", m.resumeUntil) {
		t.Fatal("incorrect reconnect grace")
	}
	m.owner = "second"
	if m.canResume("first", now) {
		t.Fatal("old client can undo takeover")
	}
}
