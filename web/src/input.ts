// Mouse, keyboard and gamepad capture. Events become small binary messages on
// the "input" data channel (format in docs/protocol.md).

import { vkFor, VK } from "./keymap.js";

export type MouseMode = "absolute" | "relative";

const T = { abs: 1, rel: 2, button: 3, key: 4, scroll: 5, pad: 6, padArrival: 7, toggleCursor: 8, hostCursor: 9 } as const;
const MOD = { shift: 1, ctrl: 2, alt: 4, meta: 8 } as const;

// Moonlight gamepad flags, indexed by the browser's "standard" button layout.
const PAD_FLAGS: Record<number, number> = {
  0: 0x1000, 1: 0x2000, 2: 0x4000, 3: 0x8000, // A B X Y
  4: 0x0100, 5: 0x0200, // LB RB
  8: 0x0020, 9: 0x0010, // Back Start
  10: 0x0040, 11: 0x0080, // LS RS clicks
  12: 0x0001, 13: 0x0002, 14: 0x0004, 15: 0x0008, // d-pad
  16: 0x0400, // Guide
};

export interface InputUI {
  // Called when relative mode waits for a click or key press to lock the pointer.
  armed(on: boolean): void;
  modeChanged(mode: MouseMode): void;
  // The host pointer moved to (x, y) in video pixels (absolute mode).
  hostPointer(x: number, y: number): void;
  hotkey(key: "menu" | "fullscreen" | "stats" | "mode"): void;
  menuOpen(): boolean;
  sendControl(msg: object): void;
}

const CLIP_MAX = 1 << 20; // bytes, same cap as bridge and agent
const CLIP_CHUNK = 8192; // UTF-16 units per control message (fits pion's 64 KiB limit)

interface PendingPaste { vk: number; mods: number; up: boolean; finish: () => void }

const isPaste = (e: KeyboardEvent) =>
  (e.code === "KeyV" && e.ctrlKey && !e.altKey && !e.metaKey) || (e.code === "Insert" && e.shiftKey && !e.ctrlKey);

export class Input {
  mode: MouseMode = "absolute";
  private keys = new Set<number>();
  private buttons = new Set<number>();
  private pads = new Map<number, { slot: number; last: string }>(); // browser index -> slot
  private rumble = new Map<number, { low: number; high: number; at: number }>();
  private padTimer = 0;
  private remX = 0; private remY = 0; private remWheelY = 0; private remWheelX = 0;
  private off: (() => void)[] = [];
  private lastClip = ""; // text both sides already have
  private pendingWrite: string | null = null; // host text the browser would not accept yet
  private incoming = ""; // host clipboard chunks
  private pasteId = 0;
  private pastes = new Map<number, PendingPaste>();

  constructor(private dc: RTCDataChannel, private video: HTMLVideoElement, private ui: InputUI) {
    const moveEvent = "onpointerrawupdate" in window ? "pointerrawupdate" : "pointermove";
    this.on(video, moveEvent, (e) => this.onMove(e as PointerEvent));
    this.on(video, "mousedown", (e) => this.onButton(e as MouseEvent, true));
    this.on(window, "mouseup", (e) => this.onButton(e as MouseEvent, false));
    this.on(video, "contextmenu", (e) => e.preventDefault());
    this.on(video, "wheel", (e) => this.onWheel(e as WheelEvent), { passive: false });
    this.on(window, "keydown", (e) => this.onKey(e as KeyboardEvent, true), { capture: true });
    this.on(window, "keyup", (e) => this.onKey(e as KeyboardEvent, false), { capture: true });
    this.on(window, "blur", () => this.releaseAll());
    this.on(document, "visibilitychange", () => { if (document.hidden) this.releaseAll(); });
    this.on(document, "pointerlockchange", () => {
      // Pointer lock hides the browser's cursor, so let the host's show in
      // the video instead; hide it again whenever the browser draws one.
      if (this.locked() && this.ui.menuOpen()) {
        // A pending lock request can finish after the menu opens.
        document.exitPointerLock();
        return;
      }
      this.send(new Uint8Array([T.hostCursor, this.locked() ? 1 : 0]));
      this.ui.armed(this.mode === "relative" && !this.locked());
    });
    this.padTimer = window.setInterval(() => this.pollPads(), 4);
  }

  stop() {
    this.releaseAll();
    clearInterval(this.padTimer);
    this.off.forEach((f) => f());
    if (this.locked()) document.exitPointerLock();
    this.ui.armed(false);
  }

