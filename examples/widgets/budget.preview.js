// Feed preview: five bars that keep rebalancing themselves, an invitation to drag.
export default function (el) {
  const tight = el.clientHeight < 260; // console tiles are square and small
  const names = ["organizing", "ads", "staff", "events", "travel"];
  const colors = ["#F056C1", "#3bb273", "#efab1d", "#4ea8de", "#adadaf"];
  el.innerHTML = `<div style="position:absolute;inset:0;padding:7%;display:flex;flex-direction:column;justify-content:center;gap:${tight ? "4%" : "9%"};padding:${tight ? "6%" : "7%"}">
    ${names.map((n, i) => `<div><div style="display:flex;justify-content:space-between;font-size:${tight ? 11 : 12}px;opacity:.7"><span>${n}</span><span class="p"></span></div>
      <div style="height:9px;background:currentColor;opacity:.9"><div style="position:relative;height:100%;background:var(--background-color,#fff);opacity:.88"><div class="bar" style="height:100%;width:0;background:${colors[i]};transition:width 1.6s cubic-bezier(.2,.8,.2,1)"></div></div></div></div>`).join("")}
    ${tight ? "" : `<div style="font-size:12px;opacity:.5;text-align:right">how would you spend $250,000?</div>`}</div>`;
  const bars = el.querySelectorAll(".bar"), pcts = el.querySelectorAll(".p");
  const set = (v) => v.forEach((x, i) => { bars[i].style.width = x + "%"; pcts[i].textContent = Math.round(x) + "%"; });
  const rand = () => { const r = names.map(() => Math.random() + .15), s = r.reduce((a, b) => a + b); return r.map((x) => x / s * 100); };
  requestAnimationFrame(() => set([35, 30, 20, 10, 5]));
  setInterval(() => document.hidden || set(rand()), 2600);
}
