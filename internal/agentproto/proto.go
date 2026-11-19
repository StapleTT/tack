// Package agentproto is the JSON protocol between the host agent and the
// bridge, spoken over a WebSocket the agent opens to /agent.
package agentproto

const (
	// Agent to bridge: mouse mode for the focused host window.
	TypeMode = "mode"
	// Both ways: clipboard text. Agent to bridge when the host clipboard
	// changes; bridge to agent when a browser pastes (ID set).
	TypeClipboard = "clipboard"
	// Agent to bridge: the host clipboard now holds the text for ID.
	TypeClipboardAck = "clipboard-ack"
	// Agent to bridge: Sunshine has been running for UptimeMs. Sunshine
	// forgets its cursor-drawing flag when it restarts, so the bridge uses
	// this to tell whether its record of that flag is still valid.
	TypeSunshine = "sunshine"
	// Agent to bridge, on connect: the host cursor theme's images per state
	// (Cursors), for the browser to use as its own cursor. Size is the cursor
	// size in host pixels (XCURSOR_SIZE).
	TypeCursors = "cursors"
	// Agent to bridge: the host cursor is now in State (a Cursors key). Px is
	// its buffer width on a ScreenW-wide screen, so the browser knows its size
	// in the video.
	TypeCursor = "cursor"

	ModeAbsolute = "absolute"
	ModeRelative = "relative"

	// MaxClipboard caps clipboard text in bytes, everywhere.
	MaxClipboard = 1 << 20
)

type Message struct {
	Type string `json:"type"`
	Mode string `json:"mode,omitempty"`
	Text string `json:"text,omitempty"`
	ID   int    `json:"id,omitempty"`

	UptimeMs int64 `json:"uptime_ms,omitempty"`

	Cursors map[string]CursorImage `json:"cursors,omitempty"`
	Size    int                    `json:"size,omitempty"`
	State   string                 `json:"state,omitempty"`
	Px      int                    `json:"px,omitempty"`
	ScreenW int                    `json:"screen_w,omitempty"`
}

// CursorImage is one cursor as a PNG of any resolution, hotspot in its pixels.
type CursorImage struct {
	PNG  []byte `json:"png"`
	HotX int    `json:"hot_x"`
	HotY int    `json:"hot_y"`
}

// MaxCursorPNG caps the PNG bytes of all cursor images together, so they fit
// one data channel message (64 KiB) after base64.
const MaxCursorPNG = 40 << 10
