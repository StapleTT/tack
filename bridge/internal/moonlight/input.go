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
func Key(vk int16, down bool, mods byte) bool {
	action := C.char(C.KEY_ACTION_UP)
	if down {
		action = C.KEY_ACTION_DOWN
	}
	return C.LiSendKeyboardEvent(C.short(vk), action, C.char(mods)) == 0
}

// ToggleHostCursor sends Ctrl+Alt+Shift+N, Sunshine's shortcut for drawing or
// hiding the host cursor in captured frames. Sunshine keeps that flag globally
// until it restarts, so callers must toggle it back when the session ends.
// It reports whether the N press was sent (false without a connection).
//
// Sunshine only takes the shortcut on a fresh N press while exactly
// Ctrl+Alt+Shift are flagged as held, and its key state outlives sessions.
// So release N and the modifiers first (a release of a key that is not held
// is ignored): a held or stuck key can then no longer swallow the toggle.
func ToggleHostCursor() bool {
	Key(VKN, false, 0)
	Key(VKLShift, false, 0)
	Key(VKLMenu, false, 0)
	Key(VKLControl, false, 0)
	Key(VKLControl, true, ModCtrl)
	Key(VKLMenu, true, ModCtrl|ModAlt)
	Key(VKLShift, true, ModCtrl|ModAlt|ModShift)
	ok := Key(VKN, true, ModCtrl|ModAlt|ModShift)
	Key(VKN, false, ModCtrl|ModAlt|ModShift)
	Key(VKLShift, false, ModCtrl|ModAlt)
	Key(VKLMenu, false, ModCtrl)
	Key(VKLControl, false, 0)
	return ok
}

// Controller sends the full state of gamepad slot (0-3). mask has a bit set
// for every connected slot; clearing a slot's bit unplugs it on the host.
// Buttons use Moonlight's flags (A_FLAG etc.); stick Y is positive up.
func Controller(slot uint8, mask uint16, buttons uint32, lt, rt uint8, lx, ly, rx, ry int16) {
	C.LiSendMultiControllerEvent(C.uint8_t(slot), C.uint16_t(mask), C.int(buttons),
		C.uchar(lt), C.uchar(rt), C.short(lx), C.short(ly), C.short(rx), C.short(ry))
}

// ControllerArrival announces an Xbox-style pad with analog triggers and
// rumble in slot.
func ControllerArrival(slot uint8, mask uint16) {
	const allButtons = C.A_FLAG | C.B_FLAG | C.X_FLAG | C.Y_FLAG | C.UP_FLAG | C.DOWN_FLAG | C.LEFT_FLAG |
		C.RIGHT_FLAG | C.LB_FLAG | C.RB_FLAG | C.PLAY_FLAG | C.BACK_FLAG | C.LS_CLK_FLAG | C.RS_CLK_FLAG | C.SPECIAL_FLAG
	C.LiSendControllerArrivalEvent(C.uint8_t(slot), C.uint16_t(mask), C.LI_CTYPE_XBOX,
		C.uint32_t(allButtons), C.LI_CCAP_ANALOG_TRIGGERS|C.LI_CCAP_RUMBLE)
}
