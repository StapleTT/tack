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

Needs Go, cmake, a C compiler and OpenSSL headers.

```
git submodule update --init --recursive
make bridge            # builds moonlight-common-c statically, then build/tack-bridge
```

## Try it

```
./build/tack-bridge pair -host <sunshine-ip>    # enter the PIN at https://localhost:47990/pin on the host
./build/tack-bridge apps
./build/tack-bridge stream -app Desktop -seconds 10 -out spikes/out/cap.h264
```

State (client identity, paired host) lives in `./data` (`-data` to change).

## Status

Phase 1 (LAN only). Spike 1 (host cursor) passed on stock Sunshine. Spike 2 (bare Moonlight session) passed on x86; still to run on the Pi. Next: WebRTC relay to a bare HTTPS page (roadmap step 2).
