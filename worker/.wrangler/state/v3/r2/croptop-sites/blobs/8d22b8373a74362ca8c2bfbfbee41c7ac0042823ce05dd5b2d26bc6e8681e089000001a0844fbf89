// A campaign budget you can push around. Your allocation stays on your device.
const categories = [["Field organizing", 35], ["Ads", 30], ["Staff", 20], ["Events", 10], ["Travel", 5]];
const key = "budget:" + croptop.postId;
const root = document.getElementById("budget");
let shares = JSON.parse(localStorage.getItem(key) || "null") || categories.map(([, v]) => v);
const total = 250000;
const colors = ["#F056C1", "#3bb273", "#efab1d", "#4ea8de", "#adadaf"];
const rows = categories.map(([name], i) => {
  const row = document.createElement("div");
  row.innerHTML = `<div style="display:flex;justify-content:space-between;font-size:14px"><span>${name}</span><span class="pct"></span></div>
    <input type="range" min="0" max="100" step="1" style="width:100%;accent-color:${colors[i]}">
    <div class="bar" style="height:10px;background:${colors[i]};width:0;transition:width .2s"></div>`;
  root.appendChild(row);
  row.querySelector("input").addEventListener("input", (e) => set(i, Number(e.target.value)));
  return row;
});
const summary = document.createElement("p"); summary.style.cssText = "font-size:13px;opacity:.7"; root.appendChild(summary);
function set(i, v) {
  // move the difference proportionally across the other categories so the total stays 100
  const others = shares.reduce((s, x, j) => j === i ? s : s + x, 0);
  const rest = 100 - v;
  shares = shares.map((x, j) => j === i ? v : others ? x / others * rest : rest / (shares.length - 1));
  localStorage.setItem(key, JSON.stringify(shares)); render();
}
function render() {
  rows.forEach((row, i) => {
    row.querySelector("input").value = shares[i];
    row.querySelector(".pct").textContent = `${Math.round(shares[i])}% · $${Math.round(total * shares[i] / 100).toLocaleString()}`.replace(" · ", ", ");
    row.querySelector(".bar").style.width = shares[i] + "%";
  });
  const top = categories[shares.indexOf(Math.max(...shares))][0];
  summary.textContent = `$${total.toLocaleString()} total. Most of your money goes to ${top.toLowerCase()}. Only you can see this split; it lives in your browser.`;
}
render();
