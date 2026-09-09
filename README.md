# Croptop

Publish [Croptop](https://croptop.eth.sucks) sites to IPFS from Linux, macOS,
or Windows. One binary with an IPFS node built in: it renders your posts
with the Croptop template, adds them to IPFS, and keeps your site's IPNS
name pointing at the latest version. The console runs in your browser,
so the same thing works from a phone on your network.

This is a port of the Croptop scheme of the [Planet](https://github.com/Planetable/Planet)
Mac app. It reads and writes the same files, so you can move a library over
from the Mac app and back.

## Install

One line, any platform:

```sh
curl -fsSL https://crop.top/install.sh | sh        # macOS and Linux
```

```powershell
irm https://crop.top/install.ps1 | iex             # Windows
```

Or pick what fits:

- **macOS app**: download `Croptop.dmg` from the [latest release](https://github.com/mejango/croptop/releases/latest), drag Croptop to Applications. It is not notarized yet: the first launch is blocked, then System Settings, Privacy & Security shows an Open Anyway button for it.
- **Homebrew**: `brew install mejango/tap/croptop`
- **Windows installer**: `croptop-setup.exe` from the latest release. Windows will show a SmartScreen notice because the installer is unsigned; choose More info, Run anyway.
- **Debian, Ubuntu**: the `.deb` from the latest release. **Fedora**: the `.rpm`.
- **Plain binary**: the `.tar.gz` or `.zip` for your platform.

Croptop updates itself: the console shows a banner when a new release is out, and `croptop update` does the same from a terminal.

## First run

```
croptop
```

The first start creates the node's identity and blockstore and opens
`http://127.0.0.1:8086`. Data lives in:

| OS      | Data directory                             |
|---------|--------------------------------------------|
| macOS   | `~/Library/Application Support/croptop`    |
| Linux   | `~/.config/croptop`                        |
| Windows | `%AppData%\croptop`                        |

Pass `--data <dir>` to use another location. Keep the console running while
you want the site served from your machine; every ten minutes it renews the
IPNS record.

## Coming from the Mac app

With the Croptop Mac app installed on the same Mac:

```
croptop import-planet
```

This copies every site, its posts and files, and the IPNS keys out of the
app's container. It only reads, so the Mac app can keep running. Generated
files such as `nft.json` are kept byte for byte, so posts already sold as
NFTs keep their onchain metadata CIDs.

The first publish from `croptop` reads the current sequence number of the
IPNS record from the network and publishes above it, so the Mac app's
copy is superseded cleanly. Quit the Mac app afterwards, or it will notice
it is no longer the publisher and stop.

## Moving to another machine

A site is its IPNS key. Everything else is on IPFS.

On the machine you have now, open the site's settings and use *Show key*
(or `croptop key export <site>`). On the new machine:

```
croptop adopt yoursite.eth --key site.pem
```

or use *Adopt a site* in the console and paste the key. Croptop fetches the
published site, rebuilds the editable files from it, learns the current
sequence number, and takes over. The old machine notices on its next
renewal and marks the site "published elsewhere" instead of fighting.

To go back and forth between two machines, press *Sync* (or run
`croptop sync <site>`) before editing: it merges posts published from the
other machine, newest edit per post wins, then publishes from here.

Keep a copy of the key somewhere safe. It is the only thing IPFS cannot
give back.

## Following other sites

```
croptop follow yoursite.eth        or an IPNS name, or Follow a site in the console
```

Your node fetches the site, keeps it, serves it at `/f/<ipns>/`, checks
for a new version every six hours, and re-announces it to the network every
twelve. A site stays online because its readers host it. Each of your own
sites shows how many nodes are hosting it.

## Templates

Every site renders with the built-in Croptop template until you say
otherwise. On a site's *Template* page, *Fork to edit* copies the template
into the site, where you can change any file with the rendered preview
beside you; *Reset* brings the original back. Template authors publish a
template directory with `croptop template publish <dir>` and share the
CID; anyone installs it with `croptop template install <cid or ENS name>`
or from the same page. The template context is documented in
`docs/format.md`.

## Widgets

A post is HTML, and its attachments travel with it. Attach a script, add
`<script type="module" src="widget.js"></script>` to the post, and it runs
on the published page with `window.croptop` for the site, the post, the
template settings, the chain table, the reader's wallet, and IPFS. The
editor's widget palette inserts starter snippets. See `docs/widgets.md`.

## An always-on node

```
croptop passcode set
croptop serve --role node --listen 0.0.0.0:8086
```

runs headless on a Raspberry Pi or a small server: it keeps your IPNS
records alive, re-provides your sites and the ones you follow, and takes
edits from the console opened from a laptop or phone. `croptop status`
prints what it is doing.

## From a phone or another computer

Set a passcode, then listen on all interfaces:

```
croptop passcode set
croptop --listen 0.0.0.0:8086
```

Sign in as user `Croptop` with the passcode. Over the internet, put the
machine on [Tailscale](https://tailscale.com) or similar rather than
opening the port. The published site trees at `/<site-id>/` are always open;
everything else needs the passcode.

## IPFS engine

Two engines are built in. `embedded` (the default) runs an IPFS node inside
croptop itself, built on boxo, kubo's own libraries: no download, no child
process, same CIDs, same IPNS records, NAT traversal through relays and
hole punching. `kubo` downloads and runs kubo v0.43.0 as a child process
instead. Switch with `croptop --engine kubo`; the choice is remembered.
Keys are shared between engines, so switching back and forth is safe.

A publish writes the IPNS record to the DHT, to the public delegated
routing endpoint, and to the IPNS pubsub topic. ipfs.io reflects a new
version within a couple of minutes. eth.sucks, eth.shop and eth.limo keep
their own resolution caches and can show the previous version for a long
while after that, whichever engine published; the console's site page
links the version the network holds. Set `CROPTOP_DEBUG=1` to see the
engine's log lines.

## Hosting

`croptop host` makes a server a gateway and pin host for a domain, the way
crop.top runs. Sites push to it on publish and stay up while your computer is
closed. See `docs/host.md`.

## Commands

```
croptop                        run the console (default)
croptop import-planet          copy sites and keys from the Croptop Mac app
croptop adopt <name> --key f   take over a published site
croptop sync <site>            merge the network's version, then publish
croptop publish <site>         render, add to IPFS, update the IPNS name
croptop key export <site>      print the site's private key
croptop key import <site> f    install a key for a site you already have
croptop passcode set
croptop template list | install <cid or name> | publish <dir>
croptop engine                 print the active ipfs engine
croptop version
```

`--force` on `publish` overrides the "published elsewhere" and
"network unreachable" checks.

## API

The console speaks Planet's REST API at `/v0` (see Planet's
`Technotes/API.md`), so Planet's `pn` CLI and other clients work. The
console's own routes live under `/v0/croptop/`.

## Gateways

Sites are reachable through any gateway that resolves ENS and IPNS names:
eth.sucks, eth.shop, eth.limo, and more as they appear. Each site picks one
as its canonical address in Settings; that one is written into the site's
absolute links and RSS feed. The default is eth.sucks.

## Not in this version

Aggregating other sites, Filebase/Pinnable/Cloudflare pinning, drafts,
the podcast feed (`rss.xml` is generated, `podcast.xml` is not), HEIC
images, video compression, full-text search, following other sites. Video
thumbnails are generated only when `ffmpeg` is on your `PATH`.

The published file layout is documented in `docs/format.md`.

## Developing

The site template is the git submodule `templates/croptop`
([SiteTemplateCroptop](https://github.com/Planetable/SiteTemplateCroptop)),
embedded into the binary. To work on the template, point at a checkout:

```
go run ./cmd/croptop --templates ../SiteTemplateCroptop --data .data
```

`go test ./...` runs everything; the tests that need kubo look for a
downloaded binary under `.data/kubo` and skip otherwise.

## License

MIT. The bundled Press Start 2P font is under the SIL Open Font License.
