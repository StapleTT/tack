package main

import (
	"context"
	"time"
)

// Recognising cursor states without pixels. Every cursor change produces a
// frame with the buffer size and hotspot; the theme tells which states have
// that hotspot at that size. Animated cursors (wait, progress) keep
// producing identical frames, one per animation tick, which tells them apart
// from static cursors that share their hotspot.

const (
	// animRepeats identical frames, each within animGap of the last, mean the
	// cursor is animating. Two filters out apps that merely re-set a cursor.
	animRepeats = 2
	animGap     = 500 * time.Millisecond
	// animWait is how long an unmatched shape may wait to prove it animates
	// before it counts as the default cursor.
	animWait = 600 * time.Millisecond
)

type shapeMatcher struct {
	shapes  map[shapeKey][]shapeInfo
	last    cursorFrame
	lastAt  time.Time
	repeats int
}

// frame takes one cursor frame and returns the state to show, or "" with
// wait=true if the shape might still prove to be animated.
func (m *shapeMatcher) frame(f cursorFrame, now time.Time) (state string, wait bool) {
	if f == m.last && now.Sub(m.lastAt) < animGap {
		m.repeats++
	} else {
		m.repeats = 0
	}
	m.last, m.lastAt = f, now
	animating := m.repeats >= animRepeats

	var static, anim string
	for _, s := range m.shapes[shapeKey{f.w, f.hotX, f.hotY}] {
		if s.animated && anim == "" {
			anim = s.state
		} else if !s.animated && static == "" {
			static = s.state
		}
	}
	switch {
	case animating && anim != "":
		return anim, false
	case static != "":
		return static, false
	case anim != "":
		return "", true // not animating (yet)
	}
	return "default", false
}

// cursorState is a recognised host cursor and its buffer width.
type cursorState struct {
	name string
	px   int
}

// matchCursor turns cursor frames into state changes on out.
func matchCursor(ctx context.Context, shapes map[shapeKey][]shapeInfo, frames <-chan cursorFrame, out chan<- cursorState) {
	m := &shapeMatcher{shapes: shapes}
	var current cursorState
	var timeout <-chan time.Time
	emit := func(s string) {
		timeout = nil
		if st := (cursorState{s, m.last.w}); st != current {
			current = st
			select {
			case out <- st:
			case <-ctx.Done():
			}
		}
	}
	for {
		select {
		case f := <-frames:
			if s, wait := m.frame(f, time.Now()); wait {
				if timeout == nil {
					timeout = time.After(animWait)
				}
			} else {
				emit(s)
			}
		case <-timeout:
			emit("default")
		case <-ctx.Done():
			return
		}
	}
}
