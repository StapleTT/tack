// End-to-end check: headless Firefox opens /stream (which connects on load) and
// reports WebRTC receive stats after a few seconds of streaming.
//
//   node web/check.mjs [url] [seconds] [--mouse] [--screenshot=file.png] [--host-cursor=1|0]
//
// --mouse also moves the browser pointer and compares the real host cursor
// (hyprctl cursorpos), so it only works with the bridge talking to this host.
//
// Needs a running `tack-bridge serve` and Firefox on PATH. Uses WebDriver BiDi
// over Node's built-in WebSocket, so there are no dependencies.
import { spawn, execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const args = process.argv.slice(2).filter((a) => !a.startsWith("--"));
const mouseTest = process.argv.includes("--mouse");
const shotPath = process.argv.find((a) => a.startsWith("--screenshot="))?.slice(13);
// --host-cursor=1|0 sends the "pointer locked" message (draw host cursor) before the screenshot.
const hostCursor = process.argv.find((a) => a.startsWith("--host-cursor="))?.slice(14);
const url = args[0] ?? "https://localhost:8443/stream";
const seconds = Number(args[1] ?? 8);
const port = 9300 + (process.pid % 500); // unique per run so checks can overlap

const profile = mkdtempSync(join(tmpdir(), "tack-ff-"));
writeFileSync(join(profile, "user.js"), [
  'user_pref("media.autoplay.default", 0);',
  'user_pref("media.autoplay.blocking_policy", 0);',
  'user_pref("remote.prefs.recommended", true);',
].join("\n"));
const ff = spawn("firefox", ["--headless", "--no-remote", "--profile", profile, `--remote-debugging-port=${port}`], { stdio: "ignore" });
const cleanup = () => { ff.kill(); rmSync(profile, { recursive: true, force: true }); };
process.on("exit", cleanup);

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let ws;
for (let i = 0; i < 50 && !ws; i++) {
  await sleep(200);
  try {
    ws = await new Promise((resolve, reject) => {
      const s = new WebSocket(`ws://127.0.0.1:${port}/session`);
      s.onopen = () => resolve(s);
      s.onerror = reject;
    });
  } catch {}
}
if (!ws) throw new Error("could not reach Firefox remote agent");

let nextId = 1;
const pending = new Map();
ws.onmessage = (e) => {
  const msg = JSON.parse(e.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    msg.type === "error" ? reject(new Error(`${msg.error}: ${msg.message}`)) : resolve(msg.result);
  }
};
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const id = nextId++;
  pending.set(id, { resolve, reject });
  ws.send(JSON.stringify({ id, method, params }));
});

await send("session.new", { capabilities: { alwaysMatch: { acceptInsecureCerts: true } } });
const { contexts } = await send("browsingContext.getTree");
const context = contexts[0].context;
await send("browsingContext.navigate", { context, url, wait: "complete" });

const evaluate = async (expression) => {
  const r = await send("script.evaluate", { expression, target: { context }, awaitPromise: true, userActivation: true });
  if (r.type === "exception") throw new Error(r.exceptionDetails.text);
  return r.result.value;
};

await sleep(seconds * 1000);

const report = await evaluate(`(async () => {
  const out = { message: document.getElementById("message")?.textContent, overlayHidden: document.getElementById("overlay")?.hidden };
  if (!window.__tackPc) return JSON.stringify(out);
  (await window.__tackPc.getStats()).forEach((s) => {
    if (s.type === "inbound-rtp") out[s.kind] = {
      packetsReceived: s.packetsReceived, packetsLost: s.packetsLost, framesDecoded: s.framesDecoded,
      frameWidth: s.frameWidth, frameHeight: s.frameHeight, framesPerSecond: s.framesPerSecond,
      totalDecodeTime: s.totalDecodeTime, jitterBufferDelay: s.jitterBufferDelay, jitterBufferEmittedCount: s.jitterBufferEmittedCount,
      framesDropped: s.framesDropped, totalSamplesReceived: s.totalSamplesReceived,
    };
  });
  const v = document.getElementById("video");
  out.videoElement = { width: v.videoWidth, height: v.videoHeight, paused: v.paused, currentTime: v.currentTime };
  return JSON.stringify(out);
})()`);
const result = JSON.parse(report);

if (hostCursor !== undefined) {
  await evaluate(`window.__tackInputDc.send(new Uint8Array([9, ${Number(hostCursor)}]))`);
  await sleep(500);
}

if (shotPath) {
  const { data } = await send("browsingContext.captureScreenshot", { context });
  writeFileSync(shotPath, Buffer.from(data, "base64"));
}

if (mouseTest) {
  // Host monitor size, then for a few viewport points: move the browser
  // pointer, wait, and compare where the host cursor actually went.
  const mon = JSON.parse(execFileSync("hyprctl", ["-j", "monitors"]).toString())[0];
  result.mouse = [];
  for (const [fx, fy] of [[0.5, 0.5], [0.1, 0.2], [0.9, 0.8]]) {
    const pt = JSON.parse(await evaluate(`(() => {
      const v = document.getElementById("video"), r = v.getBoundingClientRect();
      const vw = v.videoWidth, vh = v.videoHeight, s = Math.min(r.width / vw, r.height / vh);
      const ox = r.left + (r.width - vw * s) / 2, oy = r.top + (r.height - vh * s) / 2;
      const x = Math.round(ox + vw * s * ${fx}), y = Math.round(oy + vh * s * ${fy});
      return JSON.stringify({ x, y, vx: (x - ox) / s / vw, vy: (y - oy) / s / vh });
    })()`));
    await send("input.performActions", { context, actions: [{ type: "pointer", id: "mouse", parameters: { pointerType: "mouse" },
      actions: [{ type: "pointerMove", x: pt.x, y: pt.y, origin: "viewport" }] }] });
    await sleep(300);
    const pos = execFileSync("hyprctl", ["cursorpos"]).toString().trim().split(",").map(Number);
    const expect = [Math.round(mon.x + pt.vx * mon.width), Math.round(mon.y + pt.vy * mon.height)];
    const ok = Math.abs(pos[0] - expect[0]) <= 4 && Math.abs(pos[1] - expect[1]) <= 4;
    result.mouse.push({ expect, host: pos, ok });
  }
}

console.log(JSON.stringify(result, null, 2));
process.exit(result.mouse?.some((m) => !m.ok) ? 1 : 0);
