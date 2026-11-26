// Stream page. Deliberately framework-free: nothing sits between the browser
// and the media/input paths.

import { Input, MouseMode } from "./input.js";
import { VK } from "./keymap.js";
import { HostCursor } from "./hostcursor.js";
import { WsStream } from "./wsstream.js";

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const video = $<HTMLVideoElement>("video");
const overlay = $<HTMLDivElement>("overlay");
const message = $<HTMLParagraphElement>("message");
const connectBtn = $<HTMLButtonElement>("connect");
const statsEl = $<HTMLDivElement>("stats");
const banner = $<HTMLDivElement>("banner");
const menu = $<HTMLDivElement>("menu");
const soundBtn = $<HTMLButtonElement>("sound");
const detail = $<HTMLParagraphElement>("detail");
const hint = $<HTMLDivElement>("hint");
const toast = $<HTMLDivElement>("toast");

// Optional overrides: /stream?fps=60&bitrate=20000&res=1280x720
const params = new URLSearchParams(location.search);
for (const key of ["res", "fps", "bitrate"]) {
  if (!params.has(key)) params.set(key, localStorage.getItem("tack." + key) ?? "");
}
for (const button of menu.querySelectorAll<HTMLButtonElement>("button[data-act]")) {
  button.hidden = localStorage.getItem("tack.key." + button.dataset.act) === "off";
}
const [resW, resH] = (params.get("res") ?? "").split("x").map(Number);

// Transport: WebRTC (UDP, lowest latency) unless it can't connect, e.g.
// through a reverse proxy; then the WebSocket transport, remembered per origin.
// ?transport=ws or ?transport=webrtc forces one and saves the choice.
const forced = params.get("transport");
if (forced === "ws") localStorage.setItem("tack.transport", "ws");
if (forced === "webrtc") localStorage.removeItem("tack.transport");
const useWs = () => localStorage.getItem("tack.transport") === "ws" || typeof RTCPeerConnection === "undefined";
const PROBE_MS = 4000; // WebRTC gets this long to connect before falling back

let pc: RTCPeerConnection | null = null;
let ws: WsStream | null = null;
let input: Input | null = null;
let control: RTCDataChannel | null = null;
// Handles for web/check.mjs.
const debug: Record<string, unknown> = ((window as unknown as { __tack: object }).__tack = {});
const clientID = sessionStorage.getItem("tack.client") || crypto.randomUUID();
sessionStorage.setItem("tack.client", clientID);
let reconnectTimer = 0;
let reconnectDeadline = 0;
let statsTimer = 0;
let showStats = false;
let flashTimer = 0;

fetch("/api/status").then((r) => r.json()).then((st) => { $("host-name").textContent = st.host || "Host"; }).catch(() => {});

// Briefly show a floating hint (menu shortcut, pointer locked).
function flash(el: HTMLElement, ms: number) {
  hint.hidden = toast.hidden = true;
  el.hidden = false;
  clearTimeout(flashTimer);
  flashTimer = window.setTimeout(() => { el.hidden = true; }, ms);
}

function show(text: string, button = "Connect", body = "") {
  message.textContent = text;
  detail.textContent = body;
  connectBtn.textContent = button;
  connectBtn.disabled = false;
  connectBtn.hidden = false;
  overlay.hidden = false;
}

function stop() {
  clearTimeout(reconnectTimer);
  clearInterval(statsTimer);
  hostCursor.cancel();
  input?.stop();
  input = null;
  pc?.close();
  pc = null;
  ws?.close();
  ws = null;
  menu.hidden = true;
  statsEl.hidden = true;
  hint.hidden = toast.hidden = true;
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
  if (ws) { void ws.audio.resume(); return; }
  if (!video.muted) return;
  video.muted = false;
  soundBtn.hidden = true;
}
soundBtn.addEventListener("click", unmute);
window.addEventListener("keydown", unmute, { capture: true });

const hostCursor = new HostCursor(video, () => input?.toggleHostCursor());
debug.hostCursor = hostCursor;

