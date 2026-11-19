// The host's cursor, as seen by the page.
//
// 1. Looks: the agent sends the host theme's images once ("cursors"), then
//    the state under the host pointer ("cursor": default, pointer, text, wait,
//    progress). Each state becomes a CSS cursor over the video, at the host's
//    cursor size in CSS pixels and sharp on HiDPI screens. States without an
//    image use the browser's cursor of the same name.
//
// 2. Self-check: in absolute mode the host cursor must stay out of the video
//    (the browser draws its own). Sunshine's draw-cursor flag can only be
//    toggled, never read, so the bridge's record of it can drift. When the
//    pointer rests, look at the video under the host pointer; if the host
//    cursor is clearly there at two different spots, flip the flag back.

interface CursorImage { img: HTMLImageElement; hotX: number; hotY: number }

const REST_MS = 400; // pointer still this long (plus stream latency) before looking
// Brightness correlation between the frame and the cursor image over the
// cursor's opaque pixels. Measured on real frames: a drawn cursor scores
// ~0.8, random spots on busy screens never above ~0.62.
const MATCH_MIN = 0.72;
const SLACK = 2; // px of position search either way (Sunshine's scaling rounds)
const SPOT_DIST = 40; // video px between the two sightings
const COOLDOWN_MS = 5000;

export class HostCursor {
  private images = new Map<string, CursorImage>();
  private css = new Map<string, string[]>(); // state -> CSS values, best last
  private state = "default";
  private px = 0; // host cursor buffer width on a screenW-wide screen
  private screenW = 0;
  private timer = 0;
  private sighting?: { x: number; y: number; t: number };
  private quietUntil = 0;
  lastCheck?: object; // for debugging: what the last look measured

  constructor(private video: HTMLVideoElement, private fixHostCursor: () => void) {}

  async load(images: Record<string, { png: string; hot_x: number; hot_y: number }>, size: number) {
    const dpr = Math.min(4, window.devicePixelRatio || 1);
    for (const [state, c] of Object.entries(images)) {
      const img = new Image();
      img.src = `data:image/png;base64,${c.png}`;
      try { await img.decode(); } catch { continue; }
      this.images.set(state, { img, hotX: c.hot_x, hotY: c.hot_y });
      const k = size / img.width; // image pixels to CSS pixels
      const hot = `${Math.round(c.hot_x * k)} ${Math.round(c.hot_y * k)}`;
      const css = [`url(${scaled(img, size).toDataURL()}) ${hot}, ${state}`];
      if (dpr > 1) css.push(`image-set(url(${scaled(img, Math.round(size * dpr)).toDataURL()}) ${dpr}x) ${hot}, ${state}`);
      this.css.set(state, css);
    }
    this.show(this.state, this.px, this.screenW);
  }

  show(state: string, px = 0, screenW = 0) {
    this.state = state;
    this.px = px;
    this.screenW = screenW;
    const v = this.video;
    v.style.cursor = "";
    // Browsers ignore a value they can't parse, so the last one that works wins.
    for (const css of this.css.get(state) ?? this.css.get("default") ?? []) v.style.cursor = css;
  }

  // The host pointer is now at (x, y) in video pixels (absolute mode).
  moved(x: number, y: number) {
    clearTimeout(this.timer);
    this.timer = window.setTimeout(() => this.check(x, y), REST_MS);
  }

  // Stop looking (mode change, menu, stream end).
  cancel() {
    clearTimeout(this.timer);
    this.sighting = undefined;
  }

  private check(x: number, y: number) {
    const now = performance.now();
    if (now < this.quietUntil || !this.seen(x, y)) return;
    const s = this.sighting;
    if (!s || now - s.t > 10_000 || Math.hypot(x - s.x, y - s.y) < SPOT_DIST) {
      if (!s || now - s.t > 10_000) this.sighting = { x, y, t: now };
      return;
    }
    console.info("tack: host cursor visible in absolute mode, flipping it back");
    this.sighting = undefined;
    this.quietUntil = now + COOLDOWN_MS;
    this.fixHostCursor();
  }

