# Phase 1: Trust

Date: 2026-09-08. Parent: 2026-09-08-croptop-roadmap.md.

## Goal

A reader, a subscriber, or a collector must not be able to tell whether a
site was published by the Mac app or by croptop. And the author must be able
to reach every post field that matters from the console.

## Scope

1. **RSS output.** Render `rss.xml` from Planet's `RSS.xml` template with
   the same context (`planet` with `articles`, `root_prefix`, `podcast`
   false, filters `md2html`, `rfc822`, `absoluteImageURL`, `hhmmss`). The
   template is copied into `internal/render/planet/RSS.xml` with the Planet
   license note. `podcast.xml` is not generated (audio duration needs media
   decoding; noted in README).
2. **Post controls.** Editor gets: pin (bool, writes `pinned` as the current
   time or removes it), include in navigation plus weight, hero image picker
   listing image attachments (writes `heroImage`, clears to auto). API:
   `POST /v0/planets/my/{id}/articles/{post}` accepts `pinned=true|false`,
   `includeInNavigation=true|false`, `navigationWeight=<int>`,
   `heroImage=<name or empty>`. Editing any of these clears the generated
   `nft.json` only when the hero changes.
3. **Editor.** Live markdown preview from `POST /v0/croptop/markdown`
   (goldmark, same renderer as publish), debounced. Drag-and-drop or paste
   of images: existing posts upload immediately through the attachments
   route and insert `![](name)` at the cursor; new posts queue files into
   the form's file input via `DataTransfer` and insert the same reference.
4. **Gateways.** `internal/gateway` holds the table: `sucks` (eth.sucks),
   `shop` (eth.shop), `limo` (eth.limo). Site setting `croptopGateway`
   (Croptop addition, passthrough for Planet). `render.SiteURL` uses it:
   `https://<domain>.<tld>/` for `.eth` domains, else
   `https://<ipns>.eth.<tld>/`. Settings UI offers the choice. The console
   link and preview use the site's choice; `Gateway.URLs(site)` returns all
   candidates in order for fallbacks.
5. **Format document.** `docs/format.md` describes every file and key in a
   published tree and in the source tree, the Apple epoch rule, the
   `nft.json` CID rule, and what adopt needs.
6. **Tests to run, not write.** Two-machine takeover on this Mac with a
   second data dir on FOLLO; Linux run of the release binary in Docker
   adopting FOLLO by key; phone access.

## Out of scope

Drafts, aggregation, pinning services, podcast feed, audio duration.

## Testing

- `render`: `rss.xml` exists, parses as XML, item count equals feed
  articles, first item link uses the site URL, description contains the
  rendered HTML with absolute image URLs.
- `server`: modify post with the new fields and read them back.
- `gateway`: table lookups and `SiteURL` for each gateway with and without
  a `.eth` domain.
