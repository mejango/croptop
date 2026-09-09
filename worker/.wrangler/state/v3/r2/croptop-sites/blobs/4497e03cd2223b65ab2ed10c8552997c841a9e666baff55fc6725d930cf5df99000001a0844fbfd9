/*
 * croptop.js: the runtime widgets talk to.
 *
 * A widget is a script attached to a post and referenced from the post's HTML:
 *   <script type="module" src="widget.js"></script>
 * Attachments are copied into the post's folder, so relative URLs work on
 * IPFS, on every gateway, and in the console preview.
 *
 * Everything here wraps functions that already exist in chains.js, utils.js,
 * and tx.js. Load order in modules/head.html puts this file last.
 */
window.croptop = (() => {
  const prefixMeta = document.querySelector('meta[name="croptop-prefix"]');
  const prefix = prefixMeta ? prefixMeta.content : "./";
  const postMeta = document.querySelector('meta[name="croptop-post"]');
  const postId = postMeta ? postMeta.content : null;

  let siteCache = null, postCache = null, envReady = null;
  const fetchJSON = async (url) => { const r = await fetch(url, { cache: "no-store" }); if (!r.ok) throw new Error(`${url}: ${r.status}`); return r.json(); };

  const loadEnv = () => {
    if (!envReady) envReady = (async () => {
      if (typeof env === "object" && Object.keys(env).length) return env;
      try { Object.assign(env, await fetchJSON(prefix + "templateSettings.json")); } catch (e) { /* no settings yet */ }
      return env;
    })();
    return envReady;
  };

  const gatewayTld = () => {
    const m = location.hostname.match(/\.eth\.(sucks|shop|limo)$/);
    return m ? m[1] : "sucks";
  };

  const api = {
    prefix,
    postId,
    /** planet.json as an object (cached). */
    async site() { return siteCache || (siteCache = await fetchJSON(prefix + "planet.json")); },
    /** article.json of the current post, or null on the feed. */
    async post() { if (!postId) return null; return postCache || (postCache = await fetchJSON(`${prefix}${postId}/article.json`)); },
    /** templateSettings.json values, the same `env` the template uses. */
    async env() { return loadEnv(); },
    /** The chain table and helpers from chains.js. */
    chains: { list: () => CHAINS, byId: chainById, byKey: chainByKey, rpc: chainRpc, collectionAddress: collectionAddressFor },
    wallet: {
      /** Ask the browser wallet to connect and switch to chainId. */
      async connect(chainId) { await loadEnv(); await ensureWalletOnChain(chainId); return api.wallet.address(); },
      async address() { const s = await getSigner(); return s.getAddress(); },
      provider: (chainId) => getProvider(chainId),
      signer: (chainId) => getSigner(chainId),
      /** Read-only contract call on chainId. */
      call: (chainId, address, abi, fn, params = []) => view(chainId, address, abi, fn, params),
      /** Send a transaction with the connected wallet. */
      send: (address, abi, fn, params = [], value = 0) => sign(address, abi, fn, params, value),
      /** Croptop's ERC-2771 forwarding through Relayr, as the buy button uses. */
      forward: (...args) => handleTransact(...args),
    },
    ipfs: {
      /** Public URL of a CID on the gateway this page came from. */
      url: (cid, path = "") => `https://${cid}.eth.${gatewayTld()}/${path.replace(/^\//, "")}`,
      fetchJSON: (cid, path = "") => fetchJSON(api.ipfs.url(cid, path)),
      /** Build the ipfs:// URI encoding the contracts use, from utils.js. */
      encodeUri: (cid) => encodeIPFSUri(cid),
    },
    /**
     * Scripts inserted with innerHTML never run. The template calls this after
     * it renders a post's content: each <script> is recreated so it executes,
     * and relative src values are pointed at the post's own folder so the
     * same post works on its page, in the feed's modal, and on any gateway.
     */
    activate(container, id) {
      if (id) { api.postId = id; postCache = null; }
      for (const old of Array.from(container.querySelectorAll("script"))) {
        const s = document.createElement("script");
        for (const a of old.attributes) s.setAttribute(a.name, a.value);
        const src = s.getAttribute("src");
        if (src && !/^([a-z]+:|\/|\.\.?\/)/i.test(src) && api.postId) s.setAttribute("src", `${prefix}${api.postId}/${src}`);
        s.textContent = old.textContent;
        old.replaceWith(s);
      }
    },
    /**
     * A post can ship its own feed preview: attach preview.js exporting
     *   export default function (el, { post, site, env, size, base }) {}
     * The feed calls it inside the post's frame ("frame") or list row ("row")
     * instead of showing the first lines of text. Keep it light: it runs for
     * every such post on the page at once. Clicks on it open the post.
     */
    /** True when a post carries a preview: an attached preview.js or an inline
     *  <script type="croptop/preview"> in its content. */
    hasPreview(post) {
      return !!post && ((post.attachments || []).includes("preview.js") || /<script[^>]+type=["']croptop\/preview["']/i.test(post.content || ""));
    },
    async preview(el, id, post, size) {
      try {
        const base = `${prefix}${id}/`;
        let m;
        const inline = (post && post.content || "").match(/<script[^>]+type=["']croptop\/preview["'][^>]*>([\s\S]*?)<\/script>/i);
        if (inline) {
          // the code travels with planet.json, so no extra fetch
          m = await import(URL.createObjectURL(new Blob([inline[1]], { type: "text/javascript" })));
        } else {
          // import() in a classic script resolves against this file, not the page
          m = await import(new URL(`${base}preview.js`, document.baseURI).href);
        }
        const site = await api.site();
        await loadEnv();
        (m.default || m.preview)(el, { post, site, env, size, base, postId: id });
      } catch (e) {
        console.warn("preview for " + id + " failed", e);
        el.textContent = (post && post.content || "").replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim().slice(0, 200);
      }
    },
    /** Run fn once the site (and post, on post pages) are loaded. */
    ready(fn) {
      const run = async () => { const site = await api.site(); const post = await api.post(); await loadEnv(); fn({ site, post, env }); };
      if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", run); else run();
    },
  };
  return api;
})();