const ui = {
  armed: (on: boolean) => { banner.hidden = !on || !menu.hidden; },
  modeChanged: (mode: MouseMode) => { showMode(mode); hostCursor.cancel(); },
  hostPointer: (x: number, y: number) => hostCursor.moved(x, y),
  menuOpen: () => !menu.hidden,
  hotkey: (key: "menu" | "fullscreen" | "stats" | "mode") => act(key),
  sendControl: (msg: object) => {
    if (ws) ws.sendText(msg);
    else if (control?.readyState === "open") control.send(JSON.stringify(msg));
  },
};

function showMode(mode: MouseMode) {
  $("mode").textContent = mode;
  for (const m of ["absolute", "relative"]) menu.querySelector(`[data-act=${m}]`)!.setAttribute("aria-pressed", String(m === mode));
}
showMode("absolute");

function showKeyboard() {
  const locked = !!document.fullscreenElement && !!(navigator as Navigator & { keyboard?: object }).keyboard;
  $("kb-status").textContent = !document.fullscreenElement ? "Go fullscreen to capture all keys" : locked ? "Locked · all keys sent" : "Fullscreen · no keyboard lock";
  $("fs-btn").textContent = document.fullscreenElement ? "Exit fullscreen" : "Fullscreen + keyboard lock";
}
showKeyboard();
document.addEventListener("fullscreenchange", showKeyboard);
document.addEventListener("pointerlockchange", () => { if (document.pointerLockElement) flash(toast, 2200); });

