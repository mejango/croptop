# Croptop port: cross-platform publisher

Date: 2026-09-08

## Goal

Replace the macOS-only Croptop app (a scheme of Planetable/Planet) with one
`croptop` binary for Linux, macOS, and Windows that publishes the same sites,
from the same on-disk data, to the same IPNS names, and that lets a user move
between machines without breaking IPNS publishing.

## Non-goals (v1)

Aggregation of other sites, Filebase/Pinnable/Cloudflare pinning, drafts,
podcast RSS, HEIC input, video compression, full-text search, following
other sites, native desktop window, code signing.

## Constraints discovered in the Planet code

- Croptop is Planet's engine plus a grid UI. The engine is: Stencil templates
  rendered to `Public/<uuid>/`, a kubo 0.15 child process, a Vapor REST API on
  port 8086.
- The template's JavaScript is the real product. It reads `planet.json`
  (articles inlined), `<post>/article.json`, `<post>/nft.json.cid.txt`,
  `templateSettings.json`, `_cover.png`, `_videoThumbnail.png`. All Juicebox
  logic is in that JavaScript. The app writes files; the page does the rest.
- Timestamps in every JSON file are seconds since 2001-01-01 (Apple epoch).
  The template JavaScript adds 978307200. We keep this format.
- The Stencil subset used by the template is: extends, block, include, if,
  for, `|escape`, `|mdyydot`, `|formatDateC`, `.count > 0`, `!= nil`,
  `== true`, dictionary subscripts. This is Django syntax, which pongo2 renders.
- IPNS keys are named by the site UUID in kubo's keystore. Kubo keystore
  files are portable between kubo repos.
- Publishing the same key from two machines without coordinating the IPNS
  sequence number makes the network keep the older record.
- The per-post `nft.json.cid.txt` is what the onchain tier is keyed by. It
  must not change for already-published posts. Its inputs are the post's
  first attachment CID (CIDv0), title, content, created time.
- Kubo v0.43.0 has `name publish --sequence`, `name get`, `name inspect`.

## Architecture

One Go binary. Stdlib `flag` subcommands. No web framework.

```
cmd/croptop/main.go       subcommands: serve (default), import-planet, adopt,
                          sync, publish, key export, key import, version
internal/store/           Site, Post types; load/save JSON byte-compatible with
                          Planet's My/<uuid>/planet.json and Articles/<uuid>.json
internal/render/          pongo2 rendering of the template into public/<uuid>/;
                          writes planet.json, article.json, article.md, nft.json,
                          nft.json.cid.txt, templateSettings.json, _cover.png
internal/ipfs/            download pinned kubo, init/configure repo, run child
                          process, wrap CLI calls (add, name, key, id, swarm)
internal/publish/         render -> ipfs add -> sequence-aware name publish;
                          keepalive; adopt; sync
internal/server/          HTTP: /v0 API (Planet-compatible), public tree at
                          /<uuid>/, embedded UI at /
web/                      vanilla HTML/JS UI, embedded with go:embed
templates/croptop/        git submodule of Planetable/SiteTemplateCroptop,
                          embedded with go:embed; --templates overrides
```

### Data directory

`os.UserConfigDir()/croptop`:

```
config.json          listen address, passcode hash, kubo path override, ports
sites/<uuid>/        planet.json, templateSettings.json, ops.json, avatar.png,
                     favicon.ico, Articles/<uuid>.json, Articles/<uuid>/<files>
public/<uuid>/       rendered site (what gets added to IPFS)
ipfs/                kubo repo (IPFS_PATH)
kubo/ipfs[.exe]      downloaded kubo binary
```

Site JSON gains two fields, ignored by Planet: `ipnsSequence` (last sequence
this machine published) and `publishedElsewhere` (bool, set by keepalive).

Planet keeps attachment files only in `Public/<uuid>/<post>/`, with
`article.attachments` listing names. We store the originals under
`sites/<uuid>/Articles/<post-uuid>/` so the source tree is complete on its
own, and copy them into public on render. `import-planet` copies them out of
Public into that folder.

### Store

