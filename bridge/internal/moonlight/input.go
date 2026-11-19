package moonlight

/*
#include "Limelight.h"
*/
import "C"

// Mouse buttons, mirroring BUTTON_* in Limelight.h.
const (
	ButtonLeft   = C.BUTTON_LEFT
	ButtonMiddle = C.BUTTON_MIDDLE
	ButtonRight  = C.BUTTON_RIGHT
	ButtonX1     = C.BUTTON_X1
	ButtonX2     = C.BUTTON_X2
)

// Key modifiers, mirroring MODIFIER_* in Limelight.h.
const (
	ModShift = C.MODIFIER_SHIFT
	ModCtrl  = C.MODIFIER_CTRL
	ModAlt   = C.MODIFIER_ALT
	ModMeta  = C.MODIFIER_META
)

// Windows virtual-key codes used directly by the bridge.
const (
	VKLShift   = 0xA0
	VKLControl = 0xA2
	VKLMenu    = 0xA4
	VKN        = 0x4E
)

func MouseMove(dx, dy int16) { C.LiSendMouseMoveEvent(C.short(dx), C.short(dy)) }

// MousePosition moves the host cursor to (x, y) on a refW x refH plane.
func MousePosition(x, y, refW, refH int16) {
	C.LiSendMousePositionEvent(C.short(x), C.short(y), C.short(refW), C.short(refH))
}

func MouseButton(button int, down bool) {
	action := C.char(C.BUTTON_ACTION_RELEASE)
	if down {
		action = C.BUTTON_ACTION_PRESS
	}
	C.LiSendMouseButtonEvent(action, C.int(button))
}

// Scroll sends a high-resolution vertical scroll; 120 is one wheel notch.
func Scroll(amount int16) { C.LiSendHighResScrollEvent(C.short(amount)) }

// HScroll sends a high-resolution horizontal scroll; 120 is one wheel notch.
func HScroll(amount int16) { C.LiSendHighResHScrollEvent(C.short(amount)) }

// Key sends a Windows virtual-key code; Sunshine maps it to the host keyboard.
func Key(vk int16, down bool, mods byte) {
	action := C.char(C.KEY_ACTION_UP)
	if down {
		action = C.KEY_ACTION_DOWN
	}
	C.LiSendKeyboardEvent(C.short(vk), action, C.char(mods))
}

// ToggleHostCursor sends Ctrl+Alt+Shift+N, Sunshine's shortcut for drawing or
// hiding the host cursor in captured frames. Sunshine keeps that flag globally
// until it restarts, so callers must toggle it back when the session ends.
func ToggleHostCursor() {
	Key(VKLControl, true, ModCtrl)
	Key(VKLMenu, true, ModCtrl|ModAlt)
	Key(VKLShift, true, ModCtrl|ModAlt|ModShift)
	Key(VKN, true, ModCtrl|ModAlt|ModShift)
	Key(VKN, false, ModCtrl|ModAlt|ModShift)
	Key(VKLShift, false, ModCtrl|ModAlt)
	Key(VKLMenu, false, ModCtrl)
	Key(VKLControl, false, 0)
}
