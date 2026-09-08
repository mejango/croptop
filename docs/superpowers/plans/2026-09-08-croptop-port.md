# Croptop Port Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One Go binary (`croptop`) for Linux/macOS/Windows that renders, publishes, and serves Croptop sites from Planet-compatible data, with sequence-aware IPNS publishing so a site can move between machines.

**Architecture:** Stdlib-first Go. `store` owns the JSON files, `render` turns them into `public/<uuid>/` with pongo2, `ipfs` manages a downloaded kubo child process and its keystore, `publish` orchestrates render → add → IPNS publish with network sequence checks, `server` exposes the Planet-compatible `/v0` API plus an embedded web UI.

**Tech Stack:** Go 1.27, `github.com/flosch/pongo2/v6`, `github.com/yuin/goldmark`, `golang.org/x/image` (font rendering), kubo v0.43.0 (downloaded, not linked), goreleaser.

**Spec:** `docs/superpowers/specs/2026-09-08-croptop-port-design.md`

## Global Constraints

- Module path `github.com/mejango/croptop`. Repo at `~/Documents/croptop/app`.
- Timestamps in all JSON are `float64` seconds since 2001-01-01 UTC (Apple epoch). Unix = apple + 978307200.
- JSON keys written by us must match Planet's CodingKeys exactly; unknown keys pass through.
- Kubo version pinned in one constant: `v0.43.0`. Downloads from `https://dist.ipfs.tech/kubo/v0.43.0/`.
- Kubo ports: API 5981-5991, gateway 18181-18191, swarm 4001-4011, first free wins.
- IPNS publish flags: `--allow-offline --lifetime=7200h --ttl=1m --sequence=N --key=<site uuid>`.
- Server default listen `127.0.0.1:8086`; non-loopback listen requires a passcode; auth is HTTP Basic, username `Croptop`.
- Data dir: `os.UserConfigDir()/croptop`, overridable with `--data`.
- Template: git submodule `templates/croptop`, embedded with `go:embed`; `--templates <dir>` overrides.
- Every non-trivial unit ships with one small `_test.go`. No test frameworks.
- Commit after every task with the trailer lines used in this repo's first commit.

---

### Task 1: Module bootstrap and the on-disk store

