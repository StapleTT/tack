// Homepage: host status and the way into /stream.

interface Status {
  host: string;
  online: boolean;
  paired: boolean;
  app: string;
  streaming: boolean;
  error?: string;
}

const $ = (id: string) => document.getElementById(id) as HTMLElement;

async function refresh() {
  let st: Status;
  try {
    const res = await fetch("/api/status", { cache: "no-store" });
    if (res.status === 401) { location.assign("/login"); return; }
    st = await res.json();
  } catch {
    st = { host: "", online: false, paired: false, app: "", streaming: false, error: "bridge unreachable" };
  }
  const ready = st.online && st.paired;
  $("dot").className = `dot ${ready ? "ok" : "bad"}`;
  $("host-name").textContent = st.host || "Host";
  $("host-detail").textContent = !st.online
    ? `Offline${st.error ? ` (${st.error})` : ""}`
    : !st.paired
      ? "Online, but this bridge is not paired with it"
      : st.app ? `Online · ${st.app} running` : "Online · idle";
  $("notice").textContent = st.streaming ? "Another device is streaming. Starting here takes over." : "";
  if (ready) $("start").setAttribute("href", "/stream");
  else $("start").removeAttribute("href");
  $("start").textContent = st.streaming ? "Take over stream" : "Start streaming";
  $("start").setAttribute("aria-disabled", String(!ready));
}

refresh();
setInterval(refresh, 5000);

const pair = $("pair") as HTMLFormElement;
pair.onsubmit = async (event) => {
  event.preventDefault();
  const button = pair.querySelector("button")!;
  button.disabled = true;
  const pin = String(crypto.getRandomValues(new Uint32Array(1))[0] % 10000).padStart(4, "0");
  $("pair-result").textContent = `Enter PIN ${pin} on the Sunshine host at https://localhost:47990/pin.`;
  try {
    const res = await fetch("/api/pair", { method: "POST", headers: {"Content-Type":"application/json"},
      body: JSON.stringify({ address: ($("address") as HTMLInputElement).value.trim(), pin }) });
    $("pair-result").textContent = res.ok ? "Host paired and saved." : await res.text();
  } catch { $("pair-result").textContent = "Could not reach the bridge."; }
  finally { button.disabled = false; void refresh(); }
};

for (const id of ["res", "fps", "bitrate"]) {
  const select = $(id) as HTMLSelectElement;
  const saved = localStorage.getItem("tack." + id);
  if (Array.from(select.options).some(option => option.value === saved)) select.value = saved!;
  select.onchange = () => localStorage.setItem("tack." + id, select.value);
}
for (const [key, name] of Object.entries({esc:"Esc", win:"Super", alttab:"Alt+Tab", altf4:"Alt+F4", cad:"Ctrl+Alt+Del"})) {
  const label = document.createElement("label");
  const box = document.createElement("input");
  box.type = "checkbox";
  box.checked = localStorage.getItem("tack.key." + key) !== "off";
  box.onchange = () => localStorage.setItem("tack.key." + key, box.checked ? "on" : "off");
  label.append(box, name);
  $("send-keys").append(label);
}

const passwordForm = $("password-form") as HTMLFormElement;
passwordForm.onsubmit = async (event) => {
  event.preventDefault();
  const body = new URLSearchParams();
  new FormData(passwordForm).forEach((value, name) => body.set(name, String(value)));
  const button = passwordForm.querySelector("button")!;
  button.disabled = true;
  try {
    const res = await fetch(passwordForm.action, {method: "POST", body});
    if (res.ok) { passwordForm.reset(); location.assign("/login"); }
    else $("password-result").textContent = await res.text();
  } catch { $("password-result").textContent = "Could not reach the bridge."; }
  finally { button.disabled = false; }
};