  // seen reports whether the current host cursor is drawn at (x, y) in the
  // video: the frame's brightness there follows the cursor image's pattern
  // (dark fill, light outline), allowing for a pixel or two of rounding.
  private seen(x: number, y: number): boolean {
    const c = this.images.get(this.state);
    const v = this.video;
    if (!c || !this.px || !this.screenW || !v.videoWidth || v.paused) return false;
    const size = this.px * v.videoWidth / this.screenW; // cursor width in video px
    if (size < 8) return false;
    // Grab the area around the expected spot once.
    const pad = SLACK + 2;
    const ax = Math.round(x - c.hotX * size / c.img.width) - pad, ay = Math.round(y - c.hotY * size / c.img.width) - pad;
    const aw = Math.ceil(size) + 2 * pad + 1;
    const g = canvas(aw, aw);
    g.drawImage(v, ax, ay, aw, aw, 0, 0, aw, aw);
    const area = luma(g.getImageData(0, 0, aw, aw).data);
    let best = -1;
    for (let w = Math.round(size) - 1; w <= Math.round(size) + 1; w++) {
      const h = Math.round(w * c.img.height / c.img.width);
      const ref = scaled(c.img, w, h).getContext("2d")!.getImageData(0, 0, w, h).data;
      const ox = pad + Math.round(size * c.hotX / c.img.width) - Math.round(w * c.hotX / c.img.width);
      const oy = pad + Math.round(size * c.hotY / c.img.width) - Math.round(w * c.hotY / c.img.width);
      for (let dy = -SLACK; dy <= SLACK; dy++) {
        for (let dx = -SLACK; dx <= SLACK; dx++) {
          best = Math.max(best, correlation(area, aw, ox + dx, oy + dy, ref, w, h));
        }
      }
    }
    this.lastCheck = { x, y, size: Math.round(size * 10) / 10, score: Math.round(best * 100) / 100 };
    return best >= MATCH_MIN;
  }
}

function luma(d: Uint8ClampedArray) {
  const out = new Float32Array(d.length / 4);
  for (let i = 0; i < out.length; i++) out[i] = 0.299 * d[i * 4] + 0.587 * d[i * 4 + 1] + 0.114 * d[i * 4 + 2];
  return out;
}

// correlation is the Pearson correlation between the area's brightness at
// (ox, oy) and the cursor image's, over the image's opaque pixels.
function correlation(area: Float32Array, aw: number, ox: number, oy: number, ref: Uint8ClampedArray, w: number, h: number) {
  let n = 0, sa = 0, sb = 0, saa = 0, sbb = 0, sab = 0;
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const i = (y * w + x) * 4;
      const ax = ox + x, ay = oy + y;
      if (ref[i + 3] < 200 || ax < 0 || ay < 0 || ax >= aw || ay >= aw) continue;
      const a = area[ay * aw + ax], b = 0.299 * ref[i] + 0.587 * ref[i + 1] + 0.114 * ref[i + 2];
      n++; sa += a; sb += b; saa += a * a; sbb += b * b; sab += a * b;
    }
  }
  const va = saa - sa * sa / n, vb = sbb - sb * sb / n;
  return n < 10 || va < n || vb < n ? -1 : (sab - sa * sb / n) / Math.sqrt(va * vb);
}

function canvas(w: number, h: number) {
  const c = document.createElement("canvas");
  c.width = w;
  c.height = h;
  return c.getContext("2d", { willReadFrequently: true })!;
}

// scaled draws img into a w-wide canvas (height keeps the aspect ratio).
function scaled(img: HTMLImageElement, w: number, h = Math.round(img.height * w / img.width)) {
  const g = canvas(w, h);
  g.imageSmoothingQuality = "high";
  g.drawImage(img, 0, 0, w, h);
  return g.canvas;
}