**Files:**
- Create: `go.mod`, `internal/store/time.go`, `internal/store/site.go`, `internal/store/post.go`, `internal/store/store.go`, `internal/store/store_test.go`, `internal/store/testdata/site/planet.json`, `internal/store/testdata/site/Articles/*.json` (copied from the user's real library with tokens/keys blanked)

**Interfaces:**
- Produces:
  - `type AppleTime float64` with `Time() time.Time`, `FromTime(time.Time) AppleTime`, `Unix() int64`
  - `type Site struct { ID, Name, About, Domain, IPNS, TemplateName, LastPublishedCID string; Created, Updated, LastPublished AppleTime; Tags map[string]string; Archived bool; IPNSSequence uint64 `json:"ipnsSequence,omitempty"`; PublishedElsewhere bool `json:"publishedElsewhere,omitempty"`; Extra map[string]json.RawMessage `json:"-"` ...all Planet keys }`
  - `type Post struct { ID, Title, Content, ContentRendered, Summary, Link, Slug, HeroImage, ExternalLink, VideoFilename, AudioFilename string; ArticleType int; Created, Modified AppleTime; Attachments []string; CIDs map[string]string; Tags map[string]string; HeroImageWidth, HeroImageHeight int; Pinned *AppleTime; Extra map[string]json.RawMessage `json:"-"` }`
  - `type Store struct{ Root string }` with `Sites() ([]*Site, error)`, `Site(id) (*Site, error)`, `SaveSite(*Site) error`, `Posts(siteID) ([]*Post, error)`, `Post(siteID, postID) (*Post, error)`, `SavePost(siteID, *Post) error`, `DeletePost(siteID, postID) error`, `SiteDir(id)`, `PostDir(siteID, postID)` (attachments), `PublicDir(id)`, `TemplateSettings(siteID) (map[string]any, error)`, `SaveTemplateSettings(siteID, map[string]any) error`, `Ops(siteID) (map[string]AppleTime, error)`, `RecordOp(siteID, key string) error`
- Layout under Root: `sites/<id>/planet.json`, `sites/<id>/Articles/<post>.json`, `sites/<id>/Articles/<post>/<attachment>`, `sites/<id>/templateSettings.json`, `sites/<id>/ops.json`, `public/<id>/`.

- [ ] **Step 1: Bootstrap module**

```bash
cd ~/Documents/croptop/app && go mod init github.com/mejango/croptop
```

- [ ] **Step 2: Copy a real fixture**

```bash
S=~/Library/Containers/xyz.planetable.Lite/Data/Documents/Planet/My/FF5F456D-904F-4EE6-8BB5-AD175C65319A
mkdir -p internal/store/testdata/site/Articles
cp $S/planet.json $S/templateSettings.json $S/ops.json internal/store/testdata/site/
cp $S/Articles/*.json internal/store/testdata/site/Articles/
grep -l 'APIToken\|APIKey' internal/store/testdata/site/planet.json && sed -i '' 's/"filebaseAPIToken" : "[^"]*"/"filebaseAPIToken" : ""/' internal/store/testdata/site/planet.json
```

- [ ] **Step 3: Write the failing round-trip test**

```go
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRoundTripPreservesEveryKey(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, "sites", "FF5F456D-904F-4EE6-8BB5-AD175C65319A"), os.DirFS("testdata/site")); err != nil {
		t.Fatal(err)
	}
	s := &Store{Root: root}
	site, err := s.Site("FF5F456D-904F-4EE6-8BB5-AD175C65319A")
	if err != nil {
		t.Fatal(err)
	}
	if site.Name != "CocoPay 🥥" || site.TemplateName != "Croptop" {
		t.Fatalf("bad decode: %+v", site)
	}
	if site.Created.Time().Year() != 2025 {
		t.Fatalf("apple epoch not applied: %v", site.Created.Time())
	}
	if err := s.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	orig, _ := os.ReadFile("testdata/site/planet.json")
	saved, _ := os.ReadFile(filepath.Join(root, "sites", site.ID, "planet.json"))
	json.Unmarshal(orig, &before)
	json.Unmarshal(saved, &after)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("round trip changed planet.json\nbefore %v\nafter  %v", before, after)
	}
	posts, err := s.Posts(site.ID)
	if err != nil || len(posts) == 0 {
		t.Fatalf("posts: %v %d", err, len(posts))
	}
	p := posts[0]
	if err := s.SavePost(site.ID, p); err != nil {
		t.Fatal(err)
	}
	origP, _ := os.ReadFile(filepath.Join("testdata/site/Articles", p.ID+".json"))
	savedP, _ := os.ReadFile(filepath.Join(root, "sites", site.ID, "Articles", p.ID+".json"))
	json.Unmarshal(origP, &before)
	json.Unmarshal(savedP, &after)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("round trip changed article\nbefore %v\nafter  %v", before, after)
	}
}
```

- [ ] **Step 4: Run to see it fail**

Run: `go test ./internal/store/ -run TestRoundTrip -v` — Expected: compile error, `Store` undefined.

- [ ] **Step 5: Implement**

`time.go`:
```go
package store

import "time"

const appleEpochOffset = 978307200

type AppleTime float64

func FromTime(t time.Time) AppleTime { return AppleTime(float64(t.UnixNano())/1e9 - appleEpochOffset) }
func Now() AppleTime                 { return FromTime(time.Now()) }
func (a AppleTime) Unix() int64      { return int64(float64(a) + appleEpochOffset) }
func (a AppleTime) Time() time.Time {
	sec, frac := math.Modf(float64(a) + appleEpochOffset)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}
```

`site.go` and `post.go`: structs with every Planet key from the spec's CodingKeys list, plus `Extra map[string]json.RawMessage` `json:"-"`. Implement `UnmarshalJSON`/`MarshalJSON` with the "alias type + raw map" pattern:

```go
func (s *Site) UnmarshalJSON(b []byte) error {
	type alias Site
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for _, k := range knownSiteKeys { delete(raw, k) }
	a.Extra = raw
	*s = Site(a)
	return nil
}

func (s Site) MarshalJSON() ([]byte, error) {
	type alias Site
	b, err := json.Marshal(alias(s))
	if err != nil {
		return nil, err
	}
	if len(s.Extra) == 0 {
		return b, nil
	}
	var m map[string]json.RawMessage
	json.Unmarshal(b, &m)
	for k, v := range s.Extra { m[k] = v }
	return json.Marshal(m)
}
```
`knownSiteKeys` is built once by reflecting over the struct's `json` tags. Save with `json.MarshalIndent(v, "", "  ")` and `os.WriteFile` via a temp file + rename.

`store.go`: the path helpers and CRUD listed under Interfaces. `Sites()` lists `sites/*/planet.json`, sorted by `Created`. `Posts()` lists `sites/<id>/Articles/*.json` sorted by `Created` descending.

- [ ] **Step 6: Run to see it pass**

Run: `go test ./internal/store/ -v` — Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod internal/store && git commit -m "store: Planet-compatible site and post files"
```

---

### Task 2: Keystore in pure Go

**Files:**
- Create: `internal/ipfs/keystore.go`, `internal/ipfs/keystore_test.go`

**Interfaces:**
- Produces:
  - `KeystoreFilename(name string) string` → `"key_" + base32 lowercase no padding of name`
  - `type Keystore struct{ Dir string }` with `Has(name) bool`, `ImportPEM(name string, pemBytes []byte) error`, `ExportPEM(name string) ([]byte, error)`, `ImportRaw(name string, protobuf []byte) error`, `Name(name string) (string, error)` (the `k51…` IPNS name), `Generate(name string) (ipnsName string, err error)`
- Kubo keystore file format: protobuf `PrivateKey{Type=1 (Ed25519) as field 1 varint, Data = 64 bytes seed||pub as field 2 bytes}`. Bytes: `08 01 12 40 <64 bytes>`.
- IPNS name: `PublicKey{Type=1, Data=32 bytes}` protobuf → identity multihash (`0x00`, len 36) → CIDv1 (`0x01`, codec `0x72`) → base36 lowercase with `k` prefix.

- [ ] **Step 1: Failing test**

```go
package ipfs

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestKeystoreFilename(t *testing.T) {
	got := KeystoreFilename("7B0816A5-9162-421E-AA21-88A5097CBA99")
	want := "key_g5bdaobrgzatkljzge3deljugiyuklkbiezdcljyhbatkmbzg5bueqjzhe"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestPEMRoundTripAndName(t *testing.T) {
	ks := &Keystore{Dir: t.TempDir()}
	_, priv, _ := ed25519.GenerateKey(nil)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	p := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := ks.ImportPEM("site", p); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(ks.Dir, KeystoreFilename("site")))
	if len(raw) != 68 || raw[0] != 0x08 || raw[1] != 0x01 || raw[2] != 0x12 || raw[3] != 0x40 {
		t.Fatalf("bad protobuf: %x", raw[:4])
	}
	out, err := ks.ExportPEM("site")
	if err != nil || string(out) != string(p) {
		t.Fatalf("export mismatch: %v\n%s\n%s", err, out, p)
	}
	name, err := ks.Name("site")
	if err != nil || len(name) != 62 || name[:3] != "k51" {
		t.Fatalf("bad name %q %v", name, err)
	}
}
```

- [ ] **Step 2: Run** — Expected: FAIL, undefined.

- [ ] **Step 3: Implement**

```go
func KeystoreFilename(name string) string {
	return "key_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(name)))
}

func marshalPrivProto(priv ed25519.PrivateKey) []byte {
	return append([]byte{0x08, 0x01, 0x12, 0x40}, priv...) // seed||pub, 64 bytes
}

func parsePrivProto(b []byte) (ed25519.PrivateKey, error) {
	if len(b) != 68 || b[0] != 0x08 || b[1] != 0x01 || b[2] != 0x12 || b[3] != 0x40 {
		return nil, errors.New("not an ed25519 kubo key")
	}
	return ed25519.PrivateKey(b[4:]), nil
}

func ipnsName(pub ed25519.PublicKey) string {
	pubProto := append([]byte{0x08, 0x01, 0x12, 0x20}, pub...)      // 36 bytes
	mh := append([]byte{0x00, byte(len(pubProto))}, pubProto...)     // identity multihash
	cid := append([]byte{0x01, 0x72}, mh...)                         // cidv1 libp2p-key
	return "k" + new(big.Int).SetBytes(cid).Text(36)
}
```
`Generate` uses `ed25519.GenerateKey(rand.Reader)` and writes the file with mode 0600. `ExportPEM` uses `x509.MarshalPKCS8PrivateKey` + `pem.EncodeToMemory` with type `PRIVATE KEY`. `ImportPEM` parses with `x509.ParsePKCS8PrivateKey`, rejects non-ed25519.

- [ ] **Step 4: Run** — Expected: PASS. Also run this local-only sanity check (do not commit its output):

```bash
go run ./internal/ipfs/cmd/checkname "$HOME/Library/Containers/xyz.planetable.Lite/Data/Library/Application Support/ipfs/keystore" FF5F456D-904F-4EE6-8BB5-AD175C65319A
```
Write that tiny `main` under `internal/ipfs/cmd/checkname/` (prints `ks.Name(arg)`), confirm it prints `k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb` (the `ipns` field of that site's planet.json), then delete the directory.

- [ ] **Step 5: Commit** — `git add internal/ipfs && git commit -m "ipfs: pure-Go kubo keystore with PEM import/export and IPNS names"`

---

### Task 3: Kubo download, repo setup, daemon, CLI wrapper

**Files:**
- Create: `internal/ipfs/kubo.go` (download/extract), `internal/ipfs/repo.go` (init + config), `internal/ipfs/daemon.go` (process), `internal/ipfs/cli.go` (Run helpers), `internal/ipfs/kubo_test.go`

**Interfaces:**
- Produces:
  - `const KuboVersion = "v0.43.0"`
  - `EnsureKubo(dir string, progress func(string)) (binPath string, err error)` — returns `dir/ipfs` or `dir/ipfs.exe`, downloading if absent
  - `type Node struct { Bin, RepoPath string; APIPort, GatewayPort, SwarmPort int; cmd *exec.Cmd }`
  - `NewNode(bin, repoPath string) *Node`
  - `(*Node) Init(ctx) error` — `ipfs init` if `repoPath/config` missing, then apply config
  - `(*Node) Start(ctx) error` — runs daemon, waits for "Daemon is ready", up to 90s
  - `(*Node) Stop() error` — `ipfs shutdown`, then kill after 10s
  - `(*Node) Run(ctx, args ...string) (stdout []byte, err error)` — error includes stderr
  - `(*Node) RunJSON(ctx, v any, args ...string) error` — adds `--enc=json`
  - `(*Node) Keystore() *Keystore` → `RepoPath/keystore`
  - `(*Node) GatewayURL() string`

- [ ] **Step 1: Failing test for asset naming and port scouting**

```go
func TestAssetName(t *testing.T) {
	cases := map[[2]string]string{
		{"darwin", "arm64"}:  "kubo_v0.43.0_darwin-arm64.tar.gz",
		{"linux", "amd64"}:   "kubo_v0.43.0_linux-amd64.tar.gz",
		{"windows", "amd64"}: "kubo_v0.43.0_windows-amd64.zip",
	}
	for k, want := range cases {
		if got := assetName(k[0], k[1]); got != want {
			t.Fatalf("%v: got %s want %s", k, got, want)
		}
	}
}

func TestFreePortInRange(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:5981")
	defer l.Close()
	p, err := freePort(5981, 5991)
	if err != nil || p != 5982 {
		t.Fatalf("got %d %v", p, err)
	}
}
```

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement**

`kubo.go`: `assetName(goos, goarch)`; `EnsureKubo` downloads `https://dist.ipfs.tech/kubo/<ver>/<asset>` and `<asset>.sha512`, verifies with `crypto/sha512` (the sha512 file is `"<hex>  <asset>"`), extracts `kubo/ipfs[.exe]` from tar.gz (`archive/tar` + `compress/gzip`) or zip (`archive/zip`), chmod 0755, writes `dir/VERSION`. If `dir/VERSION` equals `KuboVersion` and the binary exists, return immediately.

`repo.go`: `Init` runs `ipfs init --profile=default` when `config` is missing, then applies, via `ipfs config` / `ipfs config --json`:
```
Addresses.API        /ip4/127.0.0.1/tcp/<api>
Addresses.Gateway    /ip4/127.0.0.1/tcp/<gw>
Addresses.Swarm      ["/ip4/0.0.0.0/tcp/<s>","/ip6/::/tcp/<s>","/ip4/0.0.0.0/udp/<s>/quic-v1","/ip6/::/udp/<s>/quic-v1"]
Swarm.ConnMgr        {"Type":"basic","LowWater":10,"HighWater":20,"GracePeriod":"20s"}
Peering.Peers        [Pinnable 12D3KooWBJY6ZVV8Tk8UDDFMEqWoxn89Xc8wnpm8uBFSR3ijDkui /ip4/167.71.172.216/tcp/4001 + quic,
                      bit.site 12D3KooWJ6MTkNM8Bu8DzNiRm1GY3Wqh8U8Pp1zRWap6xY3MvsNw /dnsaddr/node-1.ipfs.bit.site,
                      4everland 12D3KooWQ85aSCFwFkByr5e3pUCQeuheVhobVxGSSs1DrRQHnkbB /dnsaddr/node-1.ipfs.4everland.net,
                      Filebase 12D3KooWGtYkBAaqJMJEmywMxaCiNP7LCEFUAFiLEBASe232c2VH /dns4/bitswap.filebase.io/tcp/443/wss]
DNS.Resolvers        {"eth.":"https://dns.eth.limo/dns-query","bit.":"https://dweb-dns.v2ex.pro/dns-query"}
Ipns.UsePubsub       true
```
(Copy the exact 4everland/Filebase peer IDs from `Planet/IPFS/IPFSDaemon.swift:1002-1030` in the scratchpad clone.) Ports come from `freePort` over each range; store the chosen ports on the Node.

`daemon.go`: `Start` runs `ipfs daemon --enable-namesys-pubsub` with `IPFS_PATH` env, scans stdout lines for `Daemon is ready`, keeps last 50 stderr lines in a ring for error reporting. `Stop` runs `ipfs shutdown` then waits on the process with a 10s deadline and `Process.Kill`.

`cli.go`: `Run` builds `exec.CommandContext(ctx, n.Bin, args...)`, sets `IPFS_PATH`, captures stdout/stderr, wraps errors as `fmt.Errorf("ipfs %s: %w: %s", args[0], err, strings.TrimSpace(stderr))`.

- [ ] **Step 4: Run tests** — PASS. Then a manual smoke test:

```bash
go run ./cmd/croptop/ ipfs-smoke   # temporary subcommand: EnsureKubo into .data/kubo, Init .data/ipfs, Start, print `ipfs id`, Stop
```
Add `ipfs-smoke` to `cmd/croptop/main.go` now; it stays as a hidden diagnostic. Expected: prints a PeerID and exits cleanly.

- [ ] **Step 5: Commit** — `git commit -am "ipfs: download kubo, init repo, run daemon"`

---

### Task 4: Renderer

**Files:**
- Create: `internal/render/template.go` (pongo2 loading + shims + filters), `internal/render/public.go` (PublicSite/PublicPost JSON), `internal/render/render.go` (Render), `internal/render/cover.go` (cover PNG), `internal/render/markdown.go`, `internal/render/fonts/PressStart2P-Regular.ttf` (download from `https://github.com/google/fonts/raw/main/ofl/pressstart2p/PressStart2P-Regular.ttf`, OFL, keep its OFL.txt beside it), `internal/render/render_test.go`, `templates/embed.go`

**Interfaces:**
- Consumes: `store.Store`, `store.Site`, `store.Post`, `store.AppleTime`
- Produces:
  - `type CIDer interface{ FileCIDv0(ctx, path string) (string, error) }` (implemented in Task 5 by the Node via `ipfs add --only-hash --cid-version=0 -Q`)
  - `type Renderer struct { Store *store.Store; Templates fs.FS; CIDs CIDer; FFmpeg string }`
  - `(*Renderer) Render(ctx, siteID string) error` — full site into `Store.PublicDir(siteID)`
  - `(*Renderer) RenderPost(ctx, siteID, postID string) error`
  - `templates.FS` — `go:embed croptop/template.json croptop/templates croptop/assets`

Template shims applied to source text before pongo2 parses (regex, in this order):
1. `(\S+)\.count\s*>\s*0` → `$1|length > 0`
2. `\s*!=\s*nil` → `` and `\s*==\s*true` → ``
3. `(\w+)\['(\w+)'\]` → `$1.$2`
4. `{% include './x.html' %}` → `{% include "x.html" %}`; `{% extends 'x' %}` quotes normalized.
Filters registered: `mdyydot` (`"1.2.'26"` style: `M.D.'YY`), `formatDateC` (`"Nov 1, 2025"` style, check `Planet/TemplateBrowser/Template.swift` StencilExtension for the exact formats and copy them), `escape` exists.
The template context's `article.created` is `map[string]any{"timeIntervalSince1970": unix, "apple": float}` and both date filters accept that map.

- [ ] **Step 1: Failing test**

```go
func TestRenderFixtureProducesTemplateContract(t *testing.T) {
	root := t.TempDir()
	os.CopyFS(filepath.Join(root, "sites", "FF5F456D-904F-4EE6-8BB5-AD175C65319A"), os.DirFS("../store/testdata/site"))
	s := &store.Store{Root: root}
	r := &Renderer{Store: s, Templates: templates.FS, CIDs: fakeCIDs{}}
	if err := r.Render(context.Background(), "FF5F456D-904F-4EE6-8BB5-AD175C65319A"); err != nil {
		t.Fatal(err)
	}
	pub := s.PublicDir("FF5F456D-904F-4EE6-8BB5-AD175C65319A")
	for _, f := range []string{"index.html", "planet.json", "templateSettings.json", "tags.html", "assets/style.css"} {
		if _, err := os.Stat(filepath.Join(pub, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	var pj struct{ Articles []map[string]any `json:"articles"` }
	b, _ := os.ReadFile(filepath.Join(pub, "planet.json"))
	json.Unmarshal(b, &pj)
	if len(pj.Articles) == 0 {
		t.Fatal("planet.json has no articles")
	}
	id := pj.Articles[0]["id"].(string)
	for _, f := range []string{"index.html", "simple.html", "article.json", "article.md", "nft.json", "nft.json.cid.txt"} {
		if _, err := os.Stat(filepath.Join(pub, id, f)); err != nil {
			t.Fatalf("missing %s/%s", id, f)
		}
	}
	html, _ := os.ReadFile(filepath.Join(pub, id, "index.html"))
	if !strings.Contains(string(html), "../assets/") {
		t.Fatal("assets_prefix not applied on post page")
	}
}

type fakeCIDs struct{}

func (fakeCIDs) FileCIDv0(ctx context.Context, p string) (string, error) {
	return "QmFAKE" + filepath.Base(p), nil
}
```
Also `TestCoverImage`: `WriteCover(path, "hello world")` produces a decodable 512x512 PNG.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement**

`public.go`: `PublicSite` / `PublicPost` structs with the exact key sets from the spec ("Public JSON shapes"). `PublicPost` computed from `store.Post`: `hasVideo`, `hasAudio`, `heroImageFilename` (= hero image or first image attachment), `heroImageURL`, `link` (`/<id>/` or `/<slug>/`).

`render.go`, `Render`:
1. `os.RemoveAll` nothing; write into place (Planet keeps derived files; we do too). `assets/` copied from the template FS every render.
2. For each post: copy attachments from `PostDir` into `public/<id>/<post>/`; compute `cids` for each attachment via `CIDs.FileCIDv0` and store on the Post (save it); if no media attachments or audio: ensure `_cover.png` (ops key `"<post>-cover-<sha256 of text>"`), set `attachments=["_cover.png"]` when empty, CID it; if video and `FFmpeg != ""`: `ffmpeg -y -i <video> -frames:v 1 _videoThumbnail.png` (ops key `"<post>-videothumb-<filename>"`); write `article.json`, `article.md` (content), `nft.json` + `nft.json.cid.txt` per the spec's NFT rules (`image = https://ipfs.io/ipfs/<first cid>`, video → thumbnail cid, audio → cover cid; attributes title, title_sha256, content_sha256 if content, created_at unix string); render `blog.html` → `index.html`, `simple.html` → `simple.html`; if slug, copy the post dir to `public/<id>/<slug>/`.
3. `planet.json`, `templateSettings.json`, `avatar.png`, `favicon.ico`.
4. `index.html` from `index.html`, `tags.html` and per-tag pages if `template.json.generateTagPages`.

Context builder `ctx(site, posts, post *Post, prefix string, extra map[string]any)` producing every key listed in the spec. `user_settings` = template settings map, plus for every key ending in `Color` a `<key>Filter` string: implement the CSS-filter solver only if the template's CSS uses `highlightColorFilter` (grep `assets/style.css` and templates; if unused, set it to `""` and note in a `// ponytail:` comment).

`template.go`: load all `templates/**/*.html` from the FS into a `pongo2.TemplateSet` with a custom `pongo2.TemplateLoader` that applies the shims; register filters.

`cover.go`: `WriteCover(path, text string) error` — 512x512 black `image.RGBA`, Press Start 2P at 16px via `golang.org/x/image/font/opentype`, greedy word wrap in a 448px box at (32,32), white text, `png.Encode`.

`markdown.go`: goldmark with `extension.GFM`, `html.WithUnsafe()`; `Render(md string) string`.

- [ ] **Step 4: Run** — PASS. Then compare with Planet's real output for one post:

```bash
diff <(jq -S . ~/Library/Containers/xyz.planetable.Lite/Data/Documents/Planet/Public/FF5F456D-904F-4EE6-8BB5-AD175C65319A/planet.json) <(jq -S . /tmp/render-out/planet.json) | head -40
```
(Use a temporary `render-smoke` subcommand that renders a site from `--data` into its public dir.) Differences must only be `updated`/`build_timestamp`-class values and `contentRendered` whitespace. Fix key-name mismatches until so.

- [ ] **Step 5: Commit** — `git add internal/render templates && git commit -m "render: Croptop template to public tree with pongo2"`

---

### Task 5: Publisher with sequence-aware IPNS

**Files:**
- Create: `internal/publish/publish.go`, `internal/publish/sequence.go`, `internal/publish/keepalive.go`, `internal/publish/publish_test.go`, `internal/publish/testdata/fake-ipfs` (shell script)
- Modify: `internal/ipfs/cli.go` — add `FileCIDv0`, `AddDir`, `NamePublish`, `NameResolve`, `NetworkRecord`

**Interfaces:**
- Consumes: `ipfs.Node`, `render.Renderer`, `store.Store`
- Produces:
  - `(*ipfs.Node) FileCIDv0(ctx, path) (string, error)` — `add --only-hash --cid-version=0 -Q <path>`
  - `(*ipfs.Node) AddDir(ctx, dir) (cid string, err error)` — `add -r -H --cid-version=1 -Q <dir>`
  - `type Record struct{ Value string; Sequence uint64; Validity time.Time }`
  - `(*ipfs.Node) NetworkRecord(ctx, ipnsName) (*Record, error)` — `name get <name>` to a temp file, then `name inspect --enc=json <file>`; `ErrNoRecord` when kubo reports not found
  - `(*ipfs.Node) NamePublish(ctx, key, cid string, seq uint64) error`
  - `var ErrPublishedElsewhere = errors.New(...)`, `var ErrWouldResetSequence = errors.New(...)`
  - `type Publisher struct{ Store *store.Store; Node *ipfs.Node; Render *render.Renderer; Gateway string }`
  - `(*Publisher) Publish(ctx, siteID string, force bool) (cid string, seq uint64, err error)`
  - `(*Publisher) Keepalive(ctx, siteID) error`
  - `(*Publisher) RunKeepalive(ctx, every time.Duration)` — loop over all sites
  - `nextSequence(local uint64, lastCID string, net *Record, netErr error, force bool) (uint64, error)` — pure function under test

- [ ] **Step 1: Failing test for the pure sequence rule**

```go
func TestNextSequence(t *testing.T) {
	rec := func(seq uint64, cid string) *Record { return &Record{Sequence: seq, Value: "/ipfs/" + cid} }
	cases := []struct {
		name         string
		local        uint64
		lastCID      string
		net          *Record
		netErr       error
		force        bool
		want         uint64
		wantErr      error
	}{
		{"first ever", 0, "", nil, ErrNoRecord, false, 1, nil},
		{"normal", 5, "bafyA", rec(5, "bafyA"), nil, false, 6, nil},
		{"network ahead but ours", 3, "bafyA", rec(7, "bafyA"), nil, false, 8, nil},
		{"published elsewhere", 3, "bafyA", rec(7, "bafyB"), nil, false, 0, ErrPublishedElsewhere},
		{"elsewhere but forced", 3, "bafyA", rec(7, "bafyB"), nil, true, 8, nil},
		{"offline with history", 4, "bafyA", nil, errors.New("timeout"), false, 5, nil},
		{"fresh install offline", 0, "bafyA", nil, errors.New("timeout"), false, 0, ErrWouldResetSequence},
		{"fresh install offline forced", 0, "bafyA", nil, errors.New("timeout"), true, 1, nil},
	}
	for _, c := range cases {
		got, err := nextSequence(c.local, c.lastCID, c.net, c.netErr, c.force)
		if !errors.Is(err, c.wantErr) || got != c.want {
			t.Errorf("%s: got %d %v want %d %v", c.name, got, err, c.want, c.wantErr)
		}
	}
}
```

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement `nextSequence`**

```go
func nextSequence(local uint64, lastCID string, net *Record, netErr error, force bool) (uint64, error) {
	switch {
	case netErr == nil:
		if net.Value != "/ipfs/"+lastCID && lastCID != "" && !force {
			return 0, ErrPublishedElsewhere
		}
		return max(local, net.Sequence) + 1, nil
	case errors.Is(netErr, ErrNoRecord):
		return local + 1, nil
	default:
		if local == 0 && lastCID != "" && !force {
			return 0, ErrWouldResetSequence
		}
		return local + 1, nil
	}
}
```
`Publish`: render → `AddDir` → `NetworkRecord` with 20s context → `nextSequence` → `NamePublish` → update `site.IPNSSequence`, `LastPublishedCID`, `LastPublished`, `PublishedElsewhere=false`, save. `Keepalive`: skip if `PublishedElsewhere`; `NetworkRecord`; if value is ours, `NamePublish(seq+1)` with the same CID; if not ours and seq > local, set `PublishedElsewhere=true` and save.

- [ ] **Step 4: Integration test with a fake kubo**

`testdata/fake-ipfs`:
```sh
#!/bin/sh
echo "$@" >> "$FAKE_LOG"
case "$1 $2" in
  "add "*) echo bafyFAKE ;;
  "name get") printf 'record' ;;
  "name inspect") echo '{"Sequence":7,"Value":"/ipfs/bafyOLD","Validity":"2030-01-01T00:00:00Z"}' ;;
  "name publish") echo '{"Name":"k51","Value":"/ipfs/bafyFAKE"}' ;;
esac
```
Test: site with `lastPublishedCID=bafyOLD`, `ipnsSequence=2`; `Publish` must call `name publish` with `--sequence=8` (grep `$FAKE_LOG`) and save `ipnsSequence=8`. Second test: site with `lastPublishedCID=bafyMINE` → `ErrPublishedElsewhere` and no `name publish` line in the log.

- [ ] **Step 5: Run** — PASS. **Commit** — `git commit -am "publish: sequence-aware IPNS publishing and keepalive"`

---

### Task 6: Import from a Planet/Croptop library

**Files:**
- Create: `internal/publish/importplanet.go`, `internal/publish/importplanet_test.go`

**Interfaces:**
- Produces: `ImportPlanet(st *store.Store, ks *ipfs.Keystore, container string, force bool, log func(string)) (imported []string, err error)`
- Container default (macOS): `~/Library/Containers/xyz.planetable.Lite/Data`. Also accept the Planet container `xyz.planetable.Planet` when `--container` is given.

- [ ] **Step 1: Failing test** — build a fake container in a temp dir with one site (from `store/testdata/site`), a `Public/<id>/<post>/photo.png`, and a keystore file produced by `Keystore.Generate`; assert after import: `sites/<id>/planet.json` exists, `sites/<id>/Articles/<post>/photo.png` exists, `public/<id>/<post>/nft.json.cid.txt` preserved, keystore file present under the new repo, and a second import without `force` returns an error mentioning the site id.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement** — copy `Documents/Planet/My/<id>` → `sites/<id>`; copy `Documents/Planet/Public/<id>` → `public/<id>`; for each post, for each name in `attachments` (and `_cover.png`, `_videoThumbnail.png`), copy from `public/<id>/<post>/` into `sites/<id>/Articles/<post>/` if the file exists; copy `Library/Application Support/ipfs/keystore/<KeystoreFilename(id)>` → `ks.Dir`; if the keystore file is missing, log a warning naming the site (the site can still be adopted later with a PEM). Seed `IPNSSequence` = 0 (the first publish will read the network).

- [ ] **Step 4: Run** — PASS. **Commit** — `git commit -am "import-planet: bring sites, files, and keys over from the Mac app"`

---

### Task 7: Adopt and sync

**Files:**
- Create: `internal/publish/adopt.go`, `internal/publish/sync.go`, `internal/publish/adopt_test.go`
- Modify: `internal/ipfs/cli.go` — add `Get(ctx, cid, dest) error` (`ipfs get -o dest cid`), `Resolve(ctx, path) (string, error)` (`resolve --enc=json`)

**Interfaces:**
- Produces:
  - `(*Publisher) Adopt(ctx, nameOrENS string, pemBytes []byte) (siteID string, err error)`
  - `(*Publisher) Sync(ctx, siteID string) (added, updated int, err error)`
  - `rebuildSource(st *store.Store, siteID, publicDir string) error` — pure filesystem, testable without kubo
  - `mergePosts(local, remote []*store.Post) (merged []*store.Post, added, updated int)` — pure

- [ ] **Step 1: Failing tests** — `TestRebuildSourceFromPublic`: render the fixture site with Task 4's renderer into a temp public dir, delete the `sites/` dir, call `rebuildSource`, then assert `Store.Site(id)` loads with the same name/ipns and `Store.Posts(id)` has the same ids, titles, content, attachments, and that `sites/<id>/Articles/<post>/<attachment>` exists. `TestMergePosts`: remote newer `modified` wins; local-only kept; remote-only added.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement** — `Adopt`: if name ends with `.eth`, `Resolve("/ipns/"+name)` → `/ipns/k51…` else use as is; `Resolve("/ipns/"+k51)` → `/ipfs/<cid>`; `Get` into a temp dir; read `planet.json` for `id` and verify `ks.Name(id)` after `ImportPEM(id, pem)` equals the k51 name (else remove the key and fail with a clear message); move temp dir to `public/<id>`; `rebuildSource`; `NetworkRecord` → `IPNSSequence`; `lastPublishedCID=<cid>`; save. `Sync`: `NetworkRecord` → `Get` into temp → `rebuildSource` into a second temp `store.Store` → `mergePosts` → save merged posts and copy attachments → `Publish`.

- [ ] **Step 4: Run** — PASS. **Commit** — `git commit -am "adopt and sync: move a site between machines by key"`

---

### Task 8: HTTP server and Planet-compatible API

**Files:**
- Create: `internal/server/server.go` (mux, auth, public tree), `internal/server/api.go` (`/v0` routes), `internal/server/croptop.go` (`/v0/croptop/*` UI routes), `internal/server/server_test.go`

**Interfaces:**
- Consumes: `store.Store`, `publish.Publisher`, `ipfs.Node`
- Produces: `type Server struct{ Store; Pub; Node; Passcode string; UI fs.FS; Version string }`, `(*Server) Handler() http.Handler`
- Routes exactly as the spec's Server section. JSON responses for planets and articles are the `store.Site` / `store.Post` marshaling (Planet's `pn` reads these keys). Multipart limits 5 MB avatar, 50 MB attachments. `attachmentMode` `keep|append|replace`.

