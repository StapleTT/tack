package session

import (
	"encoding/binary"
	"fmt"

	"tack/bridge/internal/moonlight"
)

// Input message types on the "input" data channel. Every message is binary,
// little-endian, starting with one type byte. See docs/protocol.md.
const (
	inMouseAbs     = 1 // x, y, refW, refH u16
	inMouseRel     = 2 // dx, dy i16
	inMouseButton  = 3 // button u8, down u8
	inKey          = 4 // vk u16, down u8, mods u8
	inScroll       = 5 // vertical, horizontal i16 (120 = one notch; +v up, +h right)
	inPad          = 6 // slot u8, mask u16, buttons u32, lt u8, rt u8, lx ly rx ry i16
	inPadArrival   = 7 // slot u8, mask u16
	inToggleCursor = 8 // (none) manual recovery if the host cursor state is wrong
)

var inputLen = map[byte]int{
	inMouseAbs: 9, inMouseRel: 5, inMouseButton: 3, inKey: 5,
	inScroll: 5, inPad: 18, inPadArrival: 4, inToggleCursor: 1,
}

type inputEvent struct {
	typ              byte
	x, y, refW, refH uint16
	dx, dy           int16
	button           int
	down             bool
	vk               uint16
	mods             byte
	slot             uint8
	mask             uint16
	buttons          uint32
	lt, rt           uint8
	lx, ly, rx, ry   int16
}

func parseInput(b []byte) (inputEvent, error) {
	if len(b) == 0 {
		return inputEvent{}, fmt.Errorf("empty input message")
	}
	want, ok := inputLen[b[0]]
	if !ok {
		return inputEvent{}, fmt.Errorf("unknown input type %d", b[0])
	}
	if len(b) != want {
		return inputEvent{}, fmt.Errorf("input type %d: %d bytes, want %d", b[0], len(b), want)
	}
	le := binary.LittleEndian
	u16 := func(i int) uint16 { return le.Uint16(b[i:]) }
	i16 := func(i int) int16 { return int16(le.Uint16(b[i:])) }
	e := inputEvent{typ: b[0]}
	switch e.typ {
	case inMouseAbs:
		e.x, e.y, e.refW, e.refH = u16(1), u16(3), u16(5), u16(7)
		if e.refW == 0 || e.refH == 0 {
			return e, fmt.Errorf("mouse position with zero reference size")
		}
	case inMouseRel:
		e.dx, e.dy = i16(1), i16(3)
	case inMouseButton:
		e.button, e.down = int(b[1]), b[2] != 0
		if e.button < moonlight.ButtonLeft || e.button > moonlight.ButtonX2 {
			return e, fmt.Errorf("bad mouse button %d", e.button)
		}
	case inKey:
		e.vk, e.down, e.mods = u16(1), b[3] != 0, b[4]
	case inScroll:
		e.dy, e.dx = i16(1), i16(3)
	case inPad:
		e.slot, e.mask, e.buttons = b[1], u16(2), le.Uint32(b[4:])
		e.lt, e.rt = b[8], b[9]
		e.lx, e.ly, e.rx, e.ry = i16(10), i16(12), i16(14), i16(16)
	case inPadArrival:
		e.slot, e.mask = b[1], u16(2)
	}
	if (e.typ == inPad || e.typ == inPadArrival) && e.slot > 3 {
		return e, fmt.Errorf("bad gamepad slot %d", e.slot)
	}
	return e, nil
}

// dispatch forwards one parsed event to the Moonlight input stream.
func dispatch(e inputEvent) {
	switch e.typ {
	case inMouseAbs:
		moonlight.MousePosition(int16(e.x), int16(e.y), int16(e.refW), int16(e.refH))
	case inMouseRel:
		moonlight.MouseMove(e.dx, e.dy)
	case inMouseButton:
		moonlight.MouseButton(e.button, e.down)
	case inKey:
		moonlight.Key(int16(e.vk), e.down, e.mods)
	case inScroll:
		if e.dy != 0 {
			moonlight.Scroll(e.dy)
		}
		if e.dx != 0 {
			moonlight.HScroll(e.dx)
		}
	case inPad:
		moonlight.Controller(e.slot, e.mask, e.buttons, e.lt, e.rt, e.lx, e.ly, e.rx, e.ry)
	case inPadArrival:
		moonlight.ControllerArrival(e.slot, e.mask)
	}
}
