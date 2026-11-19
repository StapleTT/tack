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
}