  setMode(mode: MouseMode) {
    this.mode = mode;
    if (mode === "absolute" && this.locked()) document.exitPointerLock();
    this.ui.armed(mode === "relative" && !this.locked());
    this.ui.modeChanged(mode);
  }

  toggleHostCursor() { this.send(new Uint8Array([T.toggleCursor])); }

  // Press then release a key combination on the host, e.g. Ctrl+Alt+Del.
  sendCombo(vks: number[]) {
    let mods = 0;
    const modOf = (vk: number) =>
      vk === VK.LShift ? MOD.shift : vk === VK.LControl ? MOD.ctrl : vk === VK.LAlt ? MOD.alt : vk === VK.LWin ? MOD.meta : 0;
    for (const vk of vks) { mods |= modOf(vk); this.sendKey(vk, true, mods); }
    for (const vk of [...vks].reverse()) { mods &= ~modOf(vk); this.sendKey(vk, false, mods); }
  }

  // Clipboard messages from the bridge (host copied something, or the host
  // now holds the text for a held paste keystroke).
  onControl(msg: { type: string; text?: string; more?: boolean; id?: number }) {
    if (msg.type === "clipboard") {
      this.incoming += msg.text ?? "";
      if (msg.more) return;
      const text = this.incoming;
      this.incoming = "";
      this.lastClip = text;
      this.writeClipboard(text);
    } else if (msg.type === "clipboard-ack" && msg.id) {
      this.pastes.get(msg.id)?.finish();
    }
  }

  // Send the local clipboard to the host unless it already has it. Resolves
  // false when nothing was sent (no permission, empty, unchanged, too big).
  async uploadClipboard(id = 0): Promise<boolean> {
    let text: string;
    try { text = await navigator.clipboard.readText(); } catch { return false; }
    if (!text || text === this.lastClip || new TextEncoder().encode(text).length > CLIP_MAX) return false;
    this.lastClip = text;
    for (let i = 0; i < text.length;) {
      let end = Math.min(i + CLIP_CHUNK, text.length);
      const c = text.charCodeAt(end - 1);
      if (end < text.length && c >= 0xd800 && c <= 0xdbff) end--; // keep surrogate pairs whole
      this.ui.sendControl({ type: "clipboard", text: text.slice(i, end), more: end < text.length, id });
      i = end;
    }
    return true;
  }

  private writeClipboard(text: string) {
    // Browsers may refuse without a recent user gesture; retry on the next one.
    navigator.clipboard.writeText(text).then(() => { this.pendingWrite = null; }, () => { this.pendingWrite = text; });
  }

  private flushClipboard() {
    if (this.pendingWrite !== null) this.writeClipboard(this.pendingWrite);
  }

  // Hold the paste key until the host clipboard has our text, so the host
  // pastes the new content rather than the old.
  private paste(vk: number, mods: number) {
    const id = ++this.pasteId;
    let done = false;
    const p: PendingPaste = {
      vk, mods, up: false,
      finish: () => {
        if (done) return;
        done = true;
        clearTimeout(timer);
        this.pastes.delete(id);
        this.sendKey(vk, true, mods);
        if (p.up) { this.keys.delete(vk); this.sendKey(vk, false, mods); }
      },
    };
    this.pastes.set(id, p);
    const timer = setTimeout(p.finish, 2000);
    this.uploadClipboard(id).then((sent) => { if (!sent) p.finish(); });
  }

  playRumble(slot: number, low: number, high: number) {
    this.rumble.set(slot, { low, high, at: 0 });
    this.applyRumble(slot);
  }

  private on(t: EventTarget, type: string, f: (e: Event) => void, opts?: AddEventListenerOptions) {
    t.addEventListener(type, f, opts);
    this.off.push(() => t.removeEventListener(type, f, opts));
  }

  private locked() { return document.pointerLockElement === this.video; }

  private send(buf: Uint8Array) {
    if (this.dc.readyState === "open") this.dc.send(buf as Uint8Array<ArrayBuffer>);
  }

  private msg(type: number, size: number, fill: (v: DataView) => void) {
    const b = new Uint8Array(size);
    b[0] = type;
    fill(new DataView(b.buffer));
    this.send(b);
  }

  private sendKey(vk: number, down: boolean, mods: number) {
    this.msg(T.key, 5, (v) => { v.setUint16(1, vk, true); v.setUint8(3, down ? 1 : 0); v.setUint8(4, mods); });
  }

  private sendButton(button: number, down: boolean) {
    this.msg(T.button, 3, (v) => { v.setUint8(1, button); v.setUint8(2, down ? 1 : 0); });
  }