- [ ] **Step 1: Failing test** — `httptest.NewServer(srv.Handler())` on a temp store: `GET /v0/ping` 200; `POST /v0/planets/my` (multipart name, about, template=Croptop) creates a site and returns JSON with `id`; `POST /v0/planets/my/<id>/articles` with title, content, and an attachment file → 200 and the file exists under `sites/<id>/Articles/<post>/`; `GET /v0/planets/my/<id>/articles` lists it; `DELETE` removes it; with `Passcode` set, unauthenticated `GET /v0/planets/my` is 401 and Basic `Croptop:<passcode>` is 200; `GET /<id>/` after a render serves `index.html`.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement** — Go 1.22+ `http.ServeMux` patterns (`GET /v0/planets/my/{id}/articles/{post}`), `basicAuth` middleware skipped for `GET /v0/planets/my/{id}/public` and the public tree, `http.FileServerFS` over `public/` mounted at `/{id}/` by checking the first path segment parses as a UUID. Publish route calls `Pub.Publish` and returns `{cid, sequence}` or 409 with `{"error": "published elsewhere"}`. Create-site generates a UUID (`crypto/rand`, RFC 4122 v4 formatted uppercase like Planet), `ks.Generate(id)` for the IPNS name, writes `templateSettings.json` from `template.json` defaults, saves.