function act(name: string) {
  switch (name) {
    case "menu":
      menu.hidden = !menu.hidden;
      if (!menu.hidden && document.pointerLockElement) document.exitPointerLock();
      ui.armed(input?.mode === "relative" && !document.pointerLockElement);
      if (!menu.hidden) {
        hostCursor.cancel();
        hint.hidden = true;
      }
      return;
    case "fullscreen": return toggleFullscreen();
    case "stats":
      showStats = !showStats;
      statsEl.hidden = !showStats;
      $("stats-toggle").textContent = showStats ? "Hide corner stats" : "Show corner stats";
      return;
    case "mode": input?.setMode(input.mode === "absolute" ? "relative" : "absolute"); return;
    case "absolute": case "relative": input?.setMode(name); return;
    case "esc": return input?.sendCombo([VK.Escape]);
    case "win": return input?.sendCombo([VK.LWin]);
    case "alttab": return input?.sendCombo([VK.LAlt, VK.Tab]);
    case "altf4": return input?.sendCombo([VK.LAlt, VK.F4]);
    case "cad": return input?.sendCombo([VK.LControl, VK.LAlt, VK.Delete]);
    case "cursor": hostCursor.cancel(); return input?.toggleHostCursor();
    case "paste": {
      const b = $("paste-btn");
      void input?.uploadClipboard().then((sent) => {
        b.textContent = sent ? "Sent to host ✓" : "Nothing new to send";
        setTimeout(() => { b.textContent = "Send my clipboard to host"; }, 1800);
      });
      return;
    }
    case "disconnect": stop(); show("Disconnected", "Connect"); return;
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

// Control messages from the bridge, the same on both transports.
function onControl(msg: any) {
  if (msg.type === "kicked") { stop(); show("Taken over by another device", "Take it back", "Another device started a stream, so this one ended. Taking it back relaunches the session at this device's resolution."); }
  else if (msg.type === "ended") { stop(); show("Session ended", "Reconnect", `The stream ended: ${msg.reason}.`); }
  else if (msg.type === "mode") input?.setMode(msg.mode);
  else if (msg.type === "rumble") input?.playRumble(msg.pad, msg.low, msg.high);
  else if (msg.type === "cursors") void hostCursor.load(msg.cursors, msg.size);
  else if (msg.type === "cursor") hostCursor.show(msg.state, msg.px, msg.screen_w);
  else if (msg.type === "clipboard" || msg.type === "clipboard-ack") input?.onControl(msg);
}

function sessionRequest(resumeOnly: boolean, sdp = "") {
  const dpr = window.devicePixelRatio || 1;
  return {
    client_id: clientID, resume_only: resumeOnly, sdp,
    width: resW || Math.round(screen.width * dpr),
    height: resH || Math.round(screen.height * dpr),
    fps: Number(params.get("fps")) || 60,
    bitrate_kbps: Number(params.get("bitrate")) || 0,
  };
}

function connected() {
  reconnectDeadline = 0;
  overlay.hidden = true;
  if (menu.hidden) flash(hint, 5000);
}

// connectWs resolves once the host stream is up (or the bridge refused it)
// and rejects if the socket drops first.
function connectWs(resumeOnly: boolean): Promise<void> {
  const w = new WsStream(sessionRequest(resumeOnly));
  ws = w;
  debug.ws = w;
  video.srcObject = w.stream;
  video.muted = true; // audio plays through the WebSocket transport's AudioContext
  void video.play().catch(() => {});
  const syncSound = () => { if (w === ws) soundBtn.hidden = w.audio.state === "running"; };
  w.audio.onstatechange = syncSound;
  void w.audio.resume();
  syncSound();
  w.onControl = (msg) => { if (w === ws) onControl(msg); };
  return new Promise((resolve, reject) => {
    w.onStarted = () => {
      if (w !== ws) return;
      debug.input = input = new Input(w, video, ui);
      lastFrames = 0;
      statsTimer = setInterval(() => updateWsStats(w), 1000);
      connected();
      resolve();
    };
    w.onError = (_status, text) => {
      if (w !== ws) return resolve();
      stop();
      show("Could not start", "Retry", text);
      resolve();
    };
    w.onClose = (started) => {
      if (w !== ws) return;
      if (!started) return reject(new Error("the bridge closed the stream connection"));
      // Same window as a dropped WebRTC connection: the bridge holds the session for 30 s.
      reconnectDeadline = Date.now() + 30_000;
      start(true);
    };
  });
}

async function connect(resumeOnly = false) {
  stop();
  connectBtn.disabled = true;
  message.textContent = "Connecting…";
  detail.textContent = "";
  if (useWs()) return connectWs(resumeOnly);

  const conn = new RTCPeerConnection();
  pc = conn;
  conn.addTransceiver("video", { direction: "recvonly" });
  conn.addTransceiver("audio", { direction: "recvonly" });
  const ctl = conn.createDataChannel("control");
  control = ctl;
  // Reliable and ordered: a lost key-up or button-up would leave it stuck.
  const inputDc = conn.createDataChannel("input");
  inputDc.binaryType = "arraybuffer";
  inputDc.onopen = () => { if (conn === pc) debug.input = input = new Input(inputDc, video, ui); };
  Object.assign(debug, { pc: conn, inputDc, control: ctl });

  const stream = new MediaStream();
  video.srcObject = stream;
  conn.ontrack = (e) => {
    lowLatency(e.receiver);
    stream.addTrack(e.track);
    if (e.track.kind === "audio") play();
  };

  ctl.onmessage = (e) => { if (conn === pc) onControl(JSON.parse(e.data)); };

  // A fresh session that never connects means UDP can't reach the bridge
  // (typically a reverse proxy). A reconnect that fails is just the network.
  let wasConnected = false;
  const fallBack = () => {
    console.info("tack: WebRTC did not connect, switching to the WebSocket transport");
    localStorage.setItem("tack.transport", "ws");
    start();
  };
  conn.onconnectionstatechange = () => {
    if (conn !== pc) return;
    clearTimeout(reconnectTimer);
    if (conn.connectionState === "disconnected") {
      reconnectDeadline = Date.now() + 30_000;
      reconnectTimer = window.setTimeout(() => start(true), 6000);
    }
    if (conn.connectionState === "connected") { wasConnected = true; connected(); }
    if (conn.connectionState === "failed") {
      if (!wasConnected && !resumeOnly) fallBack();
      else if (Date.now() < reconnectDeadline) start(true);
      else { stop(); show("Connection lost", "Reconnect", "The bridge no longer holds the session. Reconnecting starts a new one."); }
    }
  };

  const offer = await conn.createOffer();
  await conn.setLocalDescription({ type: "offer", sdp: preferStereo(offer.sdp ?? "") });
  await waitForIce(conn, 500);

  const res = await fetch("/api/session", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(sessionRequest(resumeOnly, conn.localDescription?.sdp)),
  });
  if (!res.ok) {
    stop();
    show("Could not start", "Retry", (await res.text()).trim());
    return;
  }
  const { sdp } = await res.json();
  await conn.setRemoteDescription({ type: "answer", sdp });
  lastFrames = lastDecodeTime = 0;
  statsTimer = setInterval(() => updateStats(conn), 1000);
  // On a LAN ICE connects in well under a second; don't wait for "failed".
  setTimeout(() => { if (conn === pc && !wasConnected && !resumeOnly) fallBack(); }, PROBE_MS);
}

// Receiver counters start over for each peer connection.
let lastFrames = 0, lastDecodeTime = 0;
async function updateStats(conn: RTCPeerConnection) {
  const rows: [string, string][] = [];
  let sub = "";
  (await conn.getStats()).forEach((s) => {
    if (s.type === "inbound-rtp" && s.kind === "video") {
      const frames = s.framesDecoded ?? 0;
      const decode = s.totalDecodeTime ?? 0;
      const n = frames - lastFrames;
      const decodeMs = n > 0 ? ((decode - lastDecodeTime) / n) * 1000 : 0;
      lastFrames = frames; lastDecodeTime = decode;
      sub = `${s.frameWidth}×${s.frameHeight} · ${n} fps`;
      rows.push(["Frame rate", `${n} fps`], ["Decode", `${decodeMs.toFixed(1)} ms`], ["Lost", String(s.packetsLost)], ["Dropped", String(s.framesDropped ?? 0)]);
    }
    if (s.type === "candidate-pair" && s.nominated && s.currentRoundTripTime !== undefined) {
      rows.unshift(["RTT", `${(s.currentRoundTripTime * 1000).toFixed(1)} ms`]);
    }
  });
  renderStats(rows, sub);
}

function renderStats(rows: [string, string][], sub: string) {
  const span = (text: string, tag = "span") => Object.assign(document.createElement(tag), { textContent: text });
  statsEl.replaceChildren(...rows.flatMap(([k, v]) => [span(k), span(v)]));
  $("grid").replaceChildren(...rows.map(([k, v]) => { const d = span("", "div"); d.append(span(k), span(v, "b")); return d; }));
  $("menu-sub").textContent = sub;
  statsEl.hidden = !showStats;
}

function updateWsStats(w: WsStream) {
  const n = w.framesDecoded - lastFrames;
  lastFrames = w.framesDecoded;
  const v = video;
  renderStats([["Transport", "WebSocket"], ["Frame rate", `${n} fps`], ["Decode queue", String(w.decodeQueue)], ["Dropped", String(w.framesDropped)]],
    v.videoWidth ? `${v.videoWidth}×${v.videoHeight} · ${n} fps` : "");
}

function start(resumeOnly = false) {
  if (!resumeOnly) reconnectDeadline = 0;
  connect(resumeOnly).catch((err) => {
    stop();
    // A WebRTC API error means this browser won't do WebRTC here (disabled,
    // blocked by policy or an extension): use the WebSocket transport.
    if (!useWs() && err instanceof DOMException) {
      console.info("tack: WebRTC unavailable, switching to the WebSocket transport:", err);
      localStorage.setItem("tack.transport", "ws");
      return start(resumeOnly);
    }
    if (resumeOnly && Date.now() + 1500 < reconnectDeadline) {
      show("Connection lost", "", `Reconnecting. The session is held for ${Math.round((reconnectDeadline - Date.now()) / 1000)} s.`);
      connectBtn.hidden = true;
      reconnectTimer = window.setTimeout(() => start(true), 1500);
    } else show("Connection failed", "Retry", String(err));
  });
}
connectBtn.addEventListener("click", () => start());
start(); // the click that opened this page is the "one click"
