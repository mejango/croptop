// Feed preview: the days left, huge, and the seconds ticking under it.
export default function (el, { size }) {
  const target = new Date("2026-11-03T12:00:00-05:00");
  el.innerHTML = `<div style="position:absolute;inset:0;display:flex;flex-direction:column;justify-content:center;align-items:center;padding:6%;text-align:center">
    <div class="d" style="font-size:${size === "row" ? 40 : 96}px;font-weight:700;line-height:1">0</div>
    <div style="font-size:13px;opacity:.6">days to the midterms</div>
    <div class="s" style="font-family:ui-monospace,monospace;font-size:${size === "row" ? 12 : 18}px;opacity:.8;margin-top:6%">00:00:00</div></div>`;
  const d = el.querySelector(".d"), s = el.querySelector(".s");
  const tick = () => { let t = Math.max(0, Math.floor((target - Date.now()) / 1000)); d.textContent = Math.floor(t / 86400); t %= 86400;
    s.textContent = [Math.floor(t / 3600), Math.floor(t % 3600 / 60), t % 60].map((x) => String(x).padStart(2, "0")).join(":"); };
  tick(); setInterval(tick, 1000);
}
