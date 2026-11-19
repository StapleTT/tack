package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tack/internal/agentproto"
)

// hyprSocket returns the path of one of Hyprland's IPC sockets
// (".socket.sock" for requests, ".socket2.sock" for events).
func hyprSocket(name string) (string, error) {
	sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")
	if sig == "" {
		return "", fmt.Errorf("HYPRLAND_INSTANCE_SIGNATURE not set; run inside the Hyprland session")
	}
	return filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "hypr", sig, name), nil
}

// modeFor maps a window class to a mouse mode.
func modeFor(rule *regexp.Regexp, class string) string {
	if class != "" && rule.MatchString(class) {
		return agentproto.ModeRelative
	}
	return agentproto.ModeAbsolute
}

// hyprRequest sends one request to Hyprland's IPC socket and returns the reply.
func hyprRequest(req string) ([]byte, error) {
	path, err := hyprSocket(".socket.sock")
	if err != nil {
		return nil, err
	}
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte(req)); err != nil {
		return nil, err
	}
	return io.ReadAll(c)
}

// activeClass asks Hyprland for the focused window's class.
func activeClass() (string, error) {
	b, err := hyprRequest("j/activewindow")
	if err != nil {
		return "", err
	}
	var w struct {
		Class string `json:"class"`
	}
	_ = json.Unmarshal(b, &w) // "{}" when nothing is focused
	return w.Class, nil
}

// screenWidth is the focused monitor's width in physical pixels, the space
// Sunshine captures and Hyprland's cursor buffers live in, or 0 if unknown.
// ponytail: assumes Sunshine streams the focused monitor; pass the captured
// one if multi-monitor setups need it.
func screenWidth() int {
	b, err := hyprRequest("j/monitors")
	if err != nil {
		return 0
	}
	var mons []struct {
		Width   int  `json:"width"`
		Focused bool `json:"focused"`
	}
	_ = json.Unmarshal(b, &mons)
	for _, m := range mons {
		if m.Focused {
			return m.Width
		}
	}
	return 0
}

// watchFocus emits the mouse mode whenever the focused window's class moves
// between matching and not matching rule. It reconnects if Hyprland restarts.
func watchFocus(ctx context.Context, rule *regexp.Regexp, modes chan<- string, logf func(string, ...any)) {
	for ctx.Err() == nil {
		if err := followEvents(ctx, rule, modes, logf); err != nil && ctx.Err() == nil {
			logf("hyprland: %v (retrying)", err)
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
			}
		}
	}
}

func followEvents(ctx context.Context, rule *regexp.Regexp, modes chan<- string, logf func(string, ...any)) error {
	path, err := hyprSocket(".socket2.sock")
	if err != nil {
		return err
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		return err
	}
	defer c.Close()
	go func() { <-ctx.Done(); c.Close() }()

	if class, err := activeClass(); err == nil {
		modes <- modeFor(rule, class)
	}
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		// activewindow>>CLASS,TITLE (an empty class means nothing is focused).
		ev, data, ok := strings.Cut(sc.Text(), ">>")
		if !ok || ev != "activewindow" {
			continue
		}
		class, _, _ := strings.Cut(data, ",")
		modes <- modeFor(rule, class)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}