- [ ] **Step 4: Run** — PASS. **Commit** — `git commit -am "server: Planet-compatible /v0 API and public tree"`

---

### Task 9: Web UI

**Files:**
- Create: `web/index.html`, `web/app.js`, `web/style.css`, `web/embed.go` (`//go:embed index.html app.js style.css`)
- Modify: `internal/server/server.go` — serve `UI` at `/`

Screens (hash-routed, vanilla JS, `fetch` against `/v0`):
- `#/` site list with name, domain, last published, "published elsewhere" badge, New site, Adopt.
- `#/site/<id>` post grid (uses `/<id>/planet.json` when rendered, else `/v0/.../articles`), New post, Publish button showing progress then CID + gateway link, Settings, Preview iframe of `/<id>/`.
- `#/site/<id>/post/<pid>` editor: title, date, markdown textarea, attachments (multi-file input → `POST .../attachments`), tags (comma list), Save, Delete.
- `#/site/<id>/settings` name, about, domain, avatar upload, template settings rendered from `template.json` `settings` (grouped basic/advanced), custom code, Key export (calls `/v0/croptop/sites/<id>/key`, shows PEM in a textarea with Copy), Sync button.
- `#/adopt` form: name or ENS, PEM textarea → `POST /v0/croptop/adopt`.
- Status bar: kubo peer count from `/v0/info`, keepalive state.

