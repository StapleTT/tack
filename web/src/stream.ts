// Stream page. Deliberately framework-free: nothing sits between the browser
// and the media/input paths.

import { Input, MouseMode } from "./input.js";
import { VK } from "./keymap.js";

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const video = $<HTMLVideoElement>("video");
const overlay = $<HTMLDivElement>("overlay");
const message = $<HTMLParagraphElement>("message");
const connectBtn = $<HTMLButtonElement>("connect");
const statsEl = $<HTMLDivElement>("stats");
const banner = $<HTMLDivElement>("banner");
const menu = $<HTMLDivElement>("menu");
const soundBtn = $<HTMLButtonElement>("sound");

// Optional overrides: /stream?fps=60&bitrate=20000&res=1280x720
const params = new URLSearchParams(location.search);
const [resW, resH] = (params.get("res") ?? "").split("x").map(Number);

let pc: RTCPeerConnection | null = null;
let input: Input | null = null;
let statsTimer = 0;
let showStats = false;

function show(text: string, button = "Connect") {
  message.textContent = text;
  connectBtn.textContent = button;
  connectBtn.disabled = false;
  connectBtn.hidden = false;
  overlay.hidden = false;
}

function stop() {
  clearInterval(statsTimer);
  input?.stop();
  input = null;
  pc?.close();
  pc = null;
  menu.hidden = true;
  statsEl.hidden = true;
  soundBtn.hidden = true;
}

// Autoplay with sound needs a user gesture on this page. Without one, start
// muted and offer a button (clicking the video itself would click the host).
function play() {
  video.muted = false;
  video.play().catch(() => {
    video.muted = true;
    video.play().catch(() => {});
    soundBtn.hidden = false;
  });
}
function unmute() {
  if (!video.muted) return;
  video.muted = false;
  soundBtn.hidden = true;
}
soundBtn.addEventListener("click", unmute);
window.addEventListener("keydown", unmute, { capture: true });

const ui = {
  armed: (on: boolean) => { banner.hidden = !on || !menu.hidden; },
  modeChanged: (mode: MouseMode) => { $("mode").textContent = mode; },
  menuOpen: () => !menu.hidden,
  hotkey: (key: "menu" | "fullscreen" | "stats" | "mode") => act(key),
};

function act(name: string) {
  switch (name) {
    case "menu":
      menu.hidden = !menu.hidden;
      if (!menu.hidden && document.pointerLockElement) document.exitPointerLock();
      ui.armed(input?.mode === "relative" && !document.pointerLockElement);
      return;
    case "fullscreen": return toggleFullscreen();
    case "stats": showStats = !showStats; statsEl.hidden = !showStats; return;
    case "mode": input?.setMode(input.mode === "absolute" ? "relative" : "absolute"); return;
    case "esc": return input?.sendCombo([VK.Escape]);
    case "win": return input?.sendCombo([VK.LWin]);
    case "alttab": return input?.sendCombo([VK.LAlt, VK.Tab]);
    case "altf4": return input?.sendCombo([VK.LAlt, VK.F4]);
    case "cad": return input?.sendCombo([VK.LControl, VK.LAlt, VK.Delete]);
    case "cursor": return input?.toggleHostCursor();
    case "disconnect": stop(); show("Disconnected.", "Connect"); return;
  }
}

menu.addEventListener("click", (e) => {
  const b = (e.target as HTMLElement).closest("button");
  if (b?.dataset.act) act(b.dataset.act);
});

// Fullscreen plus Keyboard Lock (Chrome/ChromeOS) captures Esc, Alt+Tab and
// OS keys. Without Keyboard Lock (Firefox) use the menu's "Send to host".
async function toggleFullscreen() {
  if (document.fullscreenElement) return document.exitFullscreen();
  await document.documentElement.requestFullscreen({ navigationUI: "hide" });
  const kb = (navigator as Navigator & { keyboard?: { lock?: () => Promise<void> } }).keyboard;
  await kb?.lock?.().catch(() => {});
}

// Ask the receiver to render frames as soon as they decode.
function lowLatency(receiver: RTCRtpReceiver) {
  const r = receiver as RTCRtpReceiver & { jitterBufferTarget?: number; playoutDelayHint?: number };
  if ("jitterBufferTarget" in r) r.jitterBufferTarget = 0;
  if ("playoutDelayHint" in r) r.playoutDelayHint = 0;
}

