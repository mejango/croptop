// Generative: coins flow from many small donors into one campaign, then out again as spending.
// Deterministic per post: the post id seeds the layout, so every reader sees the same piece.
const canvas = document.getElementById("motion"), ctx = canvas.getContext("2d");
let seed = 0; for (const ch of croptop.postId || "x") seed = (seed * 31 + ch.charCodeAt(0)) >>> 0;
const rand = () => (seed = (seed * 1664525 + 1013904223) >>> 0) / 2 ** 32;
const accent = getComputedStyle(document.body).getPropertyValue("--link-color").trim() || "#F056C1";
const W = () => canvas.width, H = () => canvas.height;
const donors = Array.from({ length: 28 }, () => ({ x: rand() * 0.35, y: rand(), r: 2 + rand() * 4 }));
const spend = Array.from({ length: 5 }, (_, i) => ({ x: 0.85 + rand() * 0.1, y: 0.15 + i * 0.175, label: ["organizers", "ads", "staff", "events", "travel"][i] }));
const coins = [];
function spawn() { const d = donors[Math.floor(rand() * donors.length)]; coins.push({ from: { x: d.x, y: d.y }, to: { x: 0.5, y: 0.5 }, t: 0, phase: 0, speed: 0.004 + rand() * 0.006 }); }
function frame() {
  if (document.hidden) return requestAnimationFrame(frame);
  canvas.width = canvas.clientWidth * devicePixelRatio; canvas.height = canvas.clientHeight * devicePixelRatio;
  const w = W(), h = H(); ctx.clearRect(0, 0, w, h);
  ctx.fillStyle = "rgba(255,255,255,.35)"; for (const d of donors) { ctx.beginPath(); ctx.arc(d.x * w, d.y * h, d.r * devicePixelRatio, 0, 7); ctx.fill(); }
  ctx.fillStyle = accent; ctx.beginPath(); ctx.arc(0.5 * w, 0.5 * h, 18 * devicePixelRatio, 0, 7); ctx.fill();
  ctx.fillStyle = "rgba(255,255,255,.7)"; ctx.font = `${12 * devicePixelRatio}px sans-serif`; ctx.textAlign = "left";
  for (const s of spend) { ctx.fillRect(s.x * w - 3, s.y * h - 3, 6 * devicePixelRatio, 6 * devicePixelRatio); ctx.fillText(s.label, s.x * w + 10 * devicePixelRatio, s.y * h + 4 * devicePixelRatio); }
  if (rand() < 0.12) spawn();
  for (const c of coins) {
    c.t += c.speed;
    if (c.t >= 1) { if (c.phase === 0) { c.phase = 1; c.t = 0; c.from = { x: 0.5, y: 0.5 }; const s = spend[Math.floor(rand() * spend.length)]; c.to = { x: s.x, y: s.y }; } else { c.done = true; continue; } }
    const e = c.t < .5 ? 2 * c.t * c.t : -1 + (4 - 2 * c.t) * c.t;
    const x = (c.from.x + (c.to.x - c.from.x) * e) * w, y = (c.from.y + (c.to.y - c.from.y) * e + Math.sin(c.t * Math.PI) * (c.phase ? 0.04 : -0.06)) * h;
    ctx.fillStyle = c.phase ? "#3bb273" : accent; ctx.beginPath(); ctx.arc(x, y, 3 * devicePixelRatio, 0, 7); ctx.fill();
  }
  for (let i = coins.length - 1; i >= 0; i--) if (coins[i].done) coins.splice(i, 1);
  requestAnimationFrame(frame);
}
frame();
