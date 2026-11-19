# Spikes

Throwaway code lives in `spikes/`. Record results here.

## 1. Hide host cursor (make-or-break)

Question: can stock Sunshine on Hyprland stream without the host cursor composited into the video?

Check:
- Which capture backend Sunshine uses here (wlroots screencopy, KMS, NvFBC/portal).
- Whether screencopy's `overlay_cursor` is exposed through Sunshine config.
- Whether Sunshine's KMS path draws the cursor itself, and whether that can be disabled.
- Hyprland-side options to hide the cursor from capture.

If it fails: write the smallest Sunshine patch adding a "do not draw cursor" option, kept as a patch file rebased on upstream. Check Sunshine's license before distributing builds.

Result (2026-11-18): **passes on stock Sunshine, no patch needed.**

- Host runs Sunshine 2026.914 (`appversion` 7.1.431) with `capture = wlr` (wlroots screencopy) on Hyprland 0.57.1.
- In Sunshine's source, `src/platform/linux/wlgrab.cpp` passes the global `display_cursor` flag as screencopy's `overlay_cursor` on every capture request. `src/input.cpp` flips that flag when a client sends Ctrl+Alt+Shift+N.
- Verified live with `tack-bridge stream -cursor-spike`: cursor visible at frame 150, absent at frame 300 (after a hide toggle and a cursor move), visible again at frame 500 (after the second toggle).

Consequences:

- `display_cursor` is global and lasts until Sunshine restarts. It is not reset per session. The bridge must hide the cursor at session start and show it again at session end. If the bridge crashes mid-session, the cursor stays hidden for every client until the next toggle or a Sunshine restart.
- The shortcut only applies to wlr capture as configured here. Switching Sunshine to KMS or portal capture would need this spike again.

## 2. Bare moonlight-common-c session on the Pi

Question: can a minimal Go + cgo program on the Pi pair with Sunshine, launch a session, and receive encoded video and audio frames?

Check: build on aarch64, PIN pairing, RTSP setup, frame callbacks deliver H.264 NAL units and Opus packets, per-frame timing.

Result (2026-11-18): **passes on x86 (gaming PC streaming to itself); Pi run still to do.**

- Go pairing implementation (`bridge/internal/nvhttp`) paired with Sunshine using the PIN flow; the host cert is pinned for HTTPS.
- 10 s Desktop stream at 1920x1080@60, H.264 High, 20 Mbps target: 598 frames (59.6 fps), 15.1 Mbps measured, 1802 Opus packets (48 kHz stereo, 5 ms frames), connected in 24 ms, first frame after 83 ms, control RTT 1 ms.
- Sunshine's web UI only accepts PIN entry from `https://localhost:47990` by default (CSRF origin check). Entering it from the LAN IP fails unless `csrf_allowed_origins` is set.

Remaining: build and run the same command natively on the Pi 5 (`make bridge` needs Go, cmake, gcc, OpenSSL headers) and repeat the measurement over the wire.