// Chrome decodes Opus to mono unless the receiver's SDP asks for stereo.
function preferStereo(sdp: string): string {
  return sdp.replace(/(a=fmtp:\d+ [^\r\n]*useinbandfec=1)/g, "$1;stereo=1");
}

function waitForIce(conn: RTCPeerConnection, ms: number): Promise<void> {
  return new Promise((resolve) => {
    if (conn.iceGatheringState === "complete") return resolve();
    const t = setTimeout(resolve, ms);
    conn.addEventListener("icegatheringstatechange", () => {
      if (conn.iceGatheringState === "complete") { clearTimeout(t); resolve(); }
    });
  });
}

async function connect() {
  stop();
  connectBtn.disabled = true;
  message.textContent = "Connecting…";

  const conn = new RTCPeerConnection();
  pc = conn;
  (window as unknown as { __tackPc: RTCPeerConnection }).__tackPc = conn; // for web/check.mjs
  conn.addTransceiver("video", { direction: "recvonly" });
  conn.addTransceiver("audio", { direction: "recvonly" });
  const control = conn.createDataChannel("control");
  // Reliable and ordered: a lost key-up or button-up would leave it stuck.
  const inputDc = conn.createDataChannel("input");
  inputDc.binaryType = "arraybuffer";
  inputDc.onopen = () => { if (conn === pc) input = new Input(inputDc, video, ui); };

  const stream = new MediaStream();
  video.srcObject = stream;
  conn.ontrack = (e) => {
    lowLatency(e.receiver);
    stream.addTrack(e.track);
    if (e.track.kind === "audio") play();
  };

  control.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    if (msg.type === "kicked") { stop(); show("Taken over by another device.", "Take back"); }
    else if (msg.type === "ended") { stop(); show(`Stream ended: ${msg.reason}`, "Reconnect"); }
    else if (msg.type === "mode") input?.setMode(msg.mode);
    else if (msg.type === "rumble") input?.playRumble(msg.pad, msg.low, msg.high);
  };

  conn.onconnectionstatechange = () => {
    if (conn !== pc) return;
    if (conn.connectionState === "connected") overlay.hidden = true;
    if (conn.connectionState === "failed") { stop(); show("Connection lost.", "Reconnect"); }
  };

  const offer = await conn.createOffer();
  await conn.setLocalDescription({ type: "offer", sdp: preferStereo(offer.sdp ?? "") });
  await waitForIce(conn, 500);

  const dpr = window.devicePixelRatio || 1;
  const res = await fetch("/api/session", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      sdp: conn.localDescription?.sdp,
      width: resW || Math.round(screen.width * dpr),
      height: resH || Math.round(screen.height * dpr),
      fps: Number(params.get("fps")) || 60,
      bitrate_kbps: Number(params.get("bitrate")) || 0,
    }),
  });
  if (!res.ok) {
    stop();
    show(`Could not start: ${(await res.text()).trim()}`, "Retry");
    return;
  }
  const { sdp } = await res.json();
  await conn.setRemoteDescription({ type: "answer", sdp });
  statsTimer = setInterval(() => updateStats(conn), 1000);
}

// Minimal debug line until the real stats overlay lands (roadmap step 5).
let lastFrames = 0, lastDecodeTime = 0;
async function updateStats(conn: RTCPeerConnection) {
  let line = "";
  (await conn.getStats()).forEach((s) => {
    if (s.type === "inbound-rtp" && s.kind === "video") {
      const frames = s.framesDecoded ?? 0;
      const decode = s.totalDecodeTime ?? 0;
      const n = frames - lastFrames;
      const decodeMs = n > 0 ? ((decode - lastDecodeTime) / n) * 1000 : 0;
      lastFrames = frames; lastDecodeTime = decode;
      line += `${s.frameWidth}x${s.frameHeight} ${n} fps  decode ${decodeMs.toFixed(1)} ms  lost ${s.packetsLost}  dropped ${s.framesDropped ?? 0}\n`;
    }
    if (s.type === "candidate-pair" && s.nominated && s.currentRoundTripTime !== undefined) {
      line += `rtt ${(s.currentRoundTripTime * 1000).toFixed(1)} ms\n`;
    }
  });
  statsEl.textContent = line.trim();
  statsEl.hidden = !showStats;
}

const start = () => { connect().catch((err) => { stop(); show(`Error: ${err}`, "Retry"); }); };
connectBtn.addEventListener("click", start);
start(); // the click that opened this page is the "one click"
