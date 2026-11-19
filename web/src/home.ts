// Admin panel: host status, pairing, per-browser presets and the way into /stream.

interface Status {
  host: string;
  address?: string;
  online: boolean;
  paired: boolean;
  app: string;
  streaming: boolean;
  agent: boolean;
  error?: string;
}

const $ = <T extends HTMLElement = HTMLElement>(id: string) => document.getElementById(id) as T;
const pref = (key: string) => localStorage.getItem("tack." + key) ?? "";

// Views: #connect, #host, #presets.
const views = ["connect", "host", "presets"];
function route() {
  const view = views.includes(location.hash.slice(1)) ? location.hash.slice(1) : "connect";
  for (const v of views) $(v).hidden = v !== view;
  for (const a of document.querySelectorAll("nav a")) {
    const on = a.getAttribute("href") === "#" + view;
    a.textContent = (on ? "> " : "  ") + a.textContent!.trim().replace(/^> /, "");
    if (on) a.setAttribute("aria-current", "page"); else a.removeAttribute("aria-current");
  }
}
addEventListener("hashchange", route);
route();

const ua = navigator.userAgent;
const browser = /Firefox\//.test(ua) ? "Firefox" : /Edg\//.test(ua) ? "Edge" : /Chrome\//.test(ua) ? "Chrome" : "Browser";
const os = /CrOS/.test(ua) ? "Chromebook" : /Windows/.test(ua) ? "Windows" : /Mac OS/.test(ua) ? "Mac" : /Linux/.test(ua) ? "Linux" : "";
const dpr = devicePixelRatio || 1;
$("device-line").textContent = `This device: ${[os, browser].filter(Boolean).join(" · ")} · ${Math.round(screen.width * dpr)}×${Math.round(screen.height * dpr)}`;

function presetLine() {
  const res = pref("res").replace("x", "×") || "Native";
  const kbps = Number(pref("bitrate"));
  $("preset-line").textContent = `${res} · ${pref("fps") || 60} fps · ${kbps ? kbps / 1000 + " Mbps" : "Auto bitrate"}`;
}
presetLine();

let pairing: AbortController | null = null;
let repairing = false;
let last: Status | null = null;

function pairStep(step: number) {
  for (const el of document.querySelectorAll<HTMLElement>("[data-step]")) el.hidden = el.dataset.step !== String(step);
  $("pair-steps").querySelectorAll("div").forEach((d, i) => d.classList.toggle("on", i <= step));
}

async function refresh() {
  let st: Status;
  try {
    const res = await fetch("/api/status", { cache: "no-store" });
    if (res.status === 401) { location.assign("/login"); return; }
    st = await res.json();
  } catch {
    st = { host: "", online: false, paired: false, app: "", streaming: false, agent: false, error: "bridge unreachable" };
  }
  last = st;
  $("agent-dot").classList.toggle("ok", st.agent);
  $("agent-label").textContent = `Host agent ${st.agent ? "connected" : "waiting"}`;

  const saved = !!st.host;
  $("no-host").hidden = saved || !!pairing;
  $("host-card").hidden = !saved;
  const ready = st.online && st.paired;
  $("dot").classList.toggle("ok", ready);
  $("host-name").textContent = st.host || "Host";
  const state = !st.online
    ? `Offline${st.error ? ` (${st.error})` : ""}`
    : !st.paired ? "Online, but not paired with this bridge"
    : st.app ? `Online · ${st.app} running` : "Online · idle";
  $("host-detail").textContent = [st.address !== st.host ? st.address : "", state].filter(Boolean).join(" · ");
  $("session-label").textContent = st.streaming ? "Another device is streaming" : "No active stream";
  const start = $("start");
  if (ready) start.setAttribute("href", "/stream"); else start.removeAttribute("href");
  start.textContent = st.streaming ? "Take over" : "Connect";
  start.setAttribute("aria-disabled", String(!ready));

  if (!pairing) {
    pairStep(saved && !repairing ? 2 : 0);
    $("paired-title").textContent = st.paired ? `Paired with ${st.host}` : `Saved host ${st.host}`;
    $("paired-address").textContent = st.address ?? st.host;
    $("paired-status").textContent = state;
    $("paired-agent").textContent = st.agent ? "connected" : "waiting";
  }
}

