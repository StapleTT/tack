package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"tack/internal/agentproto"
)

// watchClipboard emits the host clipboard text each time it changes, using
// wl-paste --watch. Each change arrives as one base64 line, so text with
// newlines or NULs survives the pipe.
func watchClipboard(ctx context.Context, out chan<- string, logf func(string, ...any)) {
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, "wl-paste", "--no-newline", "--type", "text", "--watch", "sh", "-c", "base64 -w0; echo")
		stdout, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err == nil {
			sc := bufio.NewScanner(stdout)
			sc.Buffer(make([]byte, 64<<10), 2*agentproto.MaxClipboard)
			for sc.Scan() {
				if text, ok := decodeClip(sc.Text()); ok {
					out <- text
				}
			}
			err = cmd.Wait()
		}
		if ctx.Err() == nil {
			logf("clipboard watch stopped: %v (retrying)", err)
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
			}
		}
	}
}

// decodeClip turns one wl-paste line into text, rejecting empty, oversized
// or non-UTF-8 content.
func decodeClip(line string) (string, bool) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(line))
	if err != nil || len(b) == 0 || len(b) > agentproto.MaxClipboard || !utf8.Valid(b) {
		return "", false
	}
	return string(b), true
}

// setClipboard puts text on the host clipboard. wl-copy forks a background
// process that serves it; that child inherits stdout/stderr, so they must
// not be pipes or Wait blocks until the clipboard changes hands.
func setClipboard(ctx context.Context, text string) error {
	cmd := exec.CommandContext(ctx, "wl-copy", "--type", "text/plain;charset=utf-8")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wl-copy: %w", err)
	}
	return nil
}
