# Protocol (draft)

JSON messages, `{"type": "...", ...}`. Field names are drafts; this file is the source of truth once code exists.

## Browser ⇄ bridge: signaling WebSocket

| Direction | Type | Purpose |
|---|---|---|
| B → P | `hello` | Client id (stable per browser), decode capabilities (H.264/AV1 support from `MediaCapabilities`/`RTCRtpReceiver.getCapabilities`), screen size |
| P → B | `session` | Chosen preset: resolution, fps, bitrate, codec |
| both | `offer` / `answer` / `ice` | WebRTC negotiation |
| P → B | `kicked` | Another device took over. Client shows "taken over by another device" and stops. |
| P → B | `error` | Human-readable failure (host offline, not paired, etc.) |

Codec is fixed at Moonlight launch, so capabilities must arrive before launch. Changing codec restarts the stream.

## Browser ⇄ bridge: data channels

- `input` (unordered, no retransmit for mouse move; ordered reliable for buttons/keys is TBD by spike): mouse absolute/relative, buttons, wheel, keys, gamepad state.
- `control` (ordered, reliable): `mode` (`absolute` | `relative`), `rumble`, `clipboard`, `stats`.

## Host agent ⇄ bridge: agent WebSocket

Agent connects outbound to `wss://<pi>/agent` with a bearer token.

| Direction | Type | Purpose |
|---|---|---|
| A → P | `mode` | `absolute` or `relative` |
| A → P | `clipboard` | Host clipboard text changed (≤ 1 MB) |
| P → A | `clipboard` | Client pasted; set host clipboard (≤ 1 MB) |

## Session rules

- Newest connection wins; previous client gets `kicked`.
- If the new client's preset differs (resolution, codec), the Moonlight session relaunches.
- On browser drop, the session lives 30 s (configurable). Same client id reconnecting within grace resumes without relaunch. After grace, the Moonlight session tears down; the host app keeps running.
