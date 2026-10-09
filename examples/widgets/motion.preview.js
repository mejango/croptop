// Feed preview: the same flow of coins, smaller and calmer. Hover to become a donor:
// coins stream from the pointer into the campaign and donors near you light up.
export default function (el, { post }) {
  const canvas = document.createElement("canvas");
  canvas.style.cssText = "position:absolute;inset:0;width:100%;height:100%;display:block";
  el.appendChild(canvas);
  const ctx = canvas.getContext("2d");
  let seed = 0; for (const ch of post.id) seed = (seed * 31 + ch.charCodeAt(0)) >>> 0;
  const rand = () => (seed = (seed * 1664525 + 1013904223) >>> 0) / 2 ** 32;
  const accent = getComputedStyle(document.body).getPropertyValue("--link-color").trim() || "#F056C1";
  const fg = getComputedStyle(el).color || "#888";
  const donors = Array.from({ length: 16 }, () => ({ x: .08 + rand() * .3, y: .1 + rand() * .8 }));
  const spend = Array.from({ length: 5 }, (_, i) => ({ x: .88, y: .15 + i * .175 }));
  const coins = [];
  let mouse = null;
  el.addEventListener("pointermove", (e) => { const b = canvas.getBoundingClientRect(); mouse = { x: (e.clientX - b.left) / b.width, y: (e.clientY - b.top) / b.height }; });
  el.addEventListener("pointerleave", () => { mouse = null; });
  const frame = () => {
    if (!el.isConnected) return;
    if (document.hidden) return requestAnimationFrame(frame);
    const w = canvas.width = canvas.clientWidth * devicePixelRatio, h = canvas.height = canvas.clientHeight * devicePixelRatio, r = devicePixelRatio;
    ctx.clearRect(0, 0, w, h);
    for (const d of donors) {
      const near = mouse && Math.hypot(d.x - mouse.x, (d.y - mouse.y) * h / w) < .14;
      ctx.globalAlpha = near ? 1 : .45; ctx.fillStyle = near ? accent : fg;
      ctx.beginPath(); ctx.arc(d.x * w, d.y * h, (near ? 4 : 2.5) * r, 0, 7); ctx.fill();
    }
    if (mouse) {
      ctx.globalAlpha = .9; ctx.strokeStyle = accent; ctx.lineWidth = 1.5 * r;
      ctx.beginPath(); ctx.arc(mouse.x * w, mouse.y * h, (7 + Math.sin(Date.now() / 200) * 2) * r, 0, 7); ctx.stroke();
      if (Math.random() < .35) coins.push({ from: { ...mouse }, to: { x: .5, y: .5 }, t: 0, phase: 0, sp: .012 + Math.random() * .01 });
    }
    ctx.globalAlpha = 1; ctx.fillStyle = accent; ctx.beginPath(); ctx.arc(.5 * w, .5 * h, 12 * r, 0, 7); ctx.fill();
    ctx.globalAlpha = .7; ctx.fillStyle = fg; for (const s of spend) ctx.fillRect(s.x * w - 2 * r, s.y * h - 2 * r, 4 * r, 4 * r);
    if (rand() < .08) { const d = donors[Math.floor(rand() * donors.length)]; coins.push({ from: d, to: { x: .5, y: .5 }, t: 0, phase: 0, sp: .006 + rand() * .006 }); }
    for (const c of coins) {
      c.t += c.sp;
      if (c.t >= 1) { if (c.phase) { c.done = true; continue; } c.phase = 1; c.t = 0; c.from = { x: .5, y: .5 }; c.to = spend[Math.floor(rand() * 5)]; }
      const e = c.t < .5 ? 2 * c.t * c.t : -1 + (4 - 2 * c.t) * c.t;
      const x = (c.from.x + (c.to.x - c.from.x) * e) * w, y = (c.from.y + (c.to.y - c.from.y) * e + Math.sin(c.t * Math.PI) * (c.phase ? .04 : -.06)) * h;
      ctx.globalAlpha = 1; ctx.fillStyle = c.phase ? "#3bb273" : accent; ctx.beginPath(); ctx.arc(x, y, 2.5 * r, 0, 7); ctx.fill();
    }
    for (let i = coins.length - 1; i >= 0; i--) if (coins[i].done) coins.splice(i, 1);
    requestAnimationFrame(frame);
  };
  frame();
}
