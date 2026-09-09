/* Croptop console. Hash-routed, no build step. Talks to /v0. */
(() => {
  const $ = (sel, el = document) => el.querySelector(sel);
  const h = (tag, attrs = {}, ...kids) => {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
      if (k === "class") el.className = v;
      else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
      else if (v !== null && v !== undefined) el.setAttribute(k, v);
    }
    for (const kid of kids.flat()) if (kid !== null && kid !== undefined) el.append(kid.nodeType ? kid : document.createTextNode(kid));
    return el;
  };
  const APPLE = 978307200;
  const when = (apple) => apple ? new Date((apple + APPLE) * 1000) : null;
  const ago = (d) => {
    if (!d) return "never";
    const s = (Date.now() - d.getTime()) / 1000;
    if (s < 60) return "just now";
    if (s < 3600) return Math.floor(s / 60) + " min ago";
    if (s < 86400) return Math.floor(s / 3600) + " h ago";
    return d.toLocaleDateString();
  };

  let toastTimer;
  const toast = (msg, err) => {
    const t = $("#toast");
    t.textContent = msg; t.hidden = false; t.className = err ? "err" : "";
    clearTimeout(toastTimer); toastTimer = setTimeout(() => (t.hidden = true), err ? 8000 : 3000);
  };

  const api = async (method, url, body, isForm) => {
    const opts = { method, headers: {} };
    if (body !== undefined) {
      if (isForm) opts.body = body;
      else { opts.body = JSON.stringify(body); opts.headers["Content-Type"] = "application/json"; }
    }
    const r = await fetch(url, opts);
    const text = await r.text();
    let data; try { data = JSON.parse(text); } catch { data = { _raw: text }; }
    if (!r.ok) throw new Error(data.error || text || r.statusText);
    return data;
  };

  const state = { sites: [], following: [], status: null, template: null };

  const loadSites = async () => {
    [state.sites, state.following] = await Promise.all([api("GET", "/v0/planets/my?all=true"), api("GET", "/v0/croptop/following").catch(() => [])]);
    renderRail();
  };
  const loadStatus = async () => {
    try { state.status = await api("GET", "/v0/croptop/status"); } catch (e) { state.status = null; }
    const n = $("#node");
    n.replaceChildren();
    if (!state.status) { n.append("Console offline"); return; }
    const ipfs = state.status.ipfs;
    n.append(h("b", { class: ipfs.running ? "" : "off" }, ipfs.running ? "IPFS on" : "IPFS off"), `, ${ipfs.peers} peers`, h("br"), `croptop ${state.status.version}`);
  };
  const template = async () => state.template || (state.template = await api("GET", "/v0/croptop/template"));

  const renderRail = () => {
    const box = $("#sites");
    box.replaceChildren();
    const cur = location.hash.split("/")[2];
    for (const s of state.sites) {
      if (s.archived) continue;
      box.append(h("a", { href: "#/site/" + s.id, class: s.id === cur ? "current" : "" }, s.name, h("small", {}, s.publishedElsewhere ? "published elsewhere" : s.domain || s.ipns.slice(0, 12) + "…")));
    }
    if (state.following.length) {
      box.append(h("div", { class: "rail-head" }, "Following"));
      const curF = location.hash.startsWith("#/f/") ? location.hash.split("/")[2] : "";
      for (const f of state.following) {
        box.append(h("a", { href: "#/f/" + f.ipns, class: f.ipns === curF ? "current" : "" }, f.title || f.name, h("small", {}, f.error ? "check failed" : f.name !== f.ipns ? f.name : f.ipns.slice(0, 12) + "…")));
      }
    }
  };

  /* ---------- views ---------- */
  const views = {};

  views.home = async () => {
    const m = $("#main");
    m.replaceChildren(h("div", { class: "head" }, h("div", {}, h("h1", {}, "Your sites"), h("p", {}, "Pick a site on the left, start a new one, or adopt one you already publish elsewhere with its key."))));
    if (!state.sites.length) {
      m.append(h("div", { class: "empty" }, "No sites yet. ", h("a", { href: "#/new" }, h("u", {}, "Start one")), " or ", h("a", { href: "#/adopt" }, h("u", {}, "adopt an existing site")), "."));
    }
  };

  views.new = async () => {
    const m = $("#main");
    const form = h("form", { class: "sheet", onsubmit: async (e) => {
      e.preventDefault();
      const fd = new FormData(form); fd.set("template", "Croptop");
      const btn = $("button[type=submit]", form); btn.disabled = true;
      try { const s = await api("POST", "/v0/planets/my", fd, true); await loadSites(); location.hash = "#/site/" + s.id; toast("Site created"); }
      catch (err) { toast(err.message, true); btn.disabled = false; }
    }},
      h("label", {}, "Name", h("input", { type: "text", name: "name", required: "", autofocus: "" })),
      h("label", {}, "About", h("input", { type: "text", name: "about" }), h("span", { class: "help" }, "Shown under the site name. Markdown works.")),
      h("label", {}, "Avatar", h("input", { type: "file", name: "avatar", accept: "image/*" })),
      h("div", { class: "row" }, h("button", { class: "btn hot", type: "submit" }, "Create site")));
    m.replaceChildren(h("div", { class: "head" }, h("div", {}, h("h1", {}, "New site"), h("p", {}, "A new IPNS key is generated on this machine. Export it from the site's settings before you switch computers."))), form);
  };

  views.adopt = async () => {
    const m = $("#main");
    const log = h("pre", { class: "log", hidden: "" });
    const form = h("form", { class: "sheet", onsubmit: async (e) => {
      e.preventDefault();
      const btn = $("button[type=submit]", form); btn.disabled = true;
      log.hidden = false; log.textContent = "Fetching the site from IPFS. This can take a minute.";
      try {
        const r = await api("POST", "/v0/croptop/adopt", { name: $("[name=name]", form).value.trim(), pem: $("[name=pem]", form).value });
        await loadSites(); location.hash = "#/site/" + r.id; toast("Site adopted");
      } catch (err) { log.textContent = err.message; btn.disabled = false; }
    }},
      h("label", {}, "IPNS name or ENS domain", h("input", { type: "text", name: "name", required: "", placeholder: "k51… or yoursite.eth" })),
      h("label", {}, "Site key (PEM)", h("textarea", { name: "pem", class: "pem", required: "", placeholder: "-----BEGIN PRIVATE KEY-----" }), h("span", { class: "help" }, "From the other machine: site settings, then Export key.")),
      h("div", { class: "row" }, h("button", { class: "btn hot", type: "submit" }, "Adopt site")), log);
    m.replaceChildren(h("div", { class: "head" }, h("div", {}, h("h1", {}, "Adopt a site"), h("p", {}, "Everything a site needs is on IPFS except its key. Paste the key and this machine takes over publishing; the previous one will notice and stop."))), form);
  };

  views.follow = async () => {
    const m = $("#main");
    const log = h("pre", { class: "log", hidden: "" });
    const form = h("form", { class: "sheet", onsubmit: async (e) => {
      e.preventDefault();
      const btn = $("button[type=submit]", form); btn.disabled = true;
      log.hidden = false; log.textContent = "Fetching the site. This can take a minute.";
      try {
        const f = await api("POST", "/v0/croptop/following", { name: $("[name=name]", form).value.trim() });
        await loadSites(); location.hash = "#/f/" + f.ipns; toast("Following " + (f.title || f.name));
      } catch (err) { log.textContent = err.message; btn.disabled = false; }
    }},
      h("label", {}, "IPNS name or ENS domain", h("input", { type: "text", name: "name", required: "", placeholder: "k51… or theirsite.eth", autofocus: "" })),
      h("div", { class: "row" }, h("button", { class: "btn hot", type: "submit" }, "Follow site")), log);
    m.replaceChildren(h("div", { class: "head" }, h("div", {}, h("h1", {}, "Follow a site"), h("p", {}, "Your node keeps a copy, serves it here, and helps host it on the network. It checks for new versions every six hours."))), form);
  };

  views.followed = async (ipns) => {
    const f = state.following.find((x) => x.ipns === ipns); if (!f) return views.home();
    const m = $("#main");
    const refresh = h("button", { class: "btn quiet", onclick: async () => {
      refresh.disabled = true; refresh.textContent = "Checking…";
      try { const r = await api("POST", `/v0/croptop/following/${ipns}/refresh`); toast(r.changed ? "Updated to the newest version" : "Already current"); await loadSites(); route(); }
      catch (err) { toast(err.message, true); refresh.disabled = false; refresh.textContent = "Check now"; }
    }}, "Check now");
    m.replaceChildren(
      h("div", { class: "head" }, h("div", {}, h("h1", {}, f.title || f.name), h("p", {}, f.name !== f.ipns ? f.name : "")),
        h("div", { class: "actions" }, h("a", { class: "btn quiet", href: `/f/${ipns}/`, target: "_blank" }, "Open"), refresh,
          h("button", { class: "btn quiet danger", onclick: async () => {
            if (!confirm(`Stop following ${f.title || f.name}? Your copy is removed.`)) return;
            await api("DELETE", `/v0/croptop/following/${ipns}`); await loadSites(); toast("Unfollowed"); location.hash = "#/";
          }}, "Unfollow"))),
      h("div", { class: "state " + (f.error ? "elsewhere" : "live") }, h("span", { class: "dot" }),
        h("span", { class: "grow" }, f.error ? `Last check failed: ${f.error}` : `Hosting version `, f.error ? null : h("code", {}, f.cid.slice(0, 20) + "…"), f.error ? "" : `, changed ${ago(when(f.changed))}, checked ${ago(when(f.checked))}`)),
      h("iframe", { class: "preview", src: `/f/${ipns}/`, title: f.title || f.name }));
  };

  const siteById = (id) => state.sites.find((s) => s.id === id);

  const stateStrip = (site) => {
    const cls = site.publishedElsewhere ? "elsewhere" : site.lastPublishedCID ? "live" : "never";
    const tld = site.croptopGateway || "sucks";
    const url = site.domain && site.domain.endsWith(".eth") ? `https://${site.domain}.${tld}/` : `https://${site.ipns}.eth.${tld}/`;
    const strip = h("div", { class: "state " + cls }, h("span", { class: "dot" }));
    if (site.publishedElsewhere) {
      strip.append(h("span", { class: "grow" }, "Another machine published this site more recently. Sync pulls its posts in and makes this machine the publisher again."),
        h("button", { class: "btn", onclick: () => sync(site) }, "Sync"));
    } else if (site.lastPublishedCID) {
      strip.append(h("span", { class: "grow" }, "Live ", h("a", { href: url, target: "_blank", rel: "noopener" }, url.replace("https://", "")), (site.ipnsSequence ? `, sequence ${site.ipnsSequence}` : "") + `, published ${ago(when(site.lastPublished))}`));
    } else {
      strip.append(h("span", { class: "grow" }, "Not published yet. Publishing renders the site, adds it to IPFS, and points ", h("code", {}, site.ipns.slice(0, 16) + "…"), " at it."));
    }
    return strip;
  };

  const publish = async (site, force) => {
    const btn = $("#publish"); if (btn) { btn.disabled = true; btn.textContent = "Publishing…"; }
    try {
      const r = await api("POST", `/v0/croptop/sites/${site.id}/publish` + (force ? "?force=true" : ""));
      toast(`Published at sequence ${r.sequence}`);
      await loadSites(); route();
    } catch (err) {
      if (/another machine/.test(err.message) && confirm(err.message + "\n\nPublish anyway and override the other machine?")) return publish(site, true);
      if (/cannot reach/.test(err.message) && confirm(err.message + "\n\nPublish anyway?")) return publish(site, true);
      toast(err.message, true); await loadSites(); route();
    }
  };

  const sync = async (site) => {
    toast("Syncing…");
    try { const r = await api("POST", `/v0/croptop/sites/${site.id}/sync`); toast(`Synced: ${r.Added} new, ${r.Updated} updated. Published at ${r.Result.Sequence}.`); await loadSites(); route(); }
    catch (err) { toast(err.message, true); }
  };

  views.site = async (id) => {
    const site = siteById(id); if (!site) return views.home();
    const m = $("#main");
    const posts = await api("GET", `/v0/planets/my/${id}/articles`);
    const grid = h("div", { class: "grid" }, h("a", { class: "tile new", href: `#/site/${id}/post/new` }, "+ New post"));
    // previews are iframes that take the mouse; a click inside one arrives as a message
    window.onmessage = (e) => { if (e.data && e.data.type === "croptop-preview-click" && e.data.post) location.hash = `#/site/${id}/post/${e.data.post}`; };
    for (const p of posts) {
      const hasMedia = (p.attachments || []).some((a) => !a.startsWith("_"));
      const cover = (p.attachments || []).find((a) => /\.(png|jpe?g|gif|webp)$/i.test(a)) || (p.videoFilename ? "_videoThumbnail.png" : "_cover.png");
      const cov = h("div", { class: "cover" + (hasMedia || cover === "_cover.png" ? "" : " blank"), style: `background-image:url("/${id}/${p.id}/${encodeURIComponent(cover)}?t=${Math.floor(Date.now()/60000)}")` });
      if (p.videoFilename) cov.append(h("span", {}, "video")); else if (p.audioFilename) cov.append(h("span", {}, "audio"));
      const hasPreview = (p.attachments || []).includes("preview.js") || /<script[^>]+type=["']croptop\/preview["']/i.test(p.content || "");
      if (hasPreview) {
        // the post draws its own tile: the site's ?preview= page, live
        cov.style.background = "var(--paper)";
        cov.append(h("iframe", { class: "tile-preview", src: `/${id}/?preview=${p.id}&t=${Math.floor(Date.now()/60000)}`, sandbox: "allow-scripts allow-same-origin", loading: "lazy", title: p.title || "preview", tabindex: "-1" }));
      }
      grid.append(h("a", { class: "tile", href: `#/site/${id}/post/${p.id}` }, cov, h("div", { class: "meta" }, p.title || "Untitled", h("small", {}, when(p.created).toLocaleDateString()))));
    }
    const strip = stateStrip(site);
    m.replaceChildren(
      h("div", { class: "head" }, h("div", {}, h("h1", {}, site.name), h("p", {}, site.about)),
        h("div", { class: "actions" },
          h("a", { class: "btn quiet", href: `/${id}/`, target: "_blank" }, "Preview"),
          h("a", { class: "btn quiet", href: `#/site/${id}/template` }, "Template"),
          h("a", { class: "btn quiet", href: `#/site/${id}/settings` }, "Settings"),
          h("button", { class: "btn hot", id: "publish", onclick: () => publish(site) }, "Publish"))),
      strip, grid);
    if (site.croptopTemplate && site.croptopTemplate.forked) {
      api("GET", `/v0/croptop/sites/${id}/template/upstream`).then((st) => {
        if (!st.changed || !st.changed.length) return;
        m.insertBefore(h("div", { class: "state elsewhere" }, h("span", { class: "dot" }),
          h("span", { class: "grow" }, `Your forked template is behind ${st.upstream === "built-in" ? "the built-in template" : st.upstream} by ${st.changed.length} file${st.changed.length === 1 ? "" : "s"}.`),
          h("a", { class: "btn quiet", href: `#/site/${id}/template` }, "Review updates")), grid);
      }).catch(() => {});
    }
    if (site.lastPublishedCID) {
      const hosts = h("span", { class: "hosts", title: "Nodes currently announcing this version" }, "looking for hosts…");
      strip.append(hosts);
      api("GET", `/v0/croptop/sites/${id}/hosts`).then((r) => {
        const others = r.peers.filter((p) => p !== r.self).length;
        hosts.textContent = r.count === 0 ? "no hosts found yet" : `hosted by ${r.count} node${r.count === 1 ? "" : "s"}` + (r.peers.includes(r.self) ? (others ? ", you and " + others + " other" + (others === 1 ? "" : "s") : ", just you") : "");
        hosts.title = r.peers.join("\n");
      }).catch(() => { hosts.textContent = ""; });
    }
  };

  views.post = async (id, pid) => {
    const site = siteById(id); if (!site) return views.home();
    const m = $("#main");
    const isNew = pid === "new";
    const post = isNew ? { title: "", content: "", tags: {}, attachments: [] } : await api("GET", `/v0/planets/my/${id}/articles/${pid}`);
    const files = h("div", { class: "files" });
    const queued = new DataTransfer(); // files dropped on a new post, uploaded on create
    const imageNames = () => (post.attachments || []).filter((a) => /\.(png|jpe?g|gif|webp|avif)$/i.test(a)).concat([...queued.files].map((f) => f.name).filter((n) => /\.(png|jpe?g|gif|webp|avif)$/i.test(n)));
    let heroSelect;
    const refreshHero = () => {
      if (!heroSelect) return;
      const cur = heroSelect.value;
      heroSelect.replaceChildren(h("option", { value: "" }, "Automatic (first image)"), ...imageNames().map((n) => h("option", { value: n }, n)));
      heroSelect.value = imageNames().includes(cur) ? cur : (post.heroImage || "");
    };
    const isWidgetFile = (n) => /\.(m?js|css|json)$/i.test(n);
    const listFiles = () => {
      files.replaceChildren();
      for (const a of post.attachments || []) {
        if (a.startsWith("_")) continue;
        files.append(h("span", { class: "file" + (isWidgetFile(a) ? " widget" : "") }, isWidgetFile(a) ? h("b", {}, "widget ") : null, a, h("button", { type: "button", title: "Remove", onclick: async () => {
          if (!confirm(`Remove ${a}?`)) return;
          await api("DELETE", `/v0/planets/my/${id}/articles/${pid}/attachments/${encodeURIComponent(a)}`);
          post.attachments = post.attachments.filter((x) => x !== a); listFiles(); refreshHero();
        }}, "×")));
      }
      for (const f of queued.files) files.append(h("span", { class: "file queued" }, f.name));
      refreshHero();
    };
    const created = when(post.created) || new Date();
    const local = new Date(created.getTime() - created.getTimezoneOffset() * 60000).toISOString().slice(0, 16);

    const content = h("textarea", { name: "content" }, post.content);
    const preview = h("div", { class: "md-preview" });
    let previewTimer;
    const renderPreview = () => {
      clearTimeout(previewTimer);
      previewTimer = setTimeout(async () => {
        if (!isNew && /<script[\s>]/i.test(content.value)) {
          // scripts only run on the real post page: show the rendered post as saved
          preview.replaceChildren(h("p", { class: "help" }, "This post runs a script. The preview shows the last saved version of the real page; save to update it."),
            h("iframe", { class: "post-frame", src: `/${id}/${pid}/?t=${Date.now()}`, sandbox: "allow-scripts allow-same-origin", title: "Post" }));
          return;
        }
        const r = await fetch("/v0/croptop/markdown", { method: "POST", body: content.value });
        preview.innerHTML = await r.text();
        for (const img of preview.querySelectorAll("img")) {
          const src = img.getAttribute("src") || "";
          if (!/^(https?:|data:|blob:)/.test(src)) {
            const q = [...queued.files].find((f) => f.name === src);
            img.src = q ? URL.createObjectURL(q) : `/${id}/${pid}/${encodeURIComponent(src)}`;
          }
        }
      }, 250);
    };
    content.addEventListener("input", renderPreview);
    const insertAtCursor = (text) => {
      const [a, b] = [content.selectionStart, content.selectionEnd];
      content.value = content.value.slice(0, a) + text + content.value.slice(b);
      content.selectionStart = content.selectionEnd = a + text.length;
      content.dispatchEvent(new Event("input"));
    };
    const addFiles = async (fileList) => {
      for (const f of fileList) {
        const isImg = /^image\//.test(f.type);
        if (isNew) {
          queued.items.add(f);
        } else {
          const fd = new FormData(); fd.append("attachments", f);
          const updated = await api("POST", `/v0/planets/my/${id}/articles/${pid}/attachments`, fd, true);
          post.attachments = updated.attachments;
        }
        if (isImg) insertAtCursor(`\n![](${f.name})\n`);
      }
      listFiles();
      if (!isNew) toast("Attachment added");
    };
    for (const ev of ["dragenter", "dragover"]) content.addEventListener(ev, (e) => { e.preventDefault(); content.classList.add("drop"); });
    content.addEventListener("dragleave", () => content.classList.remove("drop"));
    content.addEventListener("drop", (e) => { e.preventDefault(); content.classList.remove("drop"); if (e.dataTransfer.files.length) addFiles(e.dataTransfer.files); });
    content.addEventListener("paste", (e) => {
      const imgs = [...(e.clipboardData.files || [])].filter((f) => /^image\//.test(f.type));
      if (imgs.length) { e.preventDefault(); addFiles(imgs.map((f, i) => new File([f], f.name && f.name !== "image.png" ? f.name : `pasted-${Date.now()}-${i}.png`, { type: f.type }))); }
    });
    const fileInput = h("input", { type: "file", name: "attachments", multiple: "", onchange: (e) => { addFiles(e.target.files); e.target.value = ""; } });
    const snippets = {
      "Script from attachment": () => { const js = (post.attachments || []).concat([...queued.files].map((f) => f.name)).find((n) => /\.m?js$/i.test(n)); return js ? `\n<script type="module" src="${js}"></script>\n` : `\n<script type="module" src="widget.js"></script>\n`; },
      "Countdown": () => `\n<p>Opens in <span id="countdown" data-until="${new Date(Date.now() + 7 * 86400000).toISOString()}"></span>.</p>\n<script type="module">\nconst el = document.getElementById("countdown"); const t = new Date(el.dataset.until);\nconst tick = () => { const s = Math.max(0, Math.floor((t - Date.now()) / 1000)); el.textContent = \`\${Math.floor(s/86400)}d \${Math.floor(s%86400/3600)}h \${Math.floor(s%3600/60)}m \${s%60}s\`; };\ntick(); setInterval(tick, 1000);\n</script>\n`,
      "Collectors count": () => `\n<p id="collectors">Loading…</p>\n<script type="module">\ncroptop.ready(async () => { const el = document.getElementById("collectors"); const chain = croptop.chains.byKey("ethereumMainnet"); const address = croptop.chains.collectionAddress(chain);\n  if (!address) { el.textContent = "No collection on Ethereum yet."; return; }\n  const total = await croptop.wallet.call(chain.id, address, ["function totalSupply() view returns (uint256)"], "totalSupply"); el.textContent = \`\${total} collected so far\`; });\n</script>\n`,
      "Poll": () => `\n<div id="poll"><button data-opt="Yes"></button> <button data-opt="No"></button></div>\n<script type="module">\nconst box = document.getElementById("poll"), key = "poll:" + croptop.postId;\nconst render = () => { const v = JSON.parse(localStorage.getItem(key) || "{}"); box.querySelectorAll("button").forEach((b) => { b.textContent = \`\${b.dataset.opt} (\${v[b.dataset.opt] || 0})\`; }); };\nbox.querySelectorAll("button").forEach((b) => b.addEventListener("click", () => { const v = JSON.parse(localStorage.getItem(key) || "{}"); v[b.dataset.opt] = (v[b.dataset.opt] || 0) + 1; localStorage.setItem(key, JSON.stringify(v)); render(); }));\nrender();\n</script>\n`,
    };
    const palette = h("div", { class: "row palette" }, h("span", { class: "help" }, "Widgets:"), ...Object.entries(snippets).map(([name, fn]) => h("button", { type: "button", class: "btn quiet small", onclick: () => insertAtCursor(fn()) }, name)), h("a", { class: "help", href: "https://github.com/mejango/croptop/blob/main/docs/widgets.md", target: "_blank" }, "how widgets work"));

    heroSelect = h("select", { name: "heroImage" });
    listFiles();

    const form = h("form", { class: "sheet wide", onsubmit: async (e) => {
      e.preventDefault();
      const fd = new FormData();
      fd.set("title", $("[name=title]", form).value);
      fd.set("content", content.value);
      fd.set("tags", $("[name=tags]", form).value);
      fd.set("date", new Date($("[name=date]", form).value).toISOString());
      fd.set("pinned", $("[name=pinned]", form).checked ? "true" : "false");
      fd.set("includeInNavigation", $("[name=includeInNavigation]", form).checked ? "true" : "false");
      fd.set("navigationWeight", $("[name=navigationWeight]", form).value || "1");
      fd.set("heroImage", heroSelect.value);
      if (!isNew) { fd.set("slug", $("[name=slug]", form).value); fd.set("externalLink", $("[name=externalLink]", form).value); }
      for (const f of queued.files) fd.append("attachments", f);
      const btn = $("button[type=submit]", form); btn.disabled = true; btn.textContent = "Saving…";
      try {
        const url = isNew ? `/v0/planets/my/${id}/articles` : `/v0/planets/my/${id}/articles/${pid}?attachmentMode=append`;
        const saved = await api("POST", url, fd, true);
        if (isNew && heroSelect.value) { const fd2 = new FormData(); fd2.set("heroImage", heroSelect.value); await api("POST", `/v0/planets/my/${id}/articles/${saved.id}`, fd2, true); }
        toast("Saved. Publish the site to put it on IPFS."); location.hash = `#/site/${id}`;
      } catch (err) { toast(err.message, true); btn.disabled = false; btn.textContent = "Save post"; }
    }},
      h("label", {}, "Title", h("input", { type: "text", name: "title", value: post.title, autofocus: "" })),
      h("div", { class: "editor" },
        h("label", {}, "Content", content, h("span", { class: "help" }, "Markdown or HTML. Drop or paste images here; the first image becomes the cover and the NFT image. Attach a script and reference it to add a widget.")),
        h("div", {}, h("span", { class: "help" }, "Preview"), preview)),
      palette,
      h("label", {}, "Attachments", files, fileInput, h("span", { class: "help" }, "Images, video, or audio. Posts without media get a generated cover.")),
      h("div", { class: "row" },
        h("label", { class: "inline" }, h("input", { type: "checkbox", name: "pinned", checked: post.pinned ? "" : null }), " Pin to the top"),
        h("label", { class: "inline" }, h("input", { type: "checkbox", name: "includeInNavigation", checked: post.isIncludedInNavigation ? "" : null }), " Show in navigation"),
        h("label", { class: "inline" }, "weight ", h("input", { type: "number", name: "navigationWeight", value: post.navigationWeight ?? 1, min: "0", style: "width:5em" }))),
      h("label", {}, "Cover image", heroSelect, h("span", { class: "help" }, "Changing the cover rebuilds the post's NFT metadata and its CID.")),
      h("label", {}, "Tags", h("input", { type: "text", name: "tags", value: Object.keys(post.tags || {}).join(", ") }), h("span", { class: "help" }, "Comma separated. Each tag gets its own page.")),
      h("label", {}, "Date", h("input", { type: "datetime-local", name: "date", value: local })),
      isNew ? null : h("details", {}, h("summary", {}, "More"),
        h("label", {}, "Slug", h("input", { type: "text", name: "slug", value: post.slug || "" }), h("span", { class: "help" }, "Optional short path, like about. Leave blank to use the id.")),
        h("label", {}, "External link", h("input", { type: "text", name: "externalLink", value: post.externalLink || "" }))),
      h("div", { class: "row" },
        h("button", { class: "btn hot", type: "submit" }, isNew ? "Create post" : "Save post"),
        h("a", { class: "btn quiet", href: `#/site/${id}` }, "Cancel"),
        isNew ? null : h("a", { class: "btn quiet", href: `/${id}/${pid}/`, target: "_blank" }, "Preview"),
        isNew ? null : h("button", { class: "btn quiet danger", type: "button", onclick: async () => {
          if (!confirm("Delete this post? Buyers keep their NFTs; the page disappears from the site on the next publish.")) return;
          await api("DELETE", `/v0/planets/my/${id}/articles/${pid}`); toast("Post deleted"); location.hash = `#/site/${id}`;
        }}, "Delete")));
    m.replaceChildren(h("div", { class: "head" }, h("div", {}, h("h1", {}, isNew ? "New post" : "Edit post"), h("p", {}, site.name))), form);
    renderPreview();
  };

  views.settings = async (id) => {
    const site = siteById(id); if (!site) return views.home();
    const m = $("#main");
    const [tmpl, settings, gateways] = await Promise.all([template(), api("GET", `/v0/croptop/sites/${id}/settings`), api("GET", "/v0/croptop/gateways")]);
    const basic = h("fieldset", {}, h("legend", {}, "Collection"));
    const advanced = h("fieldset", {}, h("legend", {}, "Advanced"));
    const keys = Object.keys(tmpl.settings || {}).sort((a, b) => a.localeCompare(b));
    for (const k of keys) {
      const s = tmpl.settings[k];
      (s.advanced ? advanced : basic).append(h("label", {}, s.name || k, h("input", { type: "text", name: "ts:" + k, value: settings[k] ?? "" }), s.description ? h("span", { class: "help" }, s.description) : null));
    }
    const form = h("form", { class: "sheet", onsubmit: async (e) => {
      e.preventDefault();
      const btn = $("button[type=submit]", form); btn.disabled = true;
      try {
        const tags = {}; for (const t of $("[name=tags]", form).value.split(",").map((x) => x.trim()).filter(Boolean)) tags[t] = t;
        await api("PUT", `/v0/croptop/sites/${id}`, {
          name: $("[name=name]", form).value, about: $("[name=about]", form).value, domain: $("[name=domain]", form).value, tags,
          gateway: $("[name=gateway]", form).value,
          custom: { customCodeHead: $("[name=customCodeHead]", form).value, customCodeHeadEnabled: !!$("[name=customCodeHead]", form).value.trim(),
                    customCodeBodyEnd: $("[name=customCodeBodyEnd]", form).value, customCodeBodyEndEnabled: !!$("[name=customCodeBodyEnd]", form).value.trim(),
                    doNotIndex: $("[name=doNotIndex]", form).checked },
        });
        const ts = {}; for (const k of keys) ts[k] = $(`[name="ts:${k}"]`, form).value;
        await api("PUT", `/v0/croptop/sites/${id}/settings`, ts);
        const avatar = $("[name=avatar]", form).files[0];
        if (avatar) { const fd = new FormData(); fd.set("avatar", avatar); await api("POST", `/v0/planets/my/${id}`, fd, true); }
        await loadSites(); toast("Settings saved. Publish to apply them on IPFS."); location.hash = `#/site/${id}`;
      } catch (err) { toast(err.message, true); btn.disabled = false; }
    }},
      h("fieldset", {}, h("legend", {}, "Site"),
        h("label", {}, "Name", h("input", { type: "text", name: "name", value: site.name })),
        h("label", {}, "About", h("input", { type: "text", name: "about", value: site.about })),
        h("label", {}, "ENS domain", h("input", { type: "text", name: "domain", value: site.domain || "", placeholder: "yoursite.eth" }), h("span", { class: "help" }, "Point the domain's content hash at ", h("code", {}, "ipns://" + site.ipns), " once, then every publish updates it.")),
        h("label", {}, "Gateway", h("select", { name: "gateway" }, ...gateways.map((g) => h("option", { value: g.Key, selected: (site.croptopGateway || "sucks") === g.Key ? "" : null }, g.Name))), h("span", { class: "help" }, "Written into the site's absolute links. Any gateway can read the site; this one is the canonical address.")),
        h("label", {}, "Tags", h("input", { type: "text", name: "tags", value: Object.keys(site.tags || {}).join(", ") }), h("span", { class: "help" }, "Tags offered in the site's filter bar.")),
        h("label", {}, "Avatar", h("input", { type: "file", name: "avatar", accept: "image/*" })),
        h("label", { class: "row" }, h("input", { type: "checkbox", name: "doNotIndex", checked: site.doNotIndex ? "" : null, style: "width:auto" }), " Ask search engines not to index")),
      basic, advanced,
      h("fieldset", {}, h("legend", {}, "Custom code"),
        h("label", {}, "In <head>", h("textarea", { name: "customCodeHead", style: "min-height:90px" }, site.customCodeHead || "")),
        h("label", {}, "Before </body>", h("textarea", { name: "customCodeBodyEnd", style: "min-height:90px" }, site.customCodeBodyEnd || ""))),
      h("div", { class: "row" }, h("button", { class: "btn hot", type: "submit" }, "Save settings"), h("a", { class: "btn quiet", href: `#/site/${id}` }, "Cancel")));

    const keyBox = h("textarea", { class: "pem", readonly: "", hidden: "" });
    const keySection = h("fieldset", {}, h("legend", {}, "Move to another machine"),
      h("p", { style: "margin:0" }, "The key is the only thing IPFS cannot give back to you. Keep a copy somewhere safe. To publish from another computer, install Croptop there, choose Adopt a site, and paste this key with ", h("code", {}, site.ipns), "."),
      h("div", { class: "row" },
        h("button", { class: "btn", type: "button", onclick: async () => {
          try { const r = await fetch(`/v0/croptop/sites/${id}/key`); if (!r.ok) throw new Error((await r.json()).error); keyBox.value = await r.text(); keyBox.hidden = false; keyBox.select(); }
          catch (err) { toast(err.message, true); }
        }}, "Show key"),
        h("button", { class: "btn quiet", type: "button", onclick: async () => { if (!keyBox.value) return; await navigator.clipboard.writeText(keyBox.value); toast("Key copied"); } }, "Copy"),
        h("a", { class: "btn quiet", href: `/v0/croptop/sites/${id}/key`, download: `${site.name}.pem` }, "Download")),
      keyBox,
      h("div", { class: "row" }, h("button", { class: "btn quiet", type: "button", onclick: () => sync(site) }, "Sync from network"), h("span", { class: "help" }, "Pull posts published from another machine, then publish from here.")));

    const danger = h("fieldset", {}, h("legend", {}, "Danger"),
      h("div", { class: "row" }, h("button", { class: "btn quiet danger", type: "button", onclick: async () => {
        if (prompt(`Type the site name to delete it from this machine:\n${site.name}`) !== site.name) return;
        await api("DELETE", `/v0/planets/my/${id}`); await loadSites(); toast("Site deleted from this machine"); location.hash = "#/";
      }}, "Delete site from this machine"), h("span", { class: "help" }, "Copies on IPFS stay reachable until they expire. The key is deleted too, so export it first.")));

    m.replaceChildren(h("div", { class: "head" }, h("div", {}, h("h1", {}, "Settings"), h("p", {}, site.name))), form, h("div", { style: "height:24px" }), keySection, h("div", { style: "height:24px" }), danger);
  };

  views.template = async (id) => {
    const site = siteById(id); if (!site) return views.home();
    const m = $("#main");
    const [info, installed] = await Promise.all([api("GET", `/v0/croptop/sites/${id}/template`), api("GET", "/v0/croptop/templates")]);
    const forked = info.choice.forked;
    const preview = h("iframe", { class: "preview", src: `/${id}/?t=${Date.now()}`, title: "Preview" });
    const reload = () => { preview.src = `/${id}/?t=${Date.now()}`; };
    const editor = h("textarea", { class: "code", spellcheck: "false", disabled: forked ? null : "" });
    let current = "";
    const fileList = h("div", { class: "filelist" });
    const openFile = async (p) => {
      current = p;
      for (const a of fileList.querySelectorAll("a")) a.classList.toggle("current", a.dataset.p === p);
      const r = await fetch(`/v0/croptop/sites/${id}/template/file?path=${encodeURIComponent(p)}`);
      editor.value = await r.text();
    };
    for (const f of info.editable) fileList.append(h("a", { href: "#", "data-p": f, onclick: (e) => { e.preventDefault(); openFile(f); } }, f));
    const save = h("button", { class: "btn hot", disabled: forked ? null : "", onclick: async () => {
      if (!current) return;
      save.disabled = true; save.textContent = "Saving…";
      try {
        const r = await fetch(`/v0/croptop/sites/${id}/template/file?path=${encodeURIComponent(current)}`, { method: "PUT", body: editor.value });
        if (!r.ok) throw new Error((await r.json()).error);
        toast("Saved and re-rendered"); reload();
      } catch (err) { toast(err.message, true); }
      save.disabled = false; save.textContent = "Save";
    }}, "Save");
    editor.addEventListener("keydown", (e) => { if ((e.metaKey || e.ctrlKey) && e.key === "s") { e.preventDefault(); save.click(); } });

    const sourceText = info.source === "fork" ? "Forked copy, edits are yours" : info.source === "default" ? "Built-in Croptop template" : "Installed template " + info.choice.cid.slice(0, 16) + "…";
    const actions = h("div", { class: "actions" });
    const updates = h("div", { class: "updates", hidden: "" });
    if (!forked) {
      actions.append(h("button", { class: "btn", onclick: async () => {
        await api("POST", `/v0/croptop/sites/${id}/template/fork`); toast("Forked. Edit any file and save."); route();
      }}, "Fork to edit"));
    } else {
      actions.append(h("button", { class: "btn quiet danger", onclick: async () => {
        if (!confirm("Discard your edits and go back to the original template?")) return;
        await api("POST", `/v0/croptop/sites/${id}/template/reset`); toast("Reset"); route();
      }}, "Reset to original"));
      // ask what upstream (the built-in template or the ENS/IPNS source) has changed since the fork
      api("GET", `/v0/croptop/sites/${id}/template/upstream`).then((st) => {
        if (!st.changed || !st.changed.length) return;
        const take = async (paths) => {
          for (const p of paths) await api("POST", `/v0/croptop/sites/${id}/template/upstream/take?path=${encodeURIComponent(p)}`);
          toast(`Updated ${paths.length} file${paths.length === 1 ? "" : "s"}`); route();
        };
        const safe = st.changed.filter((p) => !st.edited.includes(p));
        const rows = st.changed.map((p) => h("div", { class: "row upd" },
          h("code", {}, p),
          st.edited.includes(p) ? h("span", { class: "help warn" }, "you edited this file; taking theirs replaces your version") : h("span", { class: "help" }, "not edited by you"),
          h("button", { class: "btn quiet small", onclick: () => take([p]) }, "Take theirs")));
        updates.replaceChildren(
          h("b", {}, `${st.upstream === "built-in" ? "The built-in template" : st.upstream} has ${st.changed.length} updated file${st.changed.length === 1 ? "" : "s"} since you forked.`),
          ...rows,
          h("div", { class: "row" }, safe.length ? h("button", { class: "btn", onclick: () => take(safe) }, `Take all ${safe.length} you did not edit`) : null));
        updates.hidden = false;
      }).catch(() => {});
    }
    const choose = h("select", { onchange: async (e) => {
      await api("PUT", `/v0/croptop/sites/${id}/template`, { cid: e.target.value }); toast("Template changed"); route();
    }}, ...installed.map((t) => h("option", { value: t.cid, selected: (info.choice.cid || "") === t.cid ? "" : null }, `${t.name} ${t.version}` + (t.default ? " (built in)" : ` ${t.cid.slice(0, 10)}…`))));
    const installBox = h("input", { type: "text", placeholder: "Install by CID or ENS name" });
    const installBtn = h("button", { class: "btn quiet", onclick: async () => {
      installBtn.disabled = true;
      try { const t = await api("POST", "/v0/croptop/templates", { name: installBox.value.trim() }); toast(`Installed ${t.name} ${t.version}`); route(); }
      catch (err) { toast(err.message, true); installBtn.disabled = false; }
    }}, "Install");

    m.replaceChildren(...[
      h("div", { class: "head" }, h("div", {}, h("h1", {}, "Template"), h("p", {}, `${site.name}. ${sourceText}.`)), actions),
      h("div", { class: "row", style: "margin-bottom:14px" }, h("label", { class: "inline" }, "Using ", choose), installBox, installBtn),
      updates,
      forked ? null : h("p", { class: "help" }, "Fork to edit any file. The preview updates on every save. Reset brings the original back."),
      h("div", { class: "tpl" }, fileList, h("div", {}, editor, h("div", { class: "row", style: "margin-top:8px" }, save, h("span", { class: "help" }, "Cmd/Ctrl+S saves"))), preview),
    ].filter(Boolean));
    if (info.editable.length) openFile(info.editable.includes("assets/style.css") ? "assets/style.css" : info.editable[0]);
  };

  /* ---------- router ---------- */
  const route = async () => {
    const parts = location.hash.replace(/^#\/?/, "").split("/").filter(Boolean);
    renderRail();
    try {
      if (!parts.length) return views.home();
      if (parts[0] === "new") return views.new();
      if (parts[0] === "adopt") return views.adopt();
      if (parts[0] === "follow") return views.follow();
      if (parts[0] === "f" && parts[1]) return views.followed(parts[1]);
      if (parts[0] === "site" && parts[1]) {
        if (parts[2] === "post" && parts[3]) return views.post(parts[1], parts[3]);
        if (parts[2] === "settings") return views.settings(parts[1]);
        if (parts[2] === "template") return views.template(parts[1]);
        return views.site(parts[1]);
      }
      return views.home();
    } catch (err) { toast(err.message, true); }
  };
  window.addEventListener("hashchange", route);
  (async () => { await loadStatus(); await loadSites(); route(); setInterval(loadStatus, 15000); })();
})();
