# Architecture

## Targets

| | |
|---|---|
| Host | Sunshine on Arch Linux, Hyprland (Wayland), RTX 5070 Ti, wired gigabit |
| Bridge | Raspberry Pi 5, wired gigabit, same LAN, static IP reservation |
| Client A | ChromeOS laptop, 1366x768 @ 60 fps, H.264 |
| Client B | Windows laptop, 1920x1080 @ 60 fps, H.264 or AV1 |

One client streams at a time. Reliability and low latency come first.

## Components

### Bridge (Go + pion, on the Pi)

Single binary, one systemd service.

- Wraps `moonlight-common-c` through cgo: pairing, RTSP, control stream, input injection.
- Relays encoded video (H.264/AV1) and Opus audio into WebRTC tracks. Packetizes to RTP only; never decodes or transcodes.
- Embeds and serves the frontend (static files) over HTTPS.
- Serves `POST /api/session` for browsers (SDP offer in, answer out) and, later, the agent WebSocket for the host agent.
- Owns session state: who is streaming, takeover, reconnect grace.
- Stores config: admin credentials, the saved host, Sunshine client certificate, presets.

Expected load: 10 to 30 Mbps per stream in and out.

### Frontend (TypeScript)

- **Stream page**: framework-free. Video element, local cursor, pointer lock, keyboard, gamepad, clipboard, stats overlay. Nothing sits between input events and the data channel.
- **Admin panel**: a light framework is acceptable here only. Login, host pairing, presets.

### Host agent (Go, on the gaming PC)

systemd **user** service inside the Hyprland session, because it needs the Wayland session environment.

- Dials out to the Pi over a token-authenticated WebSocket. The Pi never connects into the gaming PC.
- Clipboard sync (text, 1 MB cap). Moonlight has no clipboard channel.
- Mouse-mode signal: reads Hyprland active-window events, matches rules (Steam/Proton, gamescope, user list), reports `absolute` or `relative`.

The detector is swappable: a later Hyprland plugin reporting real pointer-constraint locks emits the same message.

## Data paths

| Path | Transport |
|---|---|
| Page, admin API | HTTPS to Pi |
| Session start (SDP offer/answer) | HTTPS POST to Pi |
| Session control (kicked, ended, later mouse mode) | WebRTC data channel `control` |
| Video, audio | WebRTC (SRTP over UDP), Pi to browser |
| Mouse, keyboard, gamepad, clipboard | WebRTC data channel, browser to Pi, then Moonlight control stream (clipboard via agent) |
| Rumble | Moonlight to Pi, data channel to browser |
| Mouse mode, clipboard | Agent WSS to Pi, data channel to browser |

## TLS

Pointer lock, Keyboard Lock and async clipboard need a secure context, so HTTPS from day one.

- The bridge creates a local CA on first run (`data/tls/ca.pem`). At every start it issues a new server cert covering all of the machine's IPv4 addresses plus `localhost`, so a changed IP never breaks TLS.
- The page links to `/ca.pem` for download.
- Import the CA once per laptop: ChromeOS under Settings > Privacy and security > Manage certificates > Authorities; Windows system store for Chrome/Edge; Firefox separately (own store).

## Repo layout

One Go module (`tack`) at the root, shared by bridge and agent.

```
bridge/cmd/tack-bridge/     bridge binary
bridge/internal/nvhttp/     GameStream HTTP(S): identity, pairing, serverinfo, applist, launch
bridge/internal/moonlight/  cgo wrapper over moonlight-common-c (session, callbacks, input)
bridge/internal/session/    one browser PeerConnection tied to one Moonlight session; takeover
bridge/internal/tlsca/      local CA + per-start server certificate
web/                        TypeScript stream page (tsc only, no bundler), embedded via web/embed.go
  index.html, src/home.ts   homepage (/)
  stream.html               stream client (/stream)
  src/stream.ts             connection, overlay, menu, autoplay/sound
  src/input.ts              mouse modes, keyboard, gamepad, rumble
  src/keymap.ts             KeyboardEvent.code to Windows VK, ChromeOS choices
bridge/internal/session/input.go   input wire format parser and dispatch
web/check.mjs               headless Firefox end-to-end check (WebDriver BiDi, no deps)
third_party/moonlight-common-c/  git submodule, built static into build/mcc by `make mcc`
agent/                      host agent (not started)
deploy/                     systemd units, CA scripts (not started)
spikes/out/                 spike captures (gitignored)
```

moonlight-common-c keeps global state, so the bridge process runs at most one Moonlight session. That matches the one-client-at-a-time rule.

Build the bridge natively on the Pi. Cross-compiling cgo plus OpenSSL is not worth the setup for a single target.
