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

  const state = { sites: [], status: null, template: null };

  const loadSites = async () => { state.sites = await api("GET", "/v0/planets/my?all=true"); renderRail(); };
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

  const siteById = (id) => state.sites.find((s) => s.id === id);

  const stateStrip = (site) => {
    const cls = site.publishedElsewhere ? "elsewhere" : site.lastPublishedCID ? "live" : "never";
    const url = site.domain && site.domain.endsWith(".eth") ? `https://${site.domain}.sucks/` : `https://${site.ipns}.eth.sucks/`;
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
    for (const p of posts) {
      const hasMedia = (p.attachments || []).some((a) => !a.startsWith("_"));
      const cover = (p.attachments || []).find((a) => /\.(png|jpe?g|gif|webp)$/i.test(a)) || (p.videoFilename ? "_videoThumbnail.png" : "_cover.png");
      const cov = h("div", { class: "cover" + (hasMedia || cover === "_cover.png" ? "" : " blank"), style: `background-image:url("/${id}/${p.id}/${encodeURIComponent(cover)}?t=${Math.floor(Date.now()/60000)}")` });
      if (p.videoFilename) cov.append(h("span", {}, "video")); else if (p.audioFilename) cov.append(h("span", {}, "audio"));
      grid.append(h("a", { class: "tile", href: `#/site/${id}/post/${p.id}` }, cov, h("div", { class: "meta" }, p.title || "Untitled", h("small", {}, when(p.created).toLocaleDateString()))));
    }
    m.replaceChildren(
      h("div", { class: "head" }, h("div", {}, h("h1", {}, site.name), h("p", {}, site.about)),
        h("div", { class: "actions" },
          h("a", { class: "btn quiet", href: `/${id}/`, target: "_blank" }, "Preview"),
          h("a", { class: "btn quiet", href: `#/site/${id}/settings` }, "Settings"),
          h("button", { class: "btn hot", id: "publish", onclick: () => publish(site) }, "Publish"))),
      stateStrip(site), grid);
  };

  views.post = async (id, pid) => {
    const site = siteById(id); if (!site) return views.home();
    const m = $("#main");
    const isNew = pid === "new";
    const post = isNew ? { title: "", content: "", tags: {}, attachments: [] } : await api("GET", `/v0/planets/my/${id}/articles/${pid}`);
    const files = h("div", { class: "files" });
    const listFiles = () => {
      files.replaceChildren();
      for (const a of post.attachments || []) {
        if (a.startsWith("_")) continue;
        files.append(h("span", { class: "file" }, a, h("button", { type: "button", title: "Remove", onclick: async () => {
          if (!confirm(`Remove ${a}?`)) return;
          await api("DELETE", `/v0/planets/my/${id}/articles/${pid}/attachments/${encodeURIComponent(a)}`);
          post.attachments = post.attachments.filter((x) => x !== a); listFiles();
        }}, "×")));
      }
    };
    listFiles();
    const created = when(post.created) || new Date();
    const local = new Date(created.getTime() - created.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
    const form = h("form", { class: "sheet", onsubmit: async (e) => {
      e.preventDefault();
      const fd = new FormData();
      fd.set("title", $("[name=title]", form).value);
      fd.set("content", $("[name=content]", form).value);
      fd.set("tags", $("[name=tags]", form).value);
      fd.set("date", new Date($("[name=date]", form).value).toISOString());
      if (!isNew) { fd.set("slug", $("[name=slug]", form).value); fd.set("externalLink", $("[name=externalLink]", form).value); }
      for (const f of $("[name=attachments]", form).files) fd.append("attachments", f);
      const btn = $("button[type=submit]", form); btn.disabled = true; btn.textContent = "Saving…";
      try {
        const url = isNew ? `/v0/planets/my/${id}/articles` : `/v0/planets/my/${id}/articles/${pid}?attachmentMode=append`;
        await api("POST", url, fd, true);
        toast("Saved. Publish the site to put it on IPFS."); location.hash = `#/site/${id}`;
      } catch (err) { toast(err.message, true); btn.disabled = false; btn.textContent = "Save post"; }
    }},
      h("label", {}, "Title", h("input", { type: "text", name: "title", value: post.title, autofocus: "" })),
      h("label", {}, "Content", h("textarea", { name: "content" }, post.content), h("span", { class: "help" }, "Markdown. Reference attachments by file name, like ![](photo.jpg).")),
      h("label", {}, "Attachments", files, h("input", { type: "file", name: "attachments", multiple: "" }), h("span", { class: "help" }, "Images, video, or audio. The first image becomes the cover and the NFT image. Posts without media get a generated cover.")),
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
  };

  views.settings = async (id) => {
    const site = siteById(id); if (!site) return views.home();
    const m = $("#main");
    const [tmpl, settings] = await Promise.all([template(), api("GET", `/v0/croptop/sites/${id}/settings`)]);
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

  /* ---------- router ---------- */
  const route = async () => {
    const parts = location.hash.replace(/^#\/?/, "").split("/").filter(Boolean);
    renderRail();
    try {
      if (!parts.length) return views.home();
      if (parts[0] === "new") return views.new();
      if (parts[0] === "adopt") return views.adopt();
      if (parts[0] === "site" && parts[1]) {
        if (parts[2] === "post" && parts[3]) return views.post(parts[1], parts[3]);
        if (parts[2] === "settings") return views.settings(parts[1]);
        return views.site(parts[1]);
      }
      return views.home();
    } catch (err) { toast(err.message, true); }
  };
  window.addEventListener("hashchange", route);
  (async () => { await loadStatus(); await loadSites(); route(); setInterval(loadStatus, 15000); })();
})();