`Site` and `Post` structs with `json` tags matching Planet's CodingKeys.
Unknown keys are preserved through a `map[string]json.RawMessage` passthrough
so a file written by Planet and rewritten by us loses nothing. A custom
`AppleTime` type marshals to float seconds since 2001-01-01.

### Render

Inputs: Site, Posts, template dir, template settings. Output: `public/<uuid>/`.

Per site: `index.html`, `tags.html`, `<tag>.html` (generateTagPages),
`planet.json` (PublicPlanet with articles inline), `templateSettings.json`,
`avatar.png`, `favicon.ico`, `assets/` copied from the template.

Per post: `<uuid>/index.html` (blog.html), `<uuid>/simple.html`,
`article.json`, `article.md`, attachments, `_cover.png` when the post has no
media attachments or is audio, `_videoThumbnail.png` when ffmpeg is on PATH,
`nft.json` and `nft.json.cid.txt` when the post has at least one attachment
CID. Slug posts get a second copy at `<slug>/`.

Template context keys, matching Planet's `Template.render`: `planet`,
`article`, `articles`, `content_html`, `page_description`,
`page_description_html`, `site_navigation`, `has_avatar`, `has_podcast`,
`planet_ipns`, `assets_prefix`, `template_settings`, `user_settings`,
`article_id`, `article_type`, `article_title`, `article_summary`,
`page_title`, `build_timestamp`, `style_css_sha256`, `current_item_type`,
`social_image_url`, `og_image_url`, `custom_code_head`,
`custom_code_body_start`, `custom_code_body_end`, `tag`, `tag_key`.
`article.created.timeIntervalSince1970` is provided as a nested map value.

Derived files are generated once and cached by `ops.json` keyed the way
Planet keys them, so importing an existing site keeps every `_cover.png`,
`nft.json`, and `nft.json.cid.txt` byte for byte. Regeneration happens only
when the post's content, title, or attachments change.

CIDs: `ipfs add --only-hash --cid-version=0 <file>` for attachment CIDs and
for `nft.json.cid.txt`, exactly as Planet does. Markdown via goldmark with
GFM extensions. Cover image: 512x512 black, white text, an OFL pixel font
embedded in the binary.

### IPFS

Pinned kubo version in one constant. On first run download
`kubo_<ver>_<os>-<arch>.tar.gz|zip` from `https://dist.ipfs.tech/kubo/<ver>/`,
verify the `.sha512`, extract into `kubo/`. `config.json` may name a system
kubo instead.

Repo init mirrors Planet: API on the first free port in 5981-5991, gateway in
18181-18191, swarm in 4001-4011, `Peering.Peers` = Pinnable, bit.site,
4everland, Filebase, DoH resolvers for `.eth` and `.bit`, `Swarm.ConnMgr`
low water 10 high water 20. Daemon runs as a child with
`--enable-namesys-pubsub`; readiness is "Daemon is ready" on stdout; stopped
with `ipfs shutdown` on exit.

All kubo calls go through the CLI with `IPFS_PATH` set and `--enc=json`
where output is parsed.

### Publish

```
render(site) -> public/<uuid>/
cid = ipfs add -r -H --cid-version=1 -Q public/<uuid>
seq = networkSequence(ipns)            // name get | name inspect, 20s timeout
if seq > site.ipnsSequence and network value != site.lastPublishedCID:
    return ErrPublishedElsewhere         // UI offers sync
ipfs name publish --key=<uuid> --allow-offline --lifetime=7200h --ttl=1m
                  --sequence=max(seq, site.ipnsSequence)+1 /ipfs/<cid>
site.ipnsSequence, lastPublishedCID, lastPublished updated and saved
```

If `name get` fails (offline, first publish), sequence is
`site.ipnsSequence + 1`, and if that is 1 and the site has a
`lastPublishedCID`, publish is refused unless `--force`, so a fresh install
never resets a live name to sequence 1.

Keepalive: every 10 minutes per site, run `networkSequence`; if the network
record is ours, republish the same CID at `seq + 1`; if it is someone else's
newer record, set `publishedElsewhere = true` and stop touching that name.

### Adopt and sync

`croptop key export <site>` prints `ipfs key export -f pem-pkcs8-cleartext`.

