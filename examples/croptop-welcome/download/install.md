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
