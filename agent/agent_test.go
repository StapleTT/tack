package main

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

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
