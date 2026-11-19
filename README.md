# tack

A Parsec-style browser client for Sunshine. Open a link in Chrome or Firefox, click once, and be streaming with a mouse that never feels laggy.

A Go bridge on a Raspberry Pi 5 wraps `moonlight-common-c`, speaks Moonlight to Sunshine on the gaming PC, and re-serves the encoded stream to the browser over WebRTC. The Pi never transcodes.

```
Browser (Chrome/Firefox) ──HTTPS + WSS + WebRTC──▶ Pi 5 bridge ──Moonlight──▶ Sunshine (Arch/Hyprland)
                                                        ▲
                                                        └──WSS (outbound)── host agent (Hyprland session)
```

## Docs

- [Architecture](docs/architecture.md): components, responsibilities, repo layout
- [Protocol](docs/protocol.md): browser and host-agent messages
- [Roadmap](docs/roadmap.md): build order, phases, exit criteria
- [Spikes](docs/spikes.md): make-or-break unknowns to settle first
- [Decisions and risks](docs/decisions.md): what is decided, what is open

Source plan: `Plan.pdf` (2026-11-18).

## Build

Needs Go, Node (for `tsc`), cmake, a C compiler and OpenSSL headers.

```
git submodule update --init --recursive
make bridge            # moonlight-common-c (static) + web (tsc) + build/tack-bridge
make agent             # build/tack-agent (pure Go)
make test
```

## Try it

```
systemd-ask-password "Initial tack admin password" | ./build/tack-bridge passwd   # first install
./build/tack-bridge serve                       # homepage at https://<bridge-ip>:8443/, stream at /stream
node web/check.mjs https://localhost:8443/stream 8   # headless Firefox end-to-end check (add --mouse on the host)
```

Sign in with the admin password. Change it in the panel under **Admin password**. A change signs out all browser sessions. The bridge stores a salted bcrypt hash in `data/admin-password`. Upgrading converts an existing plaintext credential at startup and preserves the password. For local recovery, run the `passwd` command above with the bridge stopped, then start it again. In the panel, enter the gaming PC’s LAN IP, choose **Pair and save host**, and type the displayed PIN into Sunshine at `https://localhost:47990/pin` on that PC. Existing CLI pairings load automatically. Resolution, frame rate, bitrate and Send to host menu choices are saved in each browser. For the headless check, set `TACK_ADMIN_PASSWORD`.

First visit per laptop: download the CA from the page link (`/ca.pem`) and import it as a trusted authority. On ChromeOS: Settings > Privacy and security > Security > Manage certificates > Authorities. On Windows: certmgr, Trusted Root Certification Authorities. Firefox needs its own import: Settings > Privacy & Security > Certificates.

Host agent (on the gaming PC, inside Hyprland): copy the bridge's `data/agent-token` and `data/tls/ca.pem` to `~/.config/tack/`, then run `tack-agent -bridge https://<bridge-ip>:8443`, or install `deploy/tack-agent.service` (instructions inside). It syncs the clipboard and switches the browser to pointer lock when a game window (`steam_app_*`, `gamescope`) is focused. Change the rule with `-relative-class`. It also sends cursors from your XCursor theme (`XCURSOR_THEME`/`XCURSOR_SIZE`, or `-cursor-theme`/`-cursor-size`) and follows the host cursor's state (arrow, hand, text, wait, progress), so the browser cursor matches the host.

While streaming, Ctrl+Alt+Shift + H opens the menu, F toggles fullscreen (with Keyboard Lock on Chrome), M switches mouse mode (absolute/relative), and S shows stats.

Spike tools: `tack-bridge apps`, `tack-bridge stream -seconds 10 -out spikes/out/cap.h264 [-cursor-spike]`.

State (client identity, paired host) lives in `./data` (`-data` to change).

## Status

Phase 1 (LAN only). Spike 1 (host cursor) passed on stock Sunshine. Spike 2 (bare Moonlight session) passed on x86; still to run on the Pi. Step 2 (WebRTC relay to a bare HTTPS page) built and checked in headless Firefox; awaiting a real-laptop test. Step 3 (input) built; absolute mouse verified, the rest awaits real hardware. Step 4 (host agent) built and checked. The LAN admin panel is built. Next: real-device checks and reconnect hardening.
