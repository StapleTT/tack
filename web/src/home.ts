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
  $("start").setAttribute("aria-disabled", String(!ready));
}

refresh();
setInterval(refresh, 5000);
