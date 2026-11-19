package session

import "testing"

func TestParseInput(t *testing.T) {
	// Gamepad state: slot 1, mask 0b11, A+LB, triggers 10/250, sticks.
	pad := []byte{inPad, 1, 3, 0, 0x00, 0x11, 0, 0, 10, 250,
		0x00, 0x80, // lx -32768
		0xff, 0x7f, // ly 32767
		0x01, 0x00, // rx 1
		0xff, 0xff} // ry -1
	e, err := parseInput(pad)
	if err != nil {
		t.Fatal(err)
	}
	if e.slot != 1 || e.mask != 3 || e.buttons != 0x1100 || e.lt != 10 || e.rt != 250 ||
		e.lx != -32768 || e.ly != 32767 || e.rx != 1 || e.ry != -1 {
		t.Fatalf("pad parsed wrong: %+v", e)
	}

	e, err = parseInput([]byte{inMouseAbs, 0x80, 0x07, 0x38, 0x04, 0x80, 0x07, 0x38, 0x04})
	if err != nil || e.x != 1920 || e.y != 1080 || e.refW != 1920 || e.refH != 1080 {
		t.Fatalf("abs parsed wrong: %+v %v", e, err)
	}

	e, err = parseInput([]byte{inKey, 0x4e, 0x00, 1, 0x07})
	if err != nil || e.vk != 0x4e || !e.down || e.mods != 7 {
		t.Fatalf("key parsed wrong: %+v %v", e, err)
	}

	bad := [][]byte{
		nil,
		{99},                                 // unknown type
		{inKey, 0x4e, 0x00, 1},               // short
		{inMouseButton, 9, 1},                // bad button
		{inPadArrival, 4, 1, 0},              // slot out of range
		{inMouseAbs, 1, 0, 1, 0, 0, 0, 0, 0}, // zero reference size
	}
	for _, b := range bad {
		if _, err := parseInput(b); err == nil {
			t.Errorf("parseInput(%v) accepted bad input", b)
		}
	}
}
