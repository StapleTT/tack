# Protocol

This file describes what exists. Planned parts are marked.

## Pages and endpoints

| Route | What |
|---|---|
| `GET /` | LAN admin panel, pairing, per-browser preferences and stream launch |
| `GET /stream` | Stream client. Connects on load. Optional `?fps=60&bitrate=20000&res=1280x720` overrides. |
| `GET /api/status` | `{"host","address","online","paired","app","streaming","agent","error"}` for the admin panel. `agent` is true while a host agent is connected. |
| `GET, POST /login`, `POST /logout` | Sign in with the bridge admin password or revoke the browser session. Admin pages and APIs require the secure session cookie. |
| `POST /api/password` | Authenticated form containing `current`, `new` and `confirm`. Success saves the new hash and revokes all browser sessions. |
| `POST /api/pair` | JSON with `address` (LAN IP) and `pin` (four digits). Saves the pinned host after successful pairing. |
| `GET /ca.pem` | Local CA certificate |
| `POST /api/session` | Start a session (below) |

## Browser to bridge: session start (HTTPS)

There is no signaling WebSocket. ICE is non-trickle and the bridge has fixed host candidates, so one request/response is enough.

`POST /api/session`

```json
{ "sdp": "<offer>", "width": 1920, "height": 1080, "fps": 60, "bitrate_kbps": 0 }
```

- The offer has a `recvonly` video transceiver, a `recvonly` audio transceiver, and a data channel labelled `control`.
- `width`/`height` are the client's native pixels (`screen.width * devicePixelRatio`). The bridge rounds them down to even numbers.
- `bitrate_kbps` 0 means preset: 12000 at or below 800 lines, else 25000.
- The offer's codec list doubles as the capability report. The bridge refuses offers without H.264 (AV1: planned).
- The browser adds `stereo=1` to its Opus fmtp so Chrome keeps stereo.

Response `200 {"sdp": "<answer>"}`, sent once the Moonlight session is up. Errors return a non-200 status with a plain-text reason (host offline, not paired, no H.264).

## Media

- Video: H.264 track, stream ID `tack`. Each Moonlight frame becomes one sample. The RTP timestamp advances by the host's real frame spacing.
- Audio: Opus stereo track, stream ID `tack-audio`. The separate stream ID stops the browser from delaying video for lip sync.
- PLI/FIR from the browser triggers `LiRequestIdrFrame` on the host.
- All media uses one UDP port (default 8189).

## Bridge to browser: `control` data channel

| Type | Meaning |
|---|---|
| `{"type":"kicked"}` | Another device took over. The client shows "Taken over by another device." |
| `{"type":"ended","reason":"..."}` | The host ended the stream. |

| `{"type":"mode","mode":"absolute"\|"relative"}` | Switch mouse mode, from the host agent's focus rule. Also sent when the channel opens if the agent has reported a mode. |
| `{"type":"rumble","pad":0,"low":0,"high":0}` | Gamepad rumble, 0–65535 per motor. Lasts until the next rumble message for that pad. |

| `{"type":"clipboard","text":"...","more":bool}` | Host clipboard changed. Arrives in chunks of at most 8192 characters; `more:false` marks the last one. |
| `{"type":"clipboard-ack","id":n}` | The host clipboard now holds the text for paste `id` (or there is no agent). |
| `{"type":"cursors","size":24,"cursors":{"default":{"png":"<base64>","hot_x":x,"hot_y":y},...}}` | The host cursor theme's images for each state (`default`, `pointer`, `text`, `wait`, `progress`; a theme may lack some). The page uses them as its cursor over the video, drawn `size` CSS pixels wide (sharp on HiDPI via `image-set`). Hotspots are in PNG pixels. Sent on connect if the agent provided them. |
| `{"type":"cursor","state":"pointer"}` | The host cursor changed state. States without an image use the browser's cursor of the same name. |

Browser to bridge on the same channel:

| Message | Meaning |
|---|---|
| `{"type":"clipboard","text":"...","more":bool,"id":n}` | Local clipboard for the host, chunked the same way (8192 UTF-16 units, surrogate pairs kept whole). The last chunk's `id` is acked. `id` 0 means a menu upload with no held key. |

Chunks keep every data channel message under pion's default 64 KiB limit, even if JSON escaping turns each character into 6 bytes.

### Paste flow

1. You press Ctrl+V, Ctrl+Shift+V or Shift+Insert. The client holds the V/Insert key (modifiers already went through).
2. The client reads the local clipboard. If it is empty, unchanged since the last sync, over 1 MB, or not readable (no permission), it sends the key right away.
3. Otherwise it uploads the text. The bridge passes it to the agent, which runs `wl-copy` and acks. The bridge relays the ack.
4. On the ack, or after 2 s, the client sends the held key (and its release if you already let go).

Host clipboard changes are written into the browser with `navigator.clipboard.writeText`. If the browser refuses without a user gesture, the client retries on your next key press or click.

## Browser to bridge: `input` data channel

The channel is reliable and ordered: a lost key-up or button-up would leave it stuck on the host. On a LAN, retransmits cost about one round trip, and only when a packet is actually lost. Revisit for the internet phase.

