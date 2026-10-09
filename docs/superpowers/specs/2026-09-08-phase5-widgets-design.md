# Phase 5: Widgets

Date: 2026-09-08. Parent: 2026-09-08-croptop-roadmap.md.

## Goal

Croptop posts already accept raw HTML and every attachment is
content-addressed and copied into the post's folder. Name that as the
widget system: an advanced author writes a script, attaches it to a post,
references it from the post's HTML, and it runs on the published page with
a small, stable API for site, post, settings, wallet, and IPFS. No registry
and no build step are required. A registry can then be a Croptop site.

## Runtime

`assets/scripts/croptop.js` in the template (upstream change to
SiteTemplateCroptop, submodule bump) defines `window.croptop`:

```
croptop.site      planet.json as an object (fetched once, cached)
croptop.post      article.json of the current post, or null on the feed
croptop.env       templateSettings.json values (already `env` today)
croptop.chains    the chain table from chains.js
croptop.wallet    { connect(), address(), provider(), sendTx(tx) }  using tx.js's forwarding
croptop.ipfs      { url(cid, path), fetchJSON(cid, path), cidv0(bytes) }
croptop.ready(fn) runs fn after site and post are loaded
```

Everything wraps functions that exist in `utils.js` and `tx.js`; the
runtime is a naming layer, not new behavior. It is loaded by
`modules/head.html` after `tx.js`. Widgets are ES modules or plain scripts
attached to the post and referenced as `<script type="module"
src="widget.js"></script>` inside the post's content. Because attachments
are copied into `<post>/`, relative URLs resolve on IPFS, on every gateway,
and in the console preview.

## Console

- The editor's attachment area accepts `.js`, `.mjs`, `.css`, and `.json`
  files, listed with a "widget" badge instead of an image preview.
- A *Widgets* palette in the editor lists snippets: "Script from attachment"
  (picks an attached `.js` and inserts the tag), "Juicebox pay button"
  (`<button data-croptop-pay>` handled by the runtime), "Countdown",
  "Poll" (client-side, stores votes in the viewer's localStorage). Each is
  a static snippet the author can edit.
- The preview pane renders the post inside a sandboxed iframe of the
  actual post page (`/<site>/<post>/`) instead of the bare markdown when
  the content contains a `<script>` tag, so widgets run during editing.

## Registry

`docs/widgets.md` documents the runtime and the attach-and-reference
pattern with three complete examples. A registry site is a normal Croptop
site whose posts are widgets: the source as attachments, the post body as
documentation with a live demo, `nft.json` as provenance. Installing a
widget is downloading its attachments and attaching them to your post;
the console's palette gets an "Install from a site" entry that fetches a
post's attachments by site name and post id.

## Security

Widgets run with the same power as the template's own JavaScript, on a
static page, in the viewer's browser. That is the existing trust model of
raw HTML in posts. The console shows a one-line notice on posts that
contain scripts. No sandboxing beyond the browser's; a template author who
wants isolation can wrap post content in a sandboxed iframe.

## Tests

- Template submodule renders with `croptop.js` present and the runtime
  defined (headless browser check in the acceptance run).
- Editor accepts a `.js` attachment, inserts the script tag, and the
  rendered post page contains it.
- A post with a widget publishes and the widget executes on the gateway.

## Out of scope

A widget package manager, server-side rendering of widgets, sandboxing,
payments for widgets beyond what the post's own NFT already provides.