refresh();
setInterval(refresh, 5000);

$("repair").onclick = () => { repairing = true; $<HTMLInputElement>("address").value = last?.address ?? ""; pairStep(0); };
$("cancel-pair").onclick = () => pairing?.abort();

const pair = $<HTMLFormElement>("pair");
pair.onsubmit = async (event) => {
  event.preventDefault();
  const pin = String(crypto.getRandomValues(new Uint32Array(1))[0] % 10000).padStart(4, "0");
  $("pin-digits").replaceChildren(...[...pin].map((d) => Object.assign(document.createElement("span"), { textContent: d })));
  $("pair-result").textContent = "";
  pairing = new AbortController();
  pairStep(1);
  try {
    const res = await fetch("/api/pair", { method: "POST", headers: { "Content-Type": "application/json" }, signal: pairing.signal,
      body: JSON.stringify({ address: $<HTMLInputElement>("address").value.trim(), pin }) });
    if (res.ok) repairing = false;
    else $("pair-result").textContent = await res.text();
  } catch (err) {
    $("pair-result").textContent = (err as Error).name === "AbortError" ? "Pairing cancelled." : "Could not reach the bridge.";
  } finally {
    pairing = null;
    pairStep(0);
    await refresh();
  }
};

// Segmented controls backed by localStorage.
function seg(id: string, key: string, options: [string, string][], onPick = () => {}) {
  const box = $(id);
  const current = options.some(([v]) => v === pref(key)) ? pref(key) : options[0][0];
  for (const [value, label] of options) {
    const b = document.createElement("button");
    b.type = "button";
    b.textContent = label;
    b.setAttribute("aria-pressed", String(value === current));
    b.onclick = () => {
      localStorage.setItem("tack." + key, value);
      box.querySelectorAll("button").forEach((x) => x.setAttribute("aria-pressed", String(x === b)));
      onPick();
    };
    box.append(b);
  }
}
seg("res", "res", [["", "Native"], ["1366x768", "1366×768"], ["1600x900", "1600×900"], ["1920x1080", "1920×1080"], ["2560x1440", "2560×1440"]], presetLine);
seg("fps", "fps", [["60", "60"], ["30", "30"], ["120", "120"]], presetLine);

const bitrate = $<HTMLInputElement>("bitrate");
const showBitrate = () => { $("bitrate-out").textContent = bitrate.value === "0" ? "Auto" : `${bitrate.value} Mbps`; };
bitrate.value = String(Math.round(Number(pref("bitrate")) / 1000));
showBitrate();
bitrate.oninput = showBitrate;
bitrate.onchange = () => {
  localStorage.setItem("tack.bitrate", bitrate.value === "0" ? "" : String(Number(bitrate.value) * 1000));
  presetLine();
};

// Each "Send to host" key is an independent toggle.
for (const [key, name] of Object.entries({ esc: "Esc", win: "Super", alttab: "Alt Tab", altf4: "Alt F4", cad: "Ctrl Alt Del" })) {
  const b = document.createElement("button");
  b.type = "button";
  b.textContent = name;
  b.setAttribute("aria-pressed", String(pref("key." + key) !== "off"));
  b.onclick = () => {
    const on = b.getAttribute("aria-pressed") !== "true";
    b.setAttribute("aria-pressed", String(on));
    localStorage.setItem("tack.key." + key, on ? "on" : "off");
  };
  $("send-keys").append(b);
}

const passwordForm = $<HTMLFormElement>("password-form");
passwordForm.onsubmit = async (event) => {
  event.preventDefault();
  const body = new URLSearchParams();
  new FormData(passwordForm).forEach((value, name) => body.set(name, String(value)));
  const button = passwordForm.querySelector("button")!;
  button.disabled = true;
  try {
    const res = await fetch(passwordForm.action, { method: "POST", body });
    if (res.ok) { passwordForm.reset(); location.assign("/login"); }
    else $("password-result").textContent = await res.text();
  } catch { $("password-result").textContent = "Could not reach the bridge."; }
  finally { button.disabled = false; }
};