Each message is binary and little-endian. The first byte is the type.

| Type | Body | Meaning |
|---|---|---|
| 1 | x, y, refW, refH: u16 | Absolute mouse position on a refW x refH plane (the video's size) |
| 2 | dx, dy: i16 | Relative mouse movement (pointer lock) |
| 3 | button u8 (1 left, 2 middle, 3 right, 4 X1, 5 X2), down u8 | Mouse button |
| 4 | vk u16 (Windows virtual-key), down u8, mods u8 (1 shift, 2 ctrl, 4 alt, 8 meta) | Key |
| 5 | vertical, horizontal: i16 | Scroll; 120 = one notch, +vertical = up, +horizontal = right |
| 6 | slot u8, mask u16, buttons u32 (Moonlight flags), lt u8, rt u8, lx, ly, rx, ry: i16 | Gamepad state. Stick Y is positive up. Clearing the slot's bit in mask unplugs it. |
| 7 | slot u8, mask u16 | Gamepad arrival (Xbox type, analog triggers, rumble) |
| 8 | (none) | Toggle the host cursor: manual fix if the bridge's record of the cursor state is wrong (Ctrl+Alt+Shift+N or the menu) |
| 9 | visible u8 | Draw the host cursor into the video. The client sends 1 when pointer lock starts and 0 when it ends. |

The bridge drops malformed messages, and drops input from any session that is not the current one.

## Host agent to bridge

The agent connects outbound to `wss://<bridge>/agent` with `Authorization: Bearer <token>`. The token is `data/agent-token` on the bridge (created on first `serve`). It trusts the bridge through `data/tls/ca.pem`. Only one agent at a time: a new connection replaces the old one. Messages are JSON (`internal/agentproto`):

| Direction | Message | Purpose |
|---|---|---|
| A → B | `{"type":"mode","mode":"absolute"\|"relative"}` | Sent on connect and whenever the focused window crosses the rule |
| A → B | `{"type":"clipboard","text":"..."}` | Host clipboard text changed (≤ 1 MB, valid UTF-8) |
| B → A | `{"type":"clipboard","text":"...","id":n}` | A browser pasted: set the host clipboard |
| A → B | `{"type":"clipboard-ack","id":n}` | Host clipboard set (also sent if `wl-copy` failed, so the paste key is never stuck) |
| A → B | `{"type":"cursors","size":n,"cursors":{state:{"png","hot_x","hot_y"}}}` | The host XCursor theme's image per state (largest size in the theme, ≤ 40 KB of PNG in total), sent on connect. The bridge keeps the last set for new browsers |
| A → B | `{"type":"cursor","state":"text"}` | The host cursor changed state. Sent on connect and on each change |
| A → B | `{"type":"sunshine","uptime_ms":n}` | How long Sunshine has run. Sent on connect and when a new Sunshine process appears (polled every 2 s) |

Cursor states: Hyprland does not say which named cursor it shows, and its cursor capture (`ext-image-copy-capture-v1` pointer cursor session) returns transparent pixels for cursors that apps choose by name. It does report every cursor change, along with the cursor's buffer size and hotspot. The agent matches those against the theme: at 32 px, `default` (5,4), `pointer` (12,3) and `text` (14,16) are unique. `wait` and `progress` share their hotspot with static cursors such as move or help, but they animate, so Hyprland reports a new frame every ~166 ms. Two quick repeats count as animation, and an animated candidate that doesn't animate within 600 ms counts as `default`. Unrecognised shapes show as `default`.

The agent ignores clipboard changes that match the text both sides already have, so its own `wl-copy` never echoes back. When the agent disconnects, the bridge forgets its mode.

## Session rules

- One session at a time. A new `POST /api/session` ends the current one (`kicked`) and starts fresh.
- If an app is already running on the host, the bridge resumes it at the new client's resolution. Otherwise it launches the configured app (`-app`, default `Desktop`).
- Ending a session never quits the host app.
- Host cursor: exactly one cursor is visible at all times. While the browser draws its own (absolute mode, or relative mode waiting for a click), the host cursor is kept out of the video. While the pointer is locked, the host cursor is drawn into the video; a game that hides its cursor still hides it. The bridge hides it at session start and keeps it hidden across takeovers. It shows it again when the last session ends or the bridge shuts down. If the host ended the stream, the connection is gone, so the cursor stays hidden until the next session (which expects it hidden).
- Sunshine's flag can only be toggled, never read, so the bridge keeps a record in `data/host-cursor.json` (whether it is hidden, and when it last flipped). The record survives bridge crashes. The flag resets to "drawn" when Sunshine restarts, so when the agent reports a Sunshine uptime shorter than the time since the last flip, the bridge drops a "hidden" record. A toggle that could not be sent never changes the record. If the record is still wrong (Sunshine restarted with no agent connected, or someone pressed the shortcut on the host), press Ctrl+Alt+Shift+N in the stream or use "Toggle host cursor" in the menu. Both flip the flag once without changing what the bridge thinks it should be.
- After six seconds disconnected, the tab requests a new peer connection with its existing `client_id` and `resume_only: true`. The bridge accepts that request for the current owner within the 30-second disconnect grace. An explicit new connection can take over immediately.
