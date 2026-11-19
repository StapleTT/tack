package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// A minimal Wayland client: just enough of the wire protocol to open an
// ext-image-copy-capture-v1 pointer cursor session and learn, on every
// cursor change, the cursor buffer size and hotspot.
//
// Hyprland does not draw shape cursors (the ones apps pick by name) into the
// captured buffer; it comes back transparent. So the pixels are ignored, and
// shapes are recognised by hotspot and animation instead (see cursorshape.go).

// cursorFrame is the cursor as of one captured frame.
type cursorFrame struct{ w, h, hotX, hotY int }

const (
	shmARGB8888 = 0
	shmXRGB8888 = 1
)

type wlConn struct {
	c    *net.UnixConn
	next uint32
	rbuf []byte
}

func wlConnect() (*wlConn, error) {
	name := os.Getenv("WAYLAND_DISPLAY")
	if name == "" {
		name = "wayland-0"
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), name)
	}
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: name, Net: "unix"})
	if err != nil {
		return nil, err
	}
	return &wlConn{c: c, next: 2}, nil // 1 is wl_display
}

func (w *wlConn) newID() uint32 { w.next++; return w.next - 1 }

// wlFD marks a request argument passed as a file descriptor.
type wlFD int

// send writes one request. Arguments: uint32, int32, string, or wlFD.
func (w *wlConn) send(obj uint32, op uint16, args ...any) error {
	b := make([]byte, 8, 64)
	var oob []byte
	for _, a := range args {
		switch v := a.(type) {
		case uint32:
			b = binary.LittleEndian.AppendUint32(b, v)
		case int32:
			b = binary.LittleEndian.AppendUint32(b, uint32(v))
		case string:
			b = binary.LittleEndian.AppendUint32(b, uint32(len(v)+1))
			b = append(b, v...)
			b = append(b, make([]byte, 4-len(v)%4)...) // NUL and padding
		case wlFD:
			oob = syscall.UnixRights(int(v))
		default:
			panic(fmt.Sprintf("wayland: bad argument %T", a))
		}
	}
	binary.LittleEndian.PutUint32(b, obj)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b))<<16|uint32(op))
	_, _, err := w.c.WriteMsgUnix(b, oob, nil)
	return err
}

// read returns the next event.
func (w *wlConn) read() (obj uint32, op uint16, body []byte, err error) {
	for {
		if len(w.rbuf) >= 8 {
			size := int(binary.LittleEndian.Uint32(w.rbuf[4:]) >> 16)
			if size < 8 {
				return 0, 0, nil, errors.New("wayland: bad message")
			}
			if len(w.rbuf) >= size {
				obj = binary.LittleEndian.Uint32(w.rbuf)
				op = uint16(binary.LittleEndian.Uint32(w.rbuf[4:]))
				body = w.rbuf[8:size]
				w.rbuf = w.rbuf[size:]
				return obj, op, body, nil
			}
		}
		buf := make([]byte, 4096)
		n, err := w.c.Read(buf)
		if err != nil {
			return 0, 0, nil, err
		}
		w.rbuf = append(w.rbuf, buf[:n]...)
	}
}

func u32(b []byte, i int) uint32 { return binary.LittleEndian.Uint32(b[i*4:]) }

// wlString reads a string argument at byte offset off.
func wlString(b []byte, off int) (string, int) {
	n := int(binary.LittleEndian.Uint32(b[off:]))
	s := strings.TrimRight(string(b[off+4:off+4+n]), "\x00")
	return s, off + 4 + (n+3)&^3
}

