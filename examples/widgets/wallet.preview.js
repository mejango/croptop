// Feed preview: a wallet address that types itself out and pulses, asking "you?".
export default function (el, { size }) {
  const accent = getComputedStyle(document.body).getPropertyValue("--link-color").trim() || "#F056C1";
  el.innerHTML = `<div style="position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:14px;padding:6%">
    <div class="ring" style="width:${size === "row" ? 44 : 84}px;aspect-ratio:1;border-radius:50%;border:2px solid ${accent};display:grid;place-items:center;font-size:${size === "row" ? 12 : 22}px;font-weight:700;color:${accent}">you?</div>
    <div class="addr" style="font-family:ui-monospace,monospace;font-size:${size === "row" ? 11 : 14}px;opacity:.75;letter-spacing:.04em;min-height:1.4em"></div>
    <div style="font-size:12px;opacity:.5">read-only. nothing is signed.</div></div>
    <style>@keyframes ctp{0%,100%{box-shadow:0 0 0 0 ${accent}55}50%{box-shadow:0 0 0 14px ${accent}00}}</style>`;
  el.querySelector(".ring").style.animation = "ctp 2.4s ease-in-out infinite";
  const addr = el.querySelector(".addr"); const hex = "0123456789abcdef";
  const type = () => {
    const target = "0x" + Array.from({ length: 40 }, () => hex[Math.random() * 16 | 0]).join("");
    let i = 0; const short = (s) => s.slice(0, 6) + "…" + s.slice(-4);
    // narrow tiles cannot fit a full address mid-typing, so type the short form there
    const shown = el.clientWidth < 300 ? short(target) : target, len = shown.length;
    const step = () => { if (!el.isConnected) return; i += 2; addr.textContent = i >= len ? short(target) : shown.slice(0, i); if (i < len) setTimeout(step, 28); else setTimeout(type, 3200); };
    step();
  };
  type();
}
