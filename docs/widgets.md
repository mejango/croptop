# Widgets

A Croptop post is HTML. Anything you attach to a post is copied into the
post's folder and addressed by content. Put those two facts together and
every post can carry its own program: attach a script, reference it from
the post, and it runs on the published page, on every gateway, and in the
console preview. No registry, no build step, nothing to install.

## The pattern

1. Write `widget.js`.
2. Attach it to the post (drop it on the editor).
3. In the post content, add

```html
<div id="my-widget"></div>
<script type="module" src="widget.js"></script>
```

Relative URLs resolve inside the post's folder, so `widget.js`, a
`widget.css`, and any data file you attach are all reachable by name.

## The runtime

The template loads `assets/scripts/croptop.js` last, which defines
`window.croptop`:

| call | gives you |
|------|-----------|
| `croptop.ready(fn)` | runs `fn({site, post, env})` once the page's data is loaded |
| `await croptop.site()` | `planet.json`: name, about, every article |
| `await croptop.post()` | `article.json` of this post, or `null` on the feed |
| `await croptop.env()` | the site's template settings (collection addresses, RPCs, colors) |
| `croptop.chains.list()`, `.byId(id)`, `.byKey(key)`, `.rpc(chain)`, `.collectionAddress(chain)` | the chain table |
| `await croptop.wallet.connect(chainId)` | asks the browser wallet to connect and switch chain, returns the address |
| `await croptop.wallet.call(chainId, address, abi, fn, params)` | read a contract |
| `await croptop.wallet.send(address, abi, fn, params, value)` | send a transaction with the connected wallet |
| `croptop.wallet.forward(...)` | Croptop's ERC-2771 forwarding through Relayr, what the buy button uses |
| `croptop.ipfs.url(cid, path)` | a URL for a CID on the gateway the page came from |
| `await croptop.ipfs.fetchJSON(cid, path)` | fetch and parse JSON from IPFS |
| `croptop.prefix`, `croptop.postId` | `./` on the feed, `../` on a post; the post id |

Widgets run with the same power as the template's own JavaScript, in the
reader's browser, on a static page. That is the existing trust model of
raw HTML in posts.

Six finished examples, live on [follo.eth.sucks](https://follo.eth.sucks), are
in [`examples/widgets/`](../examples/widgets/).

## Three complete widgets

### Countdown

`countdown.js`
```js
const el = document.getElementById("countdown");
const target = new Date(el.dataset.until);
const tick = () => {
  const s = Math.max(0, Math.floor((target - Date.now()) / 1000));
  el.textContent = `${Math.floor(s / 86400)}d ${Math.floor(s % 86400 / 3600)}h ${Math.floor(s % 3600 / 60)}m ${s % 60}s`;
};
tick(); setInterval(tick, 1000);
```
Post content:
```html
<p>Drop opens in <span id="countdown" data-until="2026-10-01T18:00:00Z"></span>.</p>
<script type="module" src="countdown.js"></script>
```

### Who else collected this

`collectors.js`
```js
croptop.ready(async ({ post, env }) => {
  const el = document.getElementById("collectors");
  const chain = croptop.chains.byKey("ethereumMainnet");
  const address = croptop.chains.collectionAddress(chain);
  if (!address) { el.textContent = "No collection on Ethereum yet."; return; }
  const total = await croptop.wallet.call(chain.id, address, ["function totalSupply() view returns (uint256)"], "totalSupply");
  el.textContent = `${total} collected so far`;
});
```
Post content:
```html
<p id="collectors">Loading…</p>
<script type="module" src="collectors.js"></script>
```

### A poll, votes kept on the reader's device

`poll.js`
```js
const box = document.getElementById("poll");
const key = "poll:" + croptop.postId;
const render = () => {
  const votes = JSON.parse(localStorage.getItem(key) || "{}");
  box.querySelectorAll("button").forEach((b) => { b.textContent = `${b.dataset.opt} (${votes[b.dataset.opt] || 0})`; });
};
box.querySelectorAll("button").forEach((b) => b.addEventListener("click", () => {
  const votes = JSON.parse(localStorage.getItem(key) || "{}");
  votes[b.dataset.opt] = (votes[b.dataset.opt] || 0) + 1;
  localStorage.setItem(key, JSON.stringify(votes)); render();
}));
render();
```
Post content:
```html
<div id="poll"><button data-opt="Yes"></button> <button data-opt="No"></button></div>
<script type="module" src="poll.js"></script>
```

## Previews in the feed

The feed shows the first lines of a post's text in its frame. A widget post
can replace that with something alive. Attach `preview.js`:

```js
export default function (el, { post, site, env, size, base }) {
  // el: the frame (size "frame") or the list row's thumbnail (size "row").
  // Draw into el; it is positioned, clipped, and its clicks open the post.
  el.textContent = post.title;
}
```

The template mounts it for every such post on the page at once, so keep it
light: one timer, small fetches, stop when `el.isConnected` is false. The
six examples in `examples/widgets/*.preview.js` show live gas, self-balancing
budget bars, the coin flow, a typing wallet address, the site timeline, and
a countdown.

## Sharing widgets

A widget lives in a post, so a site whose posts are widgets is a registry:
the source as attachments, the body as documentation with a live demo,
`nft.json` as provenance. To reuse one, download its attachments from the
post's folder and attach them to your own post.
