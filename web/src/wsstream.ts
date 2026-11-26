// WebSocket transport: the whole session over one WebSocket on the page's own
// origin, so it works through any HTTP reverse proxy. The browser decodes
// the host's H.264 and Opus itself with WebCodecs. Wire format in
// docs/protocol.md.
//
// Video frames are drawn to a canvas whose captureStream() feeds the page's
// <video>, so input mapping, the host cursor check, fullscreen and pointer
// lock work exactly as with WebRTC.
// ponytail: the canvas hop costs about one frame; Chrome's
// MediaStreamTrackGenerator avoids it, but Firefox has no equivalent yet.

const VIDEO = 1, AUDIO = 2;
const MAX_DECODE_QUEUE = 2; // frames waiting in the decoder before we skip to a keyframe
const AUDIO_LEAD = 0.02, AUDIO_MAX_AHEAD = 0.1; // seconds

type ControlMessage = { type: string; [k: string]: unknown };

export class WsStream {
  readonly stream: MediaStream;
  readonly audio = new AudioContext({ latencyHint: "interactive", sampleRate: 48000 });
  framesDecoded = 0;
  framesDropped = 0;
  onStarted = () => {};
  onError = (_status: number, _text: string) => {};
  onControl = (_msg: ControlMessage) => {};
  onClose = (_started: boolean) => {};

  private ws: WebSocket;
  private started = false;
  private closed = false;
  private canvas = document.createElement("canvas");
  private g = this.canvas.getContext("2d", { alpha: false })!;
  private decoder?: VideoDecoder;
  private codec = "";
  private waitKey = true;
  private lastKeyRequest = 0;
  private adec: AudioDecoder;
  private audioTs = 0;
  private nextAudio = 0;

  constructor(request: object) {
    this.stream = this.canvas.captureStream();
    this.adec = new AudioDecoder({ output: (d) => this.playAudio(d), error: (e) => console.warn("tack: audio decoder", e) });
    this.adec.configure({ codec: "opus", sampleRate: 48000, numberOfChannels: 2 });

    const ws = (this.ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/api/stream`));
    ws.binaryType = "arraybuffer";
    ws.onopen = () => ws.send(JSON.stringify(request));
    ws.onmessage = (e) => {
      if (typeof e.data !== "string") return this.media(new Uint8Array(e.data as ArrayBuffer));
      const msg = JSON.parse(e.data) as ControlMessage;
      if (msg.type === "started") { this.started = true; this.onStarted(); }
      else if (msg.type === "error") { this.closed = true; this.onError(Number(msg.status), String(msg.text)); }
      else this.onControl(msg);
    };
    ws.onclose = () => {
      if (!this.closed) { this.closed = true; this.onClose(this.started); }
      this.shutdown();
    };
  }

  // Input sends binary messages; the same shape as an RTCDataChannel.
  get readyState() { return this.ws.readyState === WebSocket.OPEN ? "open" : "closed"; }
  send(b: Uint8Array<ArrayBuffer>) { this.ws.send(b); }
  sendText(msg: object) { if (this.ws.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg)); }
  get decodeQueue() { return this.decoder?.decodeQueueSize ?? 0; }

  close() {
    this.closed = true;
    this.ws.close();
    this.shutdown();
  }

  private shutdown() {
    if (this.decoder?.state !== "closed") this.decoder?.close();
    if (this.adec.state !== "closed") this.adec.close();
    if (this.audio.state !== "closed") void this.audio.close();
    for (const t of this.stream.getTracks()) t.stop();
  }

  private media(b: Uint8Array) {
    if (b[0] === AUDIO) {
      if (this.adec.state !== "configured") return;
      this.adec.decode(new EncodedAudioChunk({ type: "key", timestamp: this.audioTs, data: b.subarray(1) }));
      this.audioTs += 5000; // µs; only needs to increase
      return;
    }
    if (b[0] !== VIDEO || b.length < 10) return;
    const key = (b[1] & 1) === 1;
    const pts = Number(new DataView(b.buffer, b.byteOffset).getBigUint64(2));
    const data = b.subarray(10);
    if (key) {
      const codec = avcCodec(data);
      if (codec && (codec !== this.codec || !this.decoder || this.decoder.state === "closed")) this.configure(codec);
    }
    const d = this.decoder;
    if (!d || d.state !== "configured" || (this.waitKey && !key)) { this.framesDropped++; return; }
    if (d.decodeQueueSize > MAX_DECODE_QUEUE) { this.skipToKeyframe(); this.framesDropped++; return; }
    this.waitKey = false;
    d.decode(new EncodedVideoChunk({ type: key ? "key" : "delta", timestamp: pts, data }));
  }

  private configure(codec: string) {
    if (this.decoder && this.decoder.state !== "closed") this.decoder.close();
    this.codec = codec;
    this.waitKey = true;
    const d = (this.decoder = new VideoDecoder({
      output: (f) => this.draw(f),
      error: (e) => {
        console.warn("tack: video decoder", e);
        if (this.decoder === d) this.decoder = undefined;
        this.skipToKeyframe();
      },
    }));
    // No description: the decoder reads Annex B, which is what Moonlight delivers.
    d.configure({ codec, optimizeForLatency: true });
  }

  // Drop frames until the next keyframe, and ask the host for one now.
  private skipToKeyframe() {
    this.waitKey = true;
    const now = performance.now();
    if (now - this.lastKeyRequest > 500) {
      this.lastKeyRequest = now;
      this.sendText({ type: "keyframe" });
    }
  }

  private draw(f: VideoFrame) {
    if (this.canvas.width !== f.displayWidth || this.canvas.height !== f.displayHeight) {
      this.canvas.width = f.displayWidth;
      this.canvas.height = f.displayHeight;
    }
    this.g.drawImage(f, 0, 0);
    f.close();
    this.framesDecoded++;
  }

  // Play each 5 ms packet as soon as possible; skip ahead if the queue grows
  // (the context was suspended, or the network delivered a burst).
  private playAudio(d: AudioData) {
    const ctx = this.audio;
    if (ctx.state !== "running") { d.close(); return; }
    const buf = ctx.createBuffer(d.numberOfChannels, d.numberOfFrames, d.sampleRate);
    for (let c = 0; c < d.numberOfChannels; c++) d.copyTo(buf.getChannelData(c), { planeIndex: c, format: "f32-planar" });
    d.close();
    const now = ctx.currentTime;
    if (this.nextAudio - now > AUDIO_MAX_AHEAD) return;
    const at = Math.max(this.nextAudio, now + AUDIO_LEAD);
    const src = ctx.createBufferSource();
    src.buffer = buf;
    src.connect(ctx.destination);
    src.start(at);
    this.nextAudio = at + buf.duration;
  }
}

// avcCodec builds the WebCodecs codec string (avc1.PPCCLL) from the SPS in an
// Annex B frame, or returns "" if the frame has none.
export function avcCodec(b: Uint8Array): string {
  for (let i = 0; i + 4 < b.length; i++) {
    if (b[i] === 0 && b[i + 1] === 0 && b[i + 2] === 1 && (b[i + 3] & 0x1f) === 7) {
      if (i + 6 >= b.length) return "";
      return "avc1." + [b[i + 4], b[i + 5], b[i + 6]].map((x) => x.toString(16).padStart(2, "0")).join("");
    }
  }
  return "";
}