`/v0/croptop/*` handlers: `GET/PUT sites/{id}/settings` (template settings map), `GET sites/{id}/key` (PEM), `POST adopt`, `POST sites/{id}/sync`, `GET template` (template.json), `GET status`.

- [ ] **Step 1: Write the three files**, mobile-first CSS (single column under 700px, grid above), no external assets.
- [ ] **Step 2: Manual check** — `go run ./cmd/croptop serve --data .data` then walk every screen in a browser and once from a phone on the LAN with `--listen 0.0.0.0:8086` after `croptop passcode set`.
- [ ] **Step 3: Commit** — `git commit -am "web: embedded UI"`

---

### Task 10: CLI wiring and config

**Files:**
- Create: `cmd/croptop/main.go`, `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- `type Config struct{ Listen string; PasscodeHash string; KuboBin string; Version string }` in `config.json`; `Load(dir)`, `Save(dir)`; `SetPasscode(plain)` (sha256 with random salt), `CheckPasscode(plain) bool`.
- Subcommands: `serve [--listen] [--data] [--templates] [--no-open]` (default; opens browser via `xdg-open`/`open`/`rundll32 url.dll,FileProtocolHandler`), `import-planet [--container] [--force]`, `adopt <name> --key <file>`, `sync <site>`, `publish <site> [--force]`, `key export <site>`, `key import <site> <file>`, `passcode set`, `version`, `ipfs-smoke`.
- `serve` startup order: config → EnsureKubo → Node.Init → Node.Start → Renderer → Publisher → RunKeepalive goroutine (10 min) → HTTP; SIGINT/SIGTERM → HTTP shutdown → Node.Stop.

- [ ] **Step 1: Failing test** — `SetPasscode("x")` then `CheckPasscode("x")` true, `"y"` false; `Save`/`Load` round trip.
- [ ] **Step 2: Implement** with `flag.NewFlagSet` per subcommand.
- [ ] **Step 3: Run** `go test ./...`, `go vet ./...`, `go build ./...`. **Commit** — `git commit -am "cli: serve, import-planet, adopt, sync, publish, key, passcode"`

---

### Task 11: Cross-compile, CI, release, README

**Files:**
- Create: `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `.goreleaser.yaml`, `README.md`, `LICENSE` (MIT)

