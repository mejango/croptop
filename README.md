# Croptop

Publish [Croptop](https://croptop.eth.sucks) sites to IPFS from Linux, macOS,
or Windows. One binary: it runs a local [kubo](https://github.com/ipfs/kubo)
node, renders your posts with the Croptop template, and keeps your site's
IPNS name pointing at the latest version. The console runs in your browser,
so the same thing works from a phone on your network.

This is a port of the Croptop scheme of the [Planet](https://github.com/Planetable/Planet)
Mac app. It reads and writes the same files, so you can move a library over
from the Mac app and back.

## Install

Download the archive for your system from the
[releases page](https://github.com/mejango/croptop/releases), unpack it, and
put `croptop` somewhere on your `PATH`.

- **macOS**: the binary is not notarized yet. After unpacking, run
  `xattr -d com.apple.quarantine croptop` once, or right-click, Open.
- **Linux**: `chmod +x croptop`.
- **Windows**: unzip and run `croptop.exe` from a terminal. Windows Defender
  may ask on first launch because kubo opens a listening port.

Or build from source with Go 1.27+:

```
git clone --recurse-submodules https://github.com/mejango/croptop
cd croptop && go build ./cmd/croptop
```

## First run

```
croptop
```

The first start downloads kubo v0.43.0 (about 40 MB, checksum verified),
creates an IPFS repo, and opens `http://127.0.0.1:8086`. Data lives in:

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
croptop version
```

`--force` on `publish` overrides the "published elsewhere" and
"network unreachable" checks.

## API

The console speaks Planet's REST API at `/v0` (see Planet's
`Technotes/API.md`), so Planet's `pn` CLI and other clients work. The
console's own routes live under `/v0/croptop/`.

## Not in this version

Aggregating other sites, Filebase/Pinnable/Cloudflare pinning, drafts,
podcast RSS, HEIC images, video compression, full-text search, following
other sites. Video thumbnails are generated only when `ffmpeg` is on your
`PATH`.

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
