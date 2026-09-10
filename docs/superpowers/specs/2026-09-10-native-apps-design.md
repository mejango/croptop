# Native Croptop apps: macOS, Windows, Linux

Decision (2026-09-10): the desktop apps are fully native on each platform. No
web view. One design, written once in this document and in `docs/design/`,
enacted three times. The user iterates on the design in one place; every
change is then applied to all three apps before it ships.

## Architecture

```
SwiftUI (macOS)  \
WinUI 3 (Windows) -->  local HTTP API  -->  croptop (Go): store, render, node, publish
GTK4 (Linux)     /     127.0.0.1:8086
```

- The Go binary is the engine. It owns the data, the IPFS node, rendering,
  publishing, following, and the crop.top push. It ships inside each app.
- Each app is a native client of the console API (`/v0/...`, see "API
  contract"). No app touches the data directory directly.
- The web console stays as the headless and Linux-server UI, and as the
  reference implementation of behaviour, but the apps do not embed it.
- Node lifecycle is the app's: start `croptop serve --no-open` on launch
  (after `croptop service uninstall`, so a login service never fights the app
  for the port), keep it while the app runs, `POST /v0/croptop/quit` and
  terminate it on quit. Files handed to the app (Dock/Explorer/Open With, drops
  onto the window) go to `POST /v0/croptop/quick` and open the quick post
  screen.

## Design source of truth

`docs/design/tokens.md` holds the tokens; this section holds the screens.
The apps read like the same product: same names for everything, same order,
same copy. Platform conventions win only for chrome that the OS owns
(traffic lights vs. caption buttons, menu bar vs. title bar menu, Dock vs.
tray vs. dash).

Tokens (from the console, the brand):

| token | value | use |
|---|---|---|
| ink | #171717 | text, 2 px rules and borders |
| paper | #FFFFFF | background |
| hot | #F056C1 | the one accent: current item, focus ring, primary action |
| live | #3BB273 | node up, published |
| attention | #EFAB1D | update available, unpublished changes |
| rule | #E2E2E2 | quiet borders |
| muted | #ADADAF | secondary text |
| pixel | Capsules (bundled), fallback monospace | headings, brand, buttons, rail headings |
| body | Simplon (bundled), fallback system sans | everything else, 16 px, line 1.45 |
| rail | 220 pt | sidebar width |
| spacing | 4, 8, 12, 16, 22 | the only gaps used |

Screens, phase 1 (parity with the console home, site and posting flows):

1. **Window**: sidebar (rail) left, content right. Rail: brand "Croptop" in
   pixel 22; Feed (a boxed button, current when selected); "Sites" list (name,
   muted second line: ENS or truncated IPNS, hot 4 px left bar when current);
   "Following" heading and list; bottom: "New site", "Follow", node line ("N
   peers", live dot) and the update banner when `status.update` is true.
2. **Feed**: cards newest first from `GET /v0/croptop/feed`: who (avatar,
   site name, date), title, plain text excerpt, hero image when `hasHero`,
   preview frame when `hasPreview` (phase 2). "Check for new posts" refreshes
   followed sites. Empty state: suggestions croptop.eth, follo.eth, jango.eth.
3. **Site**: header (name, about, URL chip, Publish button, unpublished
   changes in attention), tag filter bar, post tiles grid (hero or cover, title,
   date, tags) and the "+ New post" tile. Right-click on a tile: Edit, Delete,
   Copy link.
4. **Post editor**: title, content (markdown, monospace), attachments strip
   (drop files here, thumbnails, set as hero, remove), tags, page/post
   toggle, include in navigation, pin. Save, Save and publish, Cancel, Delete.
   `POST /v0/planets/my/{id}/articles` (new) or `/articles/{post}` (edit) as
   multipart.
5. **Quick post**: media previews (image, video, audio), Post to (site menu,
   sized to content), Title, Words, Tags, Post / Post and publish / Discard.
   Command Enter posts, Command Shift Enter posts and publishes.
6. **Publish**: button state (publishing spinner, then live), errors inline.
   Result URL chips (crop.top, eth.sucks) open in the browser.
7. **New site** and **Follow** sheets (name/about; ENS or IPNS).

Phase 2: site settings (about, avatar, host, name claim, key export/import),
node status pane, search, drafts, following management (refresh, unfollow),
per-site reader for followed sites, previews in the feed. Phase 3: template
editor, adopt, templates by CID.

## API contract (already served by `croptop serve`)

- `GET /v0/ping`, `GET /v0/croptop/status` (version, latest, update, ipfs.peers, listen)
- `GET /v0/planets/my`, `POST /v0/planets/my`, `GET|POST|DELETE /v0/planets/my/{id}`
- `GET|POST /v0/planets/my/{id}/articles`, `GET|POST|DELETE .../articles/{post}`, attachments under it
- `POST /v0/croptop/sites/{id}/publish`, `GET .../url`, `GET|PUT .../settings`, `GET|POST .../key`, `POST .../name`
- `GET /v0/croptop/feed`, `GET|POST /v0/croptop/following`, `DELETE|POST refresh`
- `POST|GET|DELETE /v0/croptop/quick`, `POST /v0/croptop/markdown` (preview)
- `POST /v0/croptop/update`, `POST /v0/croptop/quit`, `POST /v0/croptop/adopt`
- Served files: `/{site}/{path}` (a site's rendered output), `/f/{ipns}/{path}` (a followed site), `/v0/croptop/quick/{id}/{name}`

JSON shapes are Planet's (`docs/format.md`): sites `{id,name,about,ipns,created,updated,lastPublished,archived,tags}`, posts `{id,title,content,created,articleType,link,attachments,tags,heroImage,pinned,isIncludedInNavigation,...}`. Dates are seconds since 2001-01-01 (Apple time).

## Per platform

| | macOS | Windows | Linux |
|---|---|---|---|
| UI | SwiftUI, macOS 13+ | WinUI 3 (C#, .NET 8, Windows App SDK, unpackaged, self-contained) | GTK4 + libadwaita (C) |
| source | `apps/macos/` (Swift package) | `apps/windows/` | `apps/linux/` |
| build | `swift build -c release --arch arm64 --arch x86_64`, bundle by `installer/macos.sh` | `dotnet publish -r win-x64|win-arm64`, Inno Setup | `meson` or a Makefile; `.deb`/`.rpm` via goreleaser nfpms contents, plus a tar.gz |
| node binary | `Croptop.app/Contents/Resources/croptop` | next to `Croptop.exe` | next to `croptop-app` or on PATH |
| files in | `application(_:open:)`, drop on window | command line, drop on window | GApplication open, drop on window |
| fonts | bundled TTF, registered at launch | bundled TTF | bundled TTF via fontconfig app dir |

## Testing

- macOS: build and run locally; screenshots with `screencapture -l`.
- Linux: build in Docker (ubuntu:24.04 with libadwaita-1-dev); run under the
  GTK broadway backend and screenshot with headless Chrome.
- Windows: CI build only (no Windows machine here); ship behind the existing
  Inno Setup installer; verify by the user.
- Every screen change is applied to all three before a release is tagged.

## Phasing

1. macOS phase 1 (this week), replacing the WKWebView window.
2. Linux phase 1.
3. Windows phase 1.
4. Phase 2 on all three, screen by screen, always in that order.
