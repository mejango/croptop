// Feed preview: the live gas price, big, with a sparkline underneath.
export default function (el, { size }) {
  const accent = getComputedStyle(document.body).getPropertyValue("--link-color").trim() || "#F056C1";
  el.innerHTML = `<div style="position:absolute;inset:0;display:flex;flex-direction:column;justify-content:center;padding:6%;font-family:inherit">
    <div style="font-size:12px;opacity:.55;letter-spacing:.02em">ethereum gas, live</div>
    <div class="n" style="font-size:${size === "row" ? 28 : 56}px;line-height:1.05;font-weight:700">…</div>
    <div class="b" style="font-size:13px;opacity:.6;margin-top:4px">connecting</div>
    <canvas style="position:absolute;left:0;right:0;bottom:0;width:100%;height:38%;display:block;opacity:.85"></canvas></div>`;
  const n = el.querySelector(".n"), b = el.querySelector(".b"), canvas = el.querySelector("canvas"), ctx = canvas.getContext("2d");
  const rpc = new ethers.JsonRpcProvider("https://ethereum-rpc.publicnode.com");
  const hist = [];
  const draw = () => {
    const w = canvas.width = canvas.clientWidth * devicePixelRatio, h = canvas.height = canvas.clientHeight * devicePixelRatio;
    ctx.clearRect(0, 0, w, h); if (hist.length < 2) return;
    const max = Math.max(...hist) * 1.15, min = Math.min(...hist) * 0.85;
    ctx.beginPath(); ctx.lineWidth = 2 * devicePixelRatio; ctx.strokeStyle = accent;
    hist.forEach((v, i) => { const x = i / (hist.length - 1) * w, y = h - (v - min) / (max - min) * h * .8 - h * .1; i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); });
    ctx.stroke();
    ctx.lineTo(w, h); ctx.lineTo(0, h); ctx.closePath(); ctx.fillStyle = accent + "22"; ctx.fill();
  };
  const tick = async () => {
    try {
      const [fee, block] = await Promise.all([rpc.getFeeData(), rpc.getBlockNumber()]);
      const gwei = Number(ethers.formatUnits(fee.gasPrice ?? 0n, "gwei"));
      hist.push(gwei); if (hist.length > 40) hist.shift();
      n.textContent = gwei.toLocaleString(undefined, { maximumFractionDigits: gwei < 1 ? 3 : 1 }) + " gwei";
      b.textContent = "block " + block.toLocaleString();
      draw();
    } catch (e) { b.textContent = "network unreachable"; }
  };
  tick(); const t = setInterval(() => document.hidden || tick(), 12000);
  new ResizeObserver(draw).observe(canvas);
}
