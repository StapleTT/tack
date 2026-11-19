# Protocol

This file describes what exists. Planned parts are marked.

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

`GET /ca.pem` returns the local CA certificate for import.

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

| `{"type":"mode","mode":"absolute"\|"relative"}` | Switch mouse mode. Nothing sends this until the host agent lands (step 4); the client already handles it. |
| `{"type":"rumble","pad":0,"low":0,"high":0}` | Gamepad rumble, 0–65535 per motor. Lasts until the next rumble message for that pad. |

Planned: `clipboard`.

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
| 8 | (none) | Toggle the host cursor: manual fix if the bridge's idea of the cursor state is wrong |

The bridge drops malformed messages, and drops input from any session that is not the current one.

## Host agent to bridge (planned, step 4)

The agent connects outbound to `wss://<pi>/agent` with a bearer token.

| Direction | Type | Purpose |
|---|---|---|
| A → P | `mode` | `absolute` or `relative` |
| A → P | `clipboard` | Host clipboard text changed (≤ 1 MB) |
| P → A | `clipboard` | Client pasted; set host clipboard (≤ 1 MB) |

## Session rules

- One session at a time. A new `POST /api/session` ends the current one (`kicked`) and starts fresh.
- If an app is already running on the host, the bridge resumes it at the new client's resolution. Otherwise it launches the configured app (`-app`, default `Desktop`).
- Ending a session never quits the host app.
- Host cursor: the bridge hides it at the first session start and keeps it hidden across takeovers. It shows it again when the last session ends or the bridge shuts down. If the host ended the stream, the connection is gone, so the cursor stays hidden until the next session (which expects it hidden). If state ever drifts, the menu has a "Toggle host cursor" fix.
- Browser gone: for now pion's ICE timeouts end the session about 30 s after the browser disappears (5 s to disconnected, 25 s more to failed). Same-client resume inside that window is planned for step 5.