- [ ] **Step 1:** `ci.yml`: on push/PR, `actions/checkout` with `submodules: true`, `actions/setup-go` 1.27, `go vet ./...`, `go test ./...`, and a matrix cross-build `GOOS in {darwin,linux,windows} x GOARCH in {amd64,arm64}` with `go build -o /dev/null ./cmd/croptop`.
- [ ] **Step 2:** `.goreleaser.yaml`: builds for the six targets, `ldflags -X main.version={{.Version}}`, archives `tar.gz` (zip for windows), checksums. `release.yml` on `v*` tags.
- [ ] **Step 3:** README: what it is, install per OS (download, `chmod +x`, macOS `xattr -d com.apple.quarantine croptop`), first run, importing from the Mac app, moving to a new machine (key export → adopt), using from a phone (`passcode set`, `--listen 0.0.0.0:8086`, Tailscale note), what's not in v1, developing (submodule, `--templates`).
- [ ] **Step 4:** Local cross-compile check: `for os in darwin linux windows; do for arch in amd64 arm64; do GOOS=$os GOARCH=$arch go build -o /dev/null ./cmd/croptop || exit 1; done; done`.
- [ ] **Step 5: Commit** — `git commit -am "ci, release, readme"`

---

### Task 12: End-to-end on the real library

- [ ] **Step 1:** `go run ./cmd/croptop import-planet --data .data` against the live Croptop container. Expected: 11 sites, keys for each, no errors.
- [ ] **Step 2:** `go run ./cmd/croptop serve --data .data --templates ~/Library/Containers/xyz.planetable.Lite/Data/Documents/Planet/Templates/croptop --no-open`, open `http://127.0.0.1:8086/`, render the CocoPay site, compare `planet.json` and one post's `article.json` and `nft.json.cid.txt` against the Mac app's `Public/` copies. `nft.json.cid.txt` must be identical for every imported post.
- [ ] **Step 3:** Publish one site (the user chooses which) and confirm with `ipfs name resolve` on the new node and on `https://<ipns>.eth.sucks/planet.json` that the CID advanced with sequence > the Mac app's. Then confirm the Mac app's keepalive does not win it back within 15 minutes.
- [ ] **Step 4:** `key export` that site, `adopt` it into a second `--data .data2` on the same machine with a different port set, publish from there, and confirm the first instance flips to "published elsewhere" on its next keepalive.
- [ ] **Step 5:** `gh repo create mejango/croptop --public --source . --push`; push the tag `v0.1.0` and confirm the release workflow produces six archives.