`croptop adopt <ipns-or-ens> --key file.pem`:
1. Resolve ENS to IPNS via `ipfs resolve` (kubo's DoH `.eth` resolver).
2. `ipfs key import <uuid> file.pem`, where `uuid` comes from the fetched
   `planet.json` `id`.
3. `ipfs get <cid>` into `public/<uuid>/` after `name resolve`.
4. Rebuild `sites/<uuid>/`: `planet.json` from public `planet.json` minus the
   articles array; each article from `article.json` plus `article.md` and
   attachments; `templateSettings.json` copied; `ops.json` seeded so derived
   files are treated as done.
5. `ipnsSequence` = network sequence.

`croptop sync <site>`: same fetch into a temp dir; merge posts by id, newer
`modified` (falling back to `created`) wins, posts absent locally are added,
posts absent remotely are kept; then publish. This is last-writer-wins per
post; simultaneous edits to one post within one sync window lose the older
edit.

`croptop key import <site> file.pem` is the low-level primitive.

### Server

Default listen `127.0.0.1:8086`. `--listen 0.0.0.0:8086` refuses to start
without a passcode set (`croptop passcode set`). Auth is HTTP Basic, user
`Croptop`, so Planet's `pn` and any Planet client work unchanged. Routes:

```
GET  /                                  UI
GET  /<site-uuid>/...                   public tree (FileServer)
GET  /v0/ping /v0/id /v0/info
GET  /v0/planets/my[?archived|all]
POST /v0/planets/my                     multipart name, about, template, avatar
GET/POST/DELETE /v0/planets/my/:uuid
POST /v0/planets/my/:uuid/publish
GET  /v0/planets/my/:uuid/public        302 -> /<uuid>/
GET/POST /v0/planets/my/:uuid/articles  multipart title, date, content, attachments
GET/POST/DELETE /v0/planets/my/:uuid/articles/:id [?attachmentMode]
GET/POST /v0/planets/my/:uuid/articles/:id/attachments
DELETE /v0/planets/my/:uuid/articles/:id/attachments/:name
GET  /v0/search?q=
```
Plus UI-only routes under `/v0/croptop/`: template settings get/set, key
export, adopt, sync, status (kubo peers, keepalive, publishedElsewhere).

### UI

One `index.html` with inline JS, no build step. Screens: site list; post grid
for a site with an iframe preview of `/<uuid>/`; post editor (title,
markdown textarea, attachment upload, tags, date); site settings (name,
about, domain, avatar, template settings including the collection address
form that MintSettings.swift provided, custom code); publish button with
progress and the resulting CID and gateway link; key export with a copy
button; adopt form. Mobile-width layout works because the same page is the
phone client.

### Import from Planet

`croptop import-planet [container]` defaults to
`~/Library/Containers/xyz.planetable.Lite/Data`. Copies `Documents/Planet/My`
into `sites/`, `Documents/Planet/Public` into `public/`, moves each post's
attachments from public into `sites/<uuid>/Articles/<post>/`, and copies
`Library/Application Support/ipfs/keystore/key_*` into our repo's keystore
(file names are `key_` + lowercase base32 of the key name, which is the site
UUID). Works while the Mac app is running. Refuses to overwrite an existing
site unless `--force`.

### Error handling

Every kubo call returns stderr in the error. Publish errors are shown in the
UI verbatim. The daemon restarts once if it exits unexpectedly; after that
the UI shows "IPFS stopped" with the last stderr lines. Download failures
name the URL and the expected sha512.

### Testing

- `store`: load every `planet.json` and article file from a fixture copied
  from the user's real library, save, and diff bytes (allowing key order).
- `render`: render `templates/croptop/dev/fixture` and assert the files the
  template JavaScript fetches exist and `planet.json` parses with articles.
- `publish`: fake kubo (a shell script on PATH) that records calls; assert
  sequence arithmetic, the ErrPublishedElsewhere path, and the sequence-1
  refusal.
- `server`: httptest against the /v0 routes with a temp data dir.
- CI: `go test ./...`, `go vet`, cross-compile all six targets.

### Release

GitHub Actions on tag: goreleaser builds darwin/linux/windows x amd64/arm64,
attaches archives and checksums. README documents install per OS and the
macOS quarantine command.
