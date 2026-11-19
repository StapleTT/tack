package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"tack/internal/agentproto"
)

// cursorStates are the host cursor states the browser can show, each with
// the XCursor names a theme may use for it. The keys double as CSS cursor
// keywords, which the browser falls back to.
var cursorStates = map[string][]string{
	"default":  {"default", "left_ptr"},
	"pointer":  {"pointer", "hand2", "pointing_hand"},
	"text":     {"text", "xterm", "ibeam"},
	"wait":     {"wait", "watch"},
	"progress": {"progress", "left_ptr_watch"},
}

// shapeKey identifies a cursor shape by what Hyprland reveals about it: the
// buffer width and the hotspot within it.
type shapeKey struct{ w, hotX, hotY int }

// shapeInfo is one theme cursor as seen at one size.
type shapeInfo struct {
	state    string
	animated bool
}

// cursorTheme is what the agent knows about the host's XCursor theme.
type cursorTheme struct {
	images map[string]agentproto.CursorImage // state -> largest first frame
	shapes map[shapeKey][]shapeInfo
}

// loadCursorTheme reads every state's cursor from theme. States the theme
// lacks are left out (the browser then uses its own keyword cursor).
func loadCursorTheme(theme string) (*cursorTheme, error) {
	t := &cursorTheme{images: map[string]agentproto.CursorImage{}, shapes: map[shapeKey][]shapeInfo{}}
	for state, names := range cursorStates {
		path, err := findCursor(theme, names, map[string]bool{})
		if err != nil {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		imgs, err := parseXcursor(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		frames := map[int]int{} // width -> frame count
		for _, im := range imgs {
			frames[im.Bounds().Dx()]++
		}
		var best *xcImage
		for i, im := range imgs {
			w := im.Bounds().Dx()
			if frames[w] > 0 { // first frame of this size
				k := shapeKey{w, im.hotX, im.hotY}
				t.shapes[k] = append(t.shapes[k], shapeInfo{state, frames[w] > 1})
				frames[w] = -frames[w]
			}
			if best == nil || w > best.Bounds().Dx() {
				best = &imgs[i]
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, best.RGBA); err != nil {
			return nil, err
		}
		t.images[state] = agentproto.CursorImage{PNG: buf.Bytes(), HotX: best.hotX, HotY: best.hotY}
	}
	if _, ok := t.images["default"]; !ok {
		return nil, fmt.Errorf("cursor theme %q has no default pointer", theme)
	}
	return t, nil
}

// iconDirs lists where XCursor themes live, in lookup order.
func iconDirs() []string {
	home, _ := os.UserHomeDir()
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	dirs := []string{filepath.Join(data, "icons"), filepath.Join(home, ".icons")}
	sys := os.Getenv("XDG_DATA_DIRS")
	if sys == "" {
		sys = "/usr/local/share:/usr/share"
	}
	for _, d := range filepath.SplitList(sys) {
		dirs = append(dirs, filepath.Join(d, "icons"))
	}
	return dirs
}

// findCursor finds the first of names in theme, following Inherits=.
func findCursor(theme string, names []string, seen map[string]bool) (string, error) {
	if seen[theme] {
		return "", errors.New("not found")
	}
	seen[theme] = true
	var inherits []string
	for _, dir := range iconDirs() {
		for _, name := range names {
			if p := filepath.Join(dir, theme, "cursors", name); fileExists(p) {
				return p, nil
			}
		}
		if f, err := os.Open(filepath.Join(dir, theme, "index.theme")); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "Inherits="); ok {
					for _, t := range strings.Split(v, ",") {
						if t = strings.Trim(strings.TrimSpace(t), `"`); t != "" {
							inherits = append(inherits, t)
						}
					}
				}
			}
			f.Close()
		}
	}
	for _, t := range inherits {
		if p, err := findCursor(t, names, seen); err == nil {
			return p, nil
		}
	}
	return "", errors.New("not found")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

type xcImage struct {
	*image.RGBA
	hotX, hotY int
}

// parseXcursor returns every image in an XCursor file in file order (all
// sizes, all animation frames). Pixels are premultiplied ARGB, which is what
// image.RGBA holds.
func parseXcursor(b []byte) ([]xcImage, error) {
	le := binary.LittleEndian
	if len(b) < 16 || string(b[:4]) != "Xcur" {
		return nil, errors.New("not an XCursor file")
	}
	var out []xcImage
	n := int(le.Uint32(b[12:]))
	for i := 0; i < n && 16+i*12+12 <= len(b); i++ {
		e := b[16+i*12:]
		pos := int(le.Uint32(e[8:]))
		if le.Uint32(e) != 0xfffd0002 || pos+36 > len(b) {
			continue
		}
		h := b[pos:]
		w, ht := int(le.Uint32(h[16:])), int(le.Uint32(h[20:]))
		if w <= 0 || ht <= 0 || w > 256 || ht > 256 || pos+36+w*ht*4 > len(b) {
			return nil, errors.New("bad image size")
		}
		img := image.NewRGBA(image.Rect(0, 0, w, ht))
		px := h[36:]
		for j := 0; j < w*ht; j++ {
			v := le.Uint32(px[j*4:])
			img.Pix[j*4+0] = byte(v >> 16) // R
			img.Pix[j*4+1] = byte(v >> 8)  // G
			img.Pix[j*4+2] = byte(v)       // B
			img.Pix[j*4+3] = byte(v >> 24) // A
		}
		out = append(out, xcImage{img, int(le.Uint32(h[24:])), int(le.Uint32(h[28:]))})
	}
	if len(out) == 0 {
		return nil, errors.New("no image")
	}
	return out, nil
}
