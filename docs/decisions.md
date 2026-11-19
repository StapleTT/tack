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
- Stock Sunshine, no patch: the host cursor is toggled with Sunshine's Ctrl+Alt+Shift+N shortcut (see spike 1). It is hidden while the browser shows its own cursor and drawn into the video while the pointer is locked, like Parsec. In pointer lock the visible cursor therefore lags by the stream latency; that is inherent.
- No adaptive bitrate in v1 (Moonlight owns encoder settings).

- Browser session start is a single HTTPS POST (non-trickle ICE), not a WebSocket. Server-to-browser messages go over the `control` data channel.
- Audio and video use separate stream IDs so the browser does not hold video back for lip sync.
- All WebRTC media uses one UDP port (default 8189), ready for the single port-forward in phase 2.
- Frontend build is `tsc` only, no bundler, until there is a reason for one.
- Input uses one reliable, ordered data channel with a compact binary format.
- Client hotkeys are Ctrl+Alt+Shift + H (menu), F (fullscreen), M (mouse mode), S (stats), the same family as Moonlight's. The client also swallows Ctrl+Alt+Shift+N and +F1–F12, which are Sunshine's host shortcuts, so they cannot flip the cursor or switch displays.
- Keys are mapped by physical code (`KeyboardEvent.code`) to Windows VK codes; the host's layout decides characters. Autorepeat is left to the host. All held keys and buttons are released on blur or tab hide.
- ChromeOS: the Search key goes to the host as Super. The top-row Back/Forward/Refresh/Fullscreen/Overview keys send F1–F5 (Search+top row already gives real F-keys). Brightness and volume keys stay with the laptop.
- Firefox has no Keyboard Lock, so a "Send to host" menu covers Esc, Super, Alt+Tab, Alt+F4 and Ctrl+Alt+Del. Making the list configurable belongs with the admin panel (step 5).
- Gamepads: browser "standard" layout mapped to an Xbox pad, stable slots (lowest free, kept until disconnect), up to 4. Polled every 4 ms; state is sent only when it changes. Rumble effects are re-issued every 4 s because browsers cap effect duration.
- Relative mode uses `pointerrawupdate` where available (Chrome) and asks for `unadjustedMovement` (raw, unaccelerated deltas), falling back if unsupported.
- Agent link is a WebSocket via `golang.org/x/net/websocket` (already in the module graph through pion), bearer-token auth, newest agent wins.
- Mode rule is one regexp on the Hyprland window class, default `^(steam_app_\d+|gamescope)$`. Proton games show up as `steam_app_<id>`; the Steam client itself (`steam`) stays absolute. The manual toggle still works and lasts until the next focus change.
- Paste holds the V/Insert key until the agent confirms the host clipboard is set (2 s fallback), so the host never pastes stale text.
- Clipboard travels over the existing `control` data channel in 8192-character chunks rather than a separate HTTP endpoint, so it needs no extra auth path.
- Bridge encrypts audio and input only, not video (`ENCFLG_AUDIO`), to save Pi CPU. Revisit for the internet phase.

## Open risks

1. **Host cursor toggle state.** Hiding works on stock Sunshine, but the flag is global, can't be read, and lasts until Sunshine restarts. Mitigated: the bridge saves its record to disk, and the agent reports Sunshine restarts. It can still drift if Sunshine restarts while no agent is connected, or if someone presses the shortcut on the host. Ctrl+Alt+Shift+N in the stream fixes it. Escape hatch if this keeps biting: a small Sunshine patch that resets the flag per session.
2. **CGNAT** unconfirmed. Breaks the forwarded-UDP-port plan for phase 2.
3. **Admin second factor**: TOTP or passkey. Decide before the reverse proxy goes live.
4. **Firefox rumble and clipboard** are browser-limited. Test early.
5. **Controller count** assumed 1 to 2.
7. **H.264 profile negotiation.** Sunshine sends High profile. The bridge offers High (`64001f`) and also accepts constrained baseline (`42e01f`), counting on the browser's decoder to handle High anyway. Headless Firefox on Linux does. Chrome on ChromeOS/Windows and Firefox on Windows are untested.
6. **Reverse proxy scope**: carries page and signaling only. Media is UDP and bypasses it.
