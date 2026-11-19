# Decisions and risks

## Decided

- Pi relays only; no transcoding.
- Bridge in Go with pion; `moonlight-common-c` via cgo.
- Single bridge binary serves frontend, signaling and agent endpoints.
- Stream page framework-free; light framework allowed for admin only.
- Host agent is a systemd user service that dials out to the Pi.
- H.264 baseline, AV1 when the client supports it. Stereo Opus. Fixed bitrate presets (start: 10 to 15 Mbps at 768p, 20 to 30 Mbps at 1080p).
- Absolute mouse by default with native browser cursor; relative + pointer lock when a game is focused.
- No cursor shape matching in v1.
- One client at a time, newest wins, 30 s reconnect grace.
- Single admin account, one saved host (schema allows more).
- HTTPS from day one via local CA.
- Stock Sunshine, no patch: the host cursor is hidden by sending Sunshine's Ctrl+Alt+Shift+N toggle at session start and again at session end (see spike 1).
- No adaptive bitrate in v1 (Moonlight owns encoder settings).

- Bridge encrypts audio and input only, not video (`ENCFLG_AUDIO`), to save Pi CPU. Revisit for the internet phase.

## Open risks

1. **Host cursor toggle state.** Hiding works on stock Sunshine, but the flag is global and lasts until Sunshine restarts. A bridge crash mid-session leaves it hidden. Escape hatch if this bites: small Sunshine patch that resets the flag per session.
2. **CGNAT** unconfirmed. Breaks the forwarded-UDP-port plan for phase 2.
3. **Admin second factor**: TOTP or passkey. Decide before the reverse proxy goes live.
4. **Firefox rumble and clipboard** are browser-limited. Test early.
5. **Controller count** assumed 1 to 2.
6. **Reverse proxy scope**: carries page and signaling only. Media is UDP and bypasses it.