  private requestLock() {
    // unadjustedMovement gives raw, unaccelerated deltas where supported (Chrome).
    const v = this.video as HTMLVideoElement & { requestPointerLock(o?: object): Promise<void> | void };
    try {
      const p = v.requestPointerLock({ unadjustedMovement: true });
      if (p && "catch" in p) p.catch(() => v.requestPointerLock());
    } catch {
      v.requestPointerLock();
    }
  }

  private onMove(e: PointerEvent) {
    if (this.ui.menuOpen()) return;
    if (this.mode === "relative") {
      if (!this.locked()) return;
      // Keep sub-pixel remainders so slow movement is not lost.
      const x = e.movementX + this.remX, y = e.movementY + this.remY;
      const dx = Math.trunc(x), dy = Math.trunc(y);
      this.remX = x - dx; this.remY = y - dy;
      if (dx || dy) this.msg(T.rel, 5, (v) => { v.setInt16(1, clamp16(dx), true); v.setInt16(3, clamp16(dy), true); });
      return;
    }
    // Absolute: map into the letterboxed video picture.
    const vw = this.video.videoWidth, vh = this.video.videoHeight;
    if (!vw || !vh) return;
    const r = this.video.getBoundingClientRect();
    const scale = Math.min(r.width / vw, r.height / vh);
    const ox = r.left + (r.width - vw * scale) / 2, oy = r.top + (r.height - vh * scale) / 2;
    const x = Math.min(vw - 1, Math.max(0, Math.round((e.clientX - ox) / scale)));
    const y = Math.min(vh - 1, Math.max(0, Math.round((e.clientY - oy) / scale)));
    this.msg(T.abs, 9, (v) => {
      v.setUint16(1, x, true); v.setUint16(3, y, true); v.setUint16(5, vw, true); v.setUint16(7, vh, true);
    });
    this.ui.hostPointer(x, y);
  }

  private onButton(e: MouseEvent, down: boolean) {
    const button = [1, 2, 3, 4, 5][e.button]; // left middle right x1 x2
    if (!button) return;
    if (down) {
      if (this.ui.menuOpen()) return;
      this.flushClipboard();
      e.preventDefault(); // no middle-click autoscroll, no text selection
      if (this.mode === "relative" && !this.locked()) { this.requestLock(); return; }
      this.buttons.add(button);
      this.sendButton(button, true);
    } else if (this.buttons.delete(button)) {
      this.sendButton(button, false);
    }
  }

  private onWheel(e: WheelEvent) {
    e.preventDefault();
    if (this.ui.menuOpen()) return;
    // 120 = one notch. Pixel deltas are ~100 per notch, line deltas ~3.
    const k = e.deltaMode === 0 ? 1.2 : e.deltaMode === 1 ? 40 : 120;
    const y = -e.deltaY * k + this.remWheelY, x = e.deltaX * k + this.remWheelX;
    const sy = Math.trunc(y), sx = Math.trunc(x);
    this.remWheelY = y - sy; this.remWheelX = x - sx;
    if (sy || sx) this.msg(T.scroll, 5, (v) => { v.setInt16(1, clamp16(sy), true); v.setInt16(3, clamp16(sx), true); });
  }

  private onKey(e: KeyboardEvent, down: boolean) {
    if (e.ctrlKey && e.altKey && e.shiftKey && down) {
      const hk = ({ KeyH: "menu", KeyF: "fullscreen", KeyS: "stats", KeyM: "mode" } as const)[e.code as "KeyH"];
      // Swallow our hotkeys and Sunshine's own (F1-F12 switch displays) so
      // the host never sees them. N is Sunshine's cursor toggle; the bridge
      // owns that flag, so route it to the bridge's manual fix.
      if (hk || e.code === "KeyN" || /^F([1-9]|1[0-2])$/.test(e.code)) {
        e.preventDefault();
        e.stopPropagation();
        if (hk && !e.repeat) this.ui.hotkey(hk);
        if (e.code === "KeyN" && !e.repeat) this.toggleHostCursor();
        return;
      }
    }
    if (this.ui.menuOpen()) {
      if (!down) this.releaseKey(e); // never leave a key held on the host
      return;
    }
    const vk = vkFor(e.code);
    if (vk === undefined) return; // leave unmapped keys (volume etc.) to the browser
    e.preventDefault();
    e.stopPropagation();
    if (down) {
      // Let the user finish a menu shortcut before a modifier can capture the pointer.
      if (this.mode === "relative" && !this.locked() && !/^(Control|Alt|Shift|Meta)(Left|Right)$/.test(e.code)) this.requestLock();
      if (e.repeat) return; // the host does its own key repeat
      this.flushClipboard();
      this.keys.add(vk);
      if (isPaste(e)) { this.paste(vk, mods(e)); return; }
      this.sendKey(vk, true, mods(e));
    } else {
      this.releaseKey(e);
    }
  }

