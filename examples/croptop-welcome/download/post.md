<style>.ct-download{color:inherit;font-family:var(--font-family,system-ui);margin:24px 0 48px}.ct-download *{box-sizing:border-box}.ct-download-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px}.ct-download-card{color:#171712;background:#ead9b6;border:2px solid #171712;border-radius:3px 1px 5px 2px;padding:24px;display:flex;flex-direction:column;align-items:flex-start;min-width:0}.ct-download-card[data-os=linux]{background:#78a0df}.ct-download-card[data-os=windows]{background:#de8d80}.ct-download-card h2{font:400 34px/1.1 Georgia,serif!important;letter-spacing:-.035em;margin:0!important;color:inherit!important}.ct-download-card canvas{width:100%;height:auto;aspect-ratio:1.3;max-height:180px;display:block;margin:12px 0}.ct-download .ct-download-note{font-size:12px!important;line-height:1.4!important;margin:6px 0 0!important;color:inherit!important;opacity:.75}.ct-download-options{margin-top:auto;min-height:90px;display:flex;flex-direction:column;justify-content:flex-end;gap:7px;width:100%}.ct-download-group{display:flex;gap:4px;flex-wrap:wrap}.ct-download button{font:500 12px/1.2 var(--font-family,system-ui)!important;color:#171712!important;background:transparent!important;border:1px solid #17171255!important;border-radius:2px!important;padding:7px 9px!important;min-height:30px;cursor:pointer}.ct-download button[aria-pressed=true]{background:#171712!important;color:#f3e4c4!important;border-color:#171712!important}.ct-download .ct-download-link{display:block;width:100%;text-align:center;margin-top:18px;background:#171712!important;color:#f3e4c4!important;text-decoration:none!important;font-size:14px!important;line-height:1.4!important;font-weight:500;padding:13px 10px;border-radius:2px}.ct-download .ct-download-link:hover{background:#303026!important}.ct-download :focus-visible{outline:2px solid #171712;outline-offset:3px}.ct-download-source{font:12px var(--font-family,system-ui);opacity:.55;margin-top:36px}.ct-download-source a{color:inherit!important;text-decoration:none!important;display:inline-block;padding:8px 0}@media(max-width:650px){.ct-download-grid{grid-template-columns:1fr}.ct-download-card{display:grid;grid-template-columns:1fr 110px;gap:0 12px;padding:24px}.ct-download-card canvas{grid-column:2;grid-row:1/4;margin:0;align-self:center}.ct-download-card h2{grid-column:1}.ct-download-options{grid-column:1;min-height:0;margin-top:16px}.ct-download .ct-download-link{grid-column:1/-1}.ct-download-card .ct-download-note{grid-column:1}}
</style>

<section class="ct-download" aria-label="Download Croptop"><div class="ct-download-grid"><div class="ct-download-card" data-os="mac"><h2>Mac</h2><canvas width="260" height="200" aria-hidden="true"></canvas><div class="ct-download-options"><p class="ct-download-note">Apple Silicon + Intel</p></div><a class="ct-download-link" href="https://github.com/mejango/croptop/releases/latest/download/Croptop.dmg">Download</a></div><div class="ct-download-card" data-os="linux"><h2>Linux</h2><canvas width="260" height="200" aria-hidden="true"></canvas><div class="ct-download-options"><div class="ct-download-group" role="group" aria-label="Linux package"><button type="button" data-format="deb" aria-pressed="true">DEB</button><button type="button" data-format="rpm" aria-pressed="false">RPM</button></div><div class="ct-download-group" role="group" aria-label="Linux processor"><button type="button" data-arch="amd64" aria-pressed="true">Intel / AMD</button><button type="button" data-arch="arm64" aria-pressed="false">ARM</button></div><p class="ct-download-note">Ubuntu / Debian</p></div><a class="ct-download-link" href="https://github.com/mejango/croptop/releases/download/v0.11.0/croptop_0.11.0_linux_amd64.deb">Download</a></div><div class="ct-download-card" data-os="windows"><h2>Windows</h2><canvas width="260" height="200" aria-hidden="true"></canvas><div class="ct-download-options"><div class="ct-download-group" role="group" aria-label="Windows processor"><button type="button" data-arch="amd64" aria-pressed="true">Intel / AMD</button><button type="button" data-arch="arm64" aria-pressed="false">ARM</button></div><p class="ct-download-note"></p></div><a class="ct-download-link" href="https://github.com/mejango/croptop/releases/latest/download/croptop-setup-amd64.exe">Download</a></div></div></section>

<script src="download.js?v=43e482f20da7"></script>

Croptop runs on your own computer and publishes peer to peer. One download, no account, no server.

**macOS**: [Croptop.dmg](https://github.com/mejango/croptop/releases/latest/download/Croptop.dmg), drag it to Applications. It works on Apple Silicon and Intel. The macOS app is Developer ID signed and notarized by Apple. Open Croptop from Applications to use the native Mac app. For the command-line version and browser console, Homebrew users can `brew install mejango/tap/croptop`.

**Windows**: [croptop-setup-amd64.exe](https://github.com/mejango/croptop/releases/latest/download/croptop-setup-amd64.exe) for Intel and AMD, or [croptop-setup-arm64.exe](https://github.com/mejango/croptop/releases/latest/download/croptop-setup-arm64.exe) for ARM. Windows shows a SmartScreen notice because the installer is unsigned; choose More info, then Run anyway.

**Linux**: `.deb` and `.rpm` packages for amd64 and arm64 are on the [latest release](https://github.com/mejango/croptop/releases/latest), or use the one-liner below.

**Command-line install, macOS or Linux**:

```
curl -fsSL https://crop.top/install.sh | sh
```

**Command-line install, Windows PowerShell**:

```
irm https://crop.top/install.ps1 | iex
```

On macOS, open Croptop from Applications. The native app brings your feed, sites, and post editor into one window. On Windows, open the Croptop shortcut to start the browser console. If you installed the command-line version, run `croptop` to open the browser console.

Make a site, write a post, and press Publish. Claim a free name and your site is at crop.top/yourname; an ENS name works at yourname.crop.top.

When a Mac app update is available, choose Get the update to download the new version. Command-line installations can update with `croptop update`. If you installed through a package manager, use that package manager to update.

Source and every release: [github.com/mejango/croptop](https://github.com/mejango/croptop).


<div class="ct-download-source"><a href="source.html" download>Source</a></div>