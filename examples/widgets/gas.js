// Live Ethereum stats straight from a public RPC, no server in between.
const $ = (id) => document.getElementById(id);
const rpc = new ethers.JsonRpcProvider("https://ethereum-rpc.publicnode.com");
const history = [];
const canvas = $("gas-spark"), ctx = canvas.getContext("2d");
const fmt = (n, d = 2) => Number(n).toLocaleString(undefined, { maximumFractionDigits: d });

async function tick() {
  try {
    const [block, fee, price] = await Promise.all([
      rpc.getBlock("latest"),
      rpc.getFeeData(),
      fetch("https://api.coinbase.com/v2/prices/ETH-USD/spot").then((r) => r.json()).then((j) => Number(j.data.amount)).catch(() => null),
    ]);
    const gwei = Number(ethers.formatUnits(fee.gasPrice ?? 0n, "gwei"));
    $("gas-block").textContent = "#" + block.number.toLocaleString();
    $("gas-fee").textContent = fmt(gwei, 2) + " gwei";
    $("gas-txs").textContent = block.transactions.length + " txs";
    if (price) $("gas-eth").textContent = "$" + fmt(price, 0);
    // what a simple transfer costs right now
    const usd = price ? (gwei * 21000 / 1e9) * price : null;
    $("gas-transfer").textContent = usd == null ? "" : usd < 0.01 ? "a plain ETH transfer costs less than a cent right now." : "a plain ETH transfer costs about $" + fmt(usd, 2) + ".";
    history.push(gwei); if (history.length > 60) history.shift();
    draw();
    $("gas-status").textContent = "live, updated " + new Date().toLocaleTimeString();
  } catch (e) {
    $("gas-status").textContent = "could not reach the network: " + e.message;
  }
}
function draw() {
  const w = canvas.width = canvas.clientWidth * devicePixelRatio, h = canvas.height = 60 * devicePixelRatio;
  ctx.clearRect(0, 0, w, h);
  if (history.length < 2) return;
  const max = Math.max(...history) * 1.1, min = Math.min(...history) * 0.9;
  ctx.lineWidth = 2 * devicePixelRatio; ctx.strokeStyle = getComputedStyle(document.body).getPropertyValue("--link-color") || "#F056C1";
  ctx.beginPath();
  history.forEach((v, i) => { const x = i / (history.length - 1) * w, y = h - (v - min) / (max - min) * h; i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); });
  ctx.stroke();
}
tick(); setInterval(tick, 12000);