  private releaseKey(e: KeyboardEvent) {
    const vk = vkFor(e.code);
    for (const p of this.pastes.values()) if (p.vk === vk) { p.up = true; return; } // sent with the held paste
    if (vk !== undefined && this.keys.delete(vk)) this.sendKey(vk, false, mods(e));
  }

  private releaseAll() {
    const held = new Set<number>();
    for (const p of this.pastes.values()) { p.up = true; held.add(p.vk); } // released when the paste goes out
    for (const vk of this.keys) if (!held.has(vk)) this.sendKey(vk, false, 0);
    for (const b of this.buttons) this.sendButton(b, false);
    this.keys.clear();
    this.buttons.clear();
  }

  private padMask() {
    let m = 0;
    for (const { slot } of this.pads.values()) m |= 1 << slot;
    return m;
  }

  private pollPads() {
    const seen = new Set<number>();
    for (const gp of navigator.getGamepads()) {
      if (!gp || !gp.connected) continue;
      seen.add(gp.index);
      let pad = this.pads.get(gp.index);
      if (!pad) {
        // Stable slots: lowest free one, kept until this pad disconnects.
        const used = new Set([...this.pads.values()].map((p) => p.slot));
        const slot = [0, 1, 2, 3].find((s) => !used.has(s));
        if (slot === undefined) continue;
        pad = { slot, last: "" };
        this.pads.set(gp.index, pad);
        const mask = this.padMask();
        this.msg(T.padArrival, 4, (v) => { v.setUint8(1, slot); v.setUint16(2, mask, true); });
      }
      let flags = 0;
      gp.buttons.forEach((b, i) => { if (b.pressed && PAD_FLAGS[i]) flags |= PAD_FLAGS[i]; });
      const trig = (i: number) => Math.round((gp.buttons[i]?.value ?? 0) * 255);
      const axis = (i: number, sign = 1) => clamp16(Math.round(sign * (gp.axes[i] ?? 0) * 32767));
      const state = [flags, trig(6), trig(7), axis(0), axis(1, -1), axis(2), axis(3, -1)];
      const key = state.join();
      if (key === pad.last) continue;
      pad.last = key;
      this.sendPad(pad.slot, this.padMask(), state);
    }
    for (const [index, pad] of this.pads) {
      if (seen.has(index)) continue;
      this.pads.delete(index);
      this.rumble.delete(pad.slot);
      this.sendPad(pad.slot, this.padMask(), [0, 0, 0, 0, 0, 0, 0]); // slot bit cleared = unplugged
    }
    // Browsers cap effect duration, but host rumble lasts until changed.
    const now = performance.now();
    for (const [slot, r] of this.rumble) if ((r.low || r.high) && now - r.at > 4000) this.applyRumble(slot);
  }

  private sendPad(slot: number, mask: number, s: number[]) {
    this.msg(T.pad, 18, (v) => {
      v.setUint8(1, slot); v.setUint16(2, mask, true); v.setUint32(4, s[0], true);
      v.setUint8(8, s[1]); v.setUint8(9, s[2]);
      v.setInt16(10, s[3], true); v.setInt16(12, s[4], true); v.setInt16(14, s[5], true); v.setInt16(16, s[6], true);
    });
  }

  private applyRumble(slot: number) {
    const r = this.rumble.get(slot);
    const index = [...this.pads].find(([, p]) => p.slot === slot)?.[0];
    if (!r || index === undefined) return;
    const act = navigator.getGamepads()[index]?.vibrationActuator;
    if (!act) return; // Firefox: best effort only
    r.at = performance.now();
    if (!r.low && !r.high) { act.reset?.(); return; }
    act.playEffect("dual-rumble", { duration: 5000, strongMagnitude: r.low / 65535, weakMagnitude: r.high / 65535 }).catch(() => {});
  }
}

function mods(e: KeyboardEvent) {
  return (e.shiftKey ? MOD.shift : 0) | (e.ctrlKey ? MOD.ctrl : 0) | (e.altKey ? MOD.alt : 0) | (e.metaKey ? MOD.meta : 0);
}

function clamp16(n: number) {
  return Math.max(-32768, Math.min(32767, n));
}
