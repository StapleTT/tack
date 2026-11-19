# Roadmap

Order puts make-or-break unknowns first.

## Phase 1: LAN

1. **Spikes**: see [spikes.md](spikes.md).
   Exit: host cursor hidden from video (or patch sketched); bare `moonlight-common-c` session receiving frames on the Pi.
2. **Bridge**: pairing, session launch, video and audio over WebRTC to a bare HTTPS page.
   Exit: both laptops play the host desktop at native resolution, 60 fps, with sound.
3. **Input**: absolute/relative mouse, pointer lock with armed state, Keyboard Lock (Chrome), Firefox "send to host" keys, ChromeOS Search/function-row choices, gamepad (1 to 2 pads, Xbox-style, stable slots) and rumble.
   Exit: desktop use and a pointer-locked game both feel local.
4. **Host agent**: clipboard sync and Hyprland-based mouse-mode signal, manual toggle.
   Exit: focusing a listed game window arms pointer lock; copy/paste works both ways.
5. **Admin panel**: login, saved host, PIN pairing, per-client presets, takeover and grace logic, stats overlay (RTT, loss, decode time, dropped frames).
6. **Hardening**: systemd units, reconnect testing (wifi blip, refresh, lid close), Firefox and ChromeOS checks.

## Phase 2: internet

7. CGNAT check first. Then reverse proxy for page and signaling, forwarded UDP port for media, admin second factor.

## Later, not v1

Adaptive bitrate, plugin-based pointer-lock detection, real cursor shapes, image clipboard, multiple hosts, microphone, surround audio.
