// Feed preview: every post on this site as a dot on one line, the newest lit.
export default function (el, { site, post, size }) {
  const accent = getComputedStyle(document.body).getPropertyValue("--link-color").trim() || "#F056C1";
  const fg = getComputedStyle(el).color || "currentColor";
  const posts = [...site.articles].sort((a, b) => a.created - b.created);
  const first = posts[0].created, span = Math.max(1, posts[posts.length - 1].created - first);
  const toDate = (t) => new Date((t + 978307200) * 1000);
  el.innerHTML = `<div style="position:absolute;inset:0;padding:8%;display:flex;flex-direction:column;justify-content:center">
    <div style="font-size:${size === "row" ? 22 : 44}px;font-weight:700;line-height:1">${posts.length} posts</div>
    <div style="font-size:12px;opacity:.55;margin-bottom:12%">${toDate(first).toLocaleDateString(undefined, { month: "short", year: "numeric" })} to now</div>
    <div class="track" style="position:relative;height:2px;background:${fg};opacity:.25"></div>
    <div class="cap" style="font-size:12px;opacity:.6;margin-top:10%;min-height:1.4em"></div></div>`;
  const track = el.querySelector(".track"), cap = el.querySelector(".cap");
  const dots = posts.map((p, i) => {
    const d = document.createElement("div");
    d.style.cssText = `position:absolute;top:-4px;left:calc(${(p.created - first) / span * 100}% - 5px);width:10px;height:10px;border-radius:50%;background:${p.id === post.id ? "#3bb273" : fg};opacity:${p.id === post.id ? 1 : .55};transition:transform .3s,background .3s`;
    track.appendChild(d); return d;
  });
  let i = 0;
  const step = () => { if (!el.isConnected) return; dots.forEach((d, j) => { d.style.transform = j === i ? "scale(1.8)" : ""; d.style.background = j === i ? accent : posts[j].id === post.id ? "#3bb273" : fg; }); cap.textContent = (posts[i].title || "untitled") + ", " + toDate(posts[i].created).toLocaleDateString(); i = (i + 1) % posts.length; };
  step(); setInterval(() => document.hidden || step(), 1800);
}
