package main

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"tack/internal/agentproto"
)

func TestModeFor(t *testing.T) {
	rule := regexp.MustCompile(`^(steam_app_\d+|gamescope)$`)
	cases := map[string]string{
		"steam_app_1091500": agentproto.ModeRelative,
		"gamescope":         agentproto.ModeRelative,
		"steam":             agentproto.ModeAbsolute, // the Steam client itself is a desktop app
		"dev.zed.Zed":       agentproto.ModeAbsolute,
		"":                  agentproto.ModeAbsolute, // nothing focused
	}
	for class, want := range cases {
		if got := modeFor(rule, class); got != want {
			t.Errorf("modeFor(%q) = %s, want %s", class, got, want)
		}
	}
}

func TestDecodeClip(t *testing.T) {
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	if got, ok := decodeClip(enc("line 1\nline 2\x00 ✓") + "\n"); !ok || got != "line 1\nline 2\x00 ✓" {
		t.Errorf("round trip failed: %q %v", got, ok)
	}
	for name, line := range map[string]string{
		"empty":     "",
		"not utf-8": enc("\xff\xfe"),
		"too big":   enc(strings.Repeat("a", agentproto.MaxClipboard+1)),
		"garbage":   "!!!",
	} {
		if _, ok := decodeClip(line); ok {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestStatStart(t *testing.T) {
	stat := "3780 (sun shine) S 1 3780 3780 0 -1 4194560 1 2 3 4 5 6 7 8 20 0 40 0 123456 999 42"
	if got := statStart(stat); got != 123456 {
		t.Fatalf("statStart = %d", got)
	}
	if statStart("garbage") != 0 || statStart("1 (x) S 1") != 0 {
		t.Fatal("bad stat parsed")
	}
	if start := sunshineStart(); start != 0 && sunshineUptime(start) <= 0 {
		t.Fatalf("running sunshine has uptime %v", sunshineUptime(start))
	}
}

func TestParseXcursor(t *testing.T) {
	le := binary.LittleEndian
	image := func(size, hot uint32, argb uint32) []byte {
		h := make([]byte, 36+size*size*4)
		for i, v := range []uint32{36, 0xfffd0002, size, 1, size, size, hot, hot, 0} {
			le.PutUint32(h[i*4:], v)
		}
		for i := uint32(0); i < size*size; i++ {
			le.PutUint32(h[36+i*4:], argb)
		}
		return h
	}
	small, large := image(2, 1, 0xff000000), image(4, 3, 0x80402010)
	b := append([]byte("Xcur"), make([]byte, 12+2*12)...)
	le.PutUint32(b[4:], 16)
	le.PutUint32(b[12:], 2)
	for i, e := range [][3]uint32{{0xfffd0002, 2, uint32(len(b))}, {0xfffd0002, 4, uint32(len(b) + len(small))}} {
		le.PutUint32(b[16+i*12:], e[0])
		le.PutUint32(b[20+i*12:], e[1])
		le.PutUint32(b[24+i*12:], e[2])
	}
	b = append(append(b, small...), large...)

	imgs, err := parseXcursor(b)
	if err != nil || len(imgs) != 2 {
		t.Fatalf("got %d images, %v", len(imgs), err)
	}
	big := imgs[1]
	if big.Bounds().Dx() != 4 || big.hotX != 3 || big.hotY != 3 {
		t.Fatalf("second image %v hot %d,%d", big.Bounds(), big.hotX, big.hotY)
	}
	if p := big.Pix[:4]; p[0] != 0x40 || p[1] != 0x20 || p[2] != 0x10 || p[3] != 0x80 {
		t.Fatalf("pixel %x, want 40201080", p)
	}
	if _, err := parseXcursor(b[:20]); err == nil {
		t.Fatal("truncated file parsed")
	}
}

func TestShapeMatcher(t *testing.T) {
	m := &shapeMatcher{shapes: map[shapeKey][]shapeInfo{
		{32, 5, 4}:   {{"default", false}},
		{32, 12, 3}:  {{"pointer", false}},
		{32, 16, 16}: {{"wait", true}},
		{32, 1, 9}:   {{"progress", true}},
	}}
	now := time.Now()
	step := func(f cursorFrame, d time.Duration) (string, bool) { now = now.Add(d); return m.frame(f, now) }
	arrow, hand, spin := cursorFrame{32, 32, 5, 4}, cursorFrame{32, 32, 12, 3}, cursorFrame{32, 32, 16, 16}

	if s, _ := step(hand, 0); s != "pointer" {
		t.Fatalf("hand = %q", s)
	}
	// A static cursor re-set quickly stays itself.
	for i := 0; i < 4; i++ {
		if s, _ := step(hand, 50*time.Millisecond); s != "pointer" {
			t.Fatalf("repeated hand = %q", s)
		}
	}
	// An animated shape waits until its frames repeat, then shows.
	if s, wait := step(spin, time.Second); s != "" || !wait {
		t.Fatalf("first spinner frame = %q, %v", s, wait)
	}
	step(spin, 166*time.Millisecond)
	if s, _ := step(spin, 166*time.Millisecond); s != "wait" {
		t.Fatalf("animating spinner = %q", s)
	}
	// Unknown shapes are the default cursor.
	if s, _ := step(cursorFrame{32, 32, 15, 15}, time.Second); s != "default" {
		t.Fatalf("unknown = %q", s)
	}
	if s, _ := step(arrow, time.Second); s != "default" {
		t.Fatalf("arrow = %q", s)
	}
}

func TestLoadCursorTheme(t *testing.T) {
	theme := os.Getenv("XCURSOR_THEME")
	if theme == "" {
		t.Skip("no XCURSOR_THEME")
	}
	ct, err := loadCursorTheme(theme)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range ct.shapes {
		t.Logf("%v %v", k, v)
	}
	if len(ct.images) < 2 {
		t.Fatalf("only %d states", len(ct.images))
	}
}