// watchCursor emits a cursorFrame every time the host cursor changes,
// reconnecting if the compositor goes away.
func watchCursor(ctx context.Context, out chan<- cursorFrame, logf func(string, ...any)) {
	for ctx.Err() == nil {
		err := followCursor(ctx, out)
		if ctx.Err() != nil {
			return
		}
		logf("cursor shapes: %v (retrying)", err)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

func followCursor(ctx context.Context, out chan<- cursorFrame) error {
	w, err := wlConnect()
	if err != nil {
		return err
	}
	defer w.c.Close()
	go func() { <-ctx.Done(); w.c.Close() }()

	// Collect globals up to a sync callback.
	registry, done := w.newID(), w.newID()
	w.send(1, 1, registry) // wl_display.get_registry
	w.send(1, 0, done)     // wl_display.sync
	want := map[string]uint32{"wl_shm": 0, "wl_seat": 0, "wl_output": 0,
		"ext_output_image_capture_source_manager_v1": 0, "ext_image_copy_capture_manager_v1": 0}
	for synced := false; !synced; {
		obj, op, body, err := w.read()
		if err != nil {
			return err
		}
		switch {
		case obj == 1 && op == 0:
			msg, _ := wlString(body, 8)
			return fmt.Errorf("wayland error %d: %s", u32(body, 1), msg)
		case obj == registry && op == 0: // global(name, interface, version)
			iface, _ := wlString(body, 4)
			if id, ok := want[iface]; ok && id == 0 {
				want[iface] = u32(body, 0)
			}
		case obj == done:
			synced = true
		}
	}
	ids := map[string]uint32{}
	for iface, name := range want {
		if name == 0 {
			return fmt.Errorf("compositor lacks %s", iface)
		}
		ids[iface] = w.newID()
		w.send(registry, 0, name, iface, uint32(1), ids[iface]) // wl_registry.bind
	}
	pointer, source, cursorSession, session := w.newID(), w.newID(), w.newID(), w.newID()
	w.send(ids["wl_seat"], 0, pointer)                                                     // get_pointer
	w.send(ids["ext_output_image_capture_source_manager_v1"], 0, source, ids["wl_output"]) // create_source
	w.send(ids["ext_image_copy_capture_manager_v1"], 1, cursorSession, source, pointer)    // create_pointer_cursor_session
	w.send(cursorSession, 1, session)                                                      // get_capture_session

	var (
		cur           cursorFrame
		format        = uint32(0xffffffff)
		buffer, frame uint32
		bufW, bufH    int
		shmFile       *os.File
	)
	defer func() {
		if shmFile != nil {
			shmFile.Close()
		}
	}()
	capture := func() {
		if frame != 0 || buffer == 0 {
			return
		}
		frame = w.newID()
		w.send(session, 0, frame)                                      // create_frame
		w.send(frame, 1, buffer)                                       // attach_buffer
		w.send(frame, 2, int32(0), int32(0), int32(bufW), int32(bufH)) // damage_buffer
		w.send(frame, 3)                                               // capture
	}
	// rearm (re)makes the buffer if the cursor size changed and starts the
	// next capture. Hyprland completes it on the next cursor change.
	failures := 0
	rearm := func() error {
		if frame != 0 || cur.w <= 0 {
			return nil
		}
		if cur.w != bufW || cur.h != bufH {
			if buffer != 0 {
				w.send(buffer, 0) // wl_buffer.destroy
				shmFile.Close()
			}
			bufW, bufH = cur.w, cur.h
			var err error
			if shmFile, buffer, err = shmBuffer(w, ids["wl_shm"], bufW, bufH, format); err != nil {
				return err
			}
		}
		capture()
		return nil
	}
	for {
		obj, op, body, err := w.read()
		if err != nil {
			return err
		}
		switch obj {
		case 1:
			if op == 0 {
				msg, _ := wlString(body, 8)
				return fmt.Errorf("wayland error %d: %s", u32(body, 1), msg)
			}
		case cursorSession:
			if op == 3 { // hotspot
				// Hyprland sends this after the frame it belongs to (the spec
				// has it before), so report the change right away as well.
				cur.hotX, cur.hotY = int(int32(u32(body, 0))), int(int32(u32(body, 1)))
				if cur.w > 0 {
					select {
					case out <- cur:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
		case session:
			switch op {
			case 0: // buffer_size
				cur.w, cur.h = int(u32(body, 0)), int(u32(body, 1))
			case 1: // shm_format
				if f := u32(body, 0); f == shmARGB8888 || (f == shmXRGB8888 && format == 0xffffffff) {
					format = f
				}
			case 4: // done
				if format == 0xffffffff || cur.w <= 0 || cur.h <= 0 || cur.w > 512 || cur.h > 512 {
					return errors.New("no usable cursor buffer format")
				}
				if err := rearm(); err != nil {
					return err
				}
			case 5: // stopped
				return errors.New("cursor session stopped")
			}
		case frame:
			switch op {
			case 3: // ready
				w.send(frame, 0) // destroy
				frame = 0
				failures = 0
				select {
				case out <- cur:
				case <-ctx.Done():
					return ctx.Err()
				}
				capture()
			case 4: // failed, usually because the constraints changed under it
				w.send(frame, 0)
				frame = 0
				if failures++; failures > 10 {
					return fmt.Errorf("cursor capture keeps failing (reason %d)", u32(body, 0))
				}
				if err := rearm(); err != nil {
					return err
				}
			}
		}
	}
}

// shmBuffer makes a w x h wl_buffer backed by an unlinked file in
// XDG_RUNTIME_DIR (tmpfs).
func shmBuffer(w *wlConn, shm uint32, width, height int, format uint32) (*os.File, uint32, error) {
	f, err := os.CreateTemp(os.Getenv("XDG_RUNTIME_DIR"), "tack-cursor-*")
	if err != nil {
		return nil, 0, err
	}
	os.Remove(f.Name())
	size := width * height * 4
	if err := f.Truncate(int64(size)); err != nil {
		f.Close()
		return nil, 0, err
	}
	pool, buffer := w.newID(), w.newID()
	w.send(shm, 0, pool, wlFD(f.Fd()), int32(size))                                        // create_pool
	w.send(pool, 0, buffer, int32(0), int32(width), int32(height), int32(width*4), format) // create_buffer
	w.send(pool, 1)                                                                        // pool.destroy
	return f, buffer, nil
}
