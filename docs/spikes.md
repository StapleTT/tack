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

## 3. Host cursor image for the browser cursor

Question: can the agent read the host's current cursor image so the browser can show it (CSS `cursor: url(...)`) instead of its own arrow?

Check: `spikes/cursor-image/cursor.c` opens an `ext-image-copy-capture-v1` pointer cursor session on the output and saves every frame.

Result (2026-11-19, Hyprland 0.57.1, NVIDIA, 10-bit output `XBGR2101010`): **partial.**

- Shape-change events work: a new frame and a new hotspot arrive whenever the cursor changes, e.g. (14,16) over text and (5,4) for the arrow. Buffers are 32x32 ARGB8888.
- The pixels come back fully transparent. With `cursor:no_hardware_cursors 1` the session never sent a frame at all.
- Not tried: the dmabuf path, 8-bit output, and a cursor theme or size other than the current one.

Cause (from Hyprland's `CursorshareSession.cpp`): it only draws cursors that come from a client's own surface. Cursors that apps choose by name (cursor-shape protocol, used by most toolkits) are cleared to transparent. Hyprland also sends the hotspot event after the frame it belongs to.

Used instead: the cursor session's change events plus hotspots, matched against the XCursor theme, with animation telling apart the loading cursors (see protocol.md, "Cursor states"). Verified live through the stream: arrow over empty space, hand over a button, I-beam over a text box. The animated states are only covered by unit tests.

Earlier fallback idea, not needed: on each shape change, grab a small `zwlr_screencopy` region around the pointer with and without the cursor overlaid, and take the cursor from the pixels that differ. Sunshine uses the same screencopy path, which works here. Soft shadow edges would come out approximate.
