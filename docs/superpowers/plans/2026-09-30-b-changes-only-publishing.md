# B: Changes-only publishing — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a publish uploads only what changed since the version the host holds. A site's first upload finishes however slow the link is, and resumes after an interruption. So jango.eth (658 MB, a 230 KB/s uplink) gets onto crop.top once, and from then on each publish is the size of the edit.

**Architecture:**
- **Stable renders.** Two renders of the same content are byte-identical, so the client can diff its new version against the one the host holds, folder by folder, using CIDs. The diff produces uploaded files plus a *manifest* that carries the largest unchanged files and folders. Hosts rebuild the version from the parent and check the result against the signed CID: the Go host does a real rebuild, and the Worker records a carry map.
- **Publish order.** A changes-only publish pushes first, as a compare-and-swap on the parent, and announces after. If an agent posted meanwhile, the client takes that version in and tries again.
- **Background uploads.** First and full uploads run in the background, one per site, newest version winning. They read the version from the engine's blocks, have no overall deadline, and resume from what the host already holds.

**Tech Stack:** Go 1.27 (boxo v0.42.2, go-libp2p-kad-dht v0.42.1), Cloudflare Worker (JS, R2, KV), wrangler 4.145.0, Railway, the macOS app (Swift).

**Spec:** `docs/superpowers/specs/2026-09-30-scaling-and-agent-economy-design.md`, section B. The order changed on 2026-09-30, so B ships before A: jango.eth has never reached crop.top, because every full upload hits today's flat 10-minute limit. Three additions go beyond the spec's text, and each serves that case:
- an upload is given up only when it stops making progress;
- an interrupted upload resumes;
- background uploads read the version's blocks instead of the live folder, so a render during a 48-minute upload cannot corrupt it.

The Save & publish fix already on main (a976f85) ships with this release.

## Global Constraints
- Manifest push: the host advertises `acceptsManifest`. The client uploads only files whose CID differs from the parent, plus the folder blocks, plus a `manifest` part `{"carry": ["assets", "<post-id>", "<post-id>/photo.jpg", …]}`. Each entry names a file or a whole folder to take from the parent. The new version is exactly the uploaded files plus the carried entries: anything neither uploaded nor carried is gone, so deletions work.
- A carried path missing from the parent is a `400`. So is a carried path inside another carried path, an uploaded file inside a carried folder or equal to a carried path, and a manifest without a parent. This applies on the Go host and the Worker alike.
- Pushes without a manifest keep their meaning: no parent means a full push; a parent without a manifest means "the parent plus these files" (0.13.16 and 0.13.17 agents).
- The client refuses to push changes-only unless its own rebuild of (parent + uploads + carry) gives the version's CID. If that check fails it falls back to a full push.
- `build_timestamp` = the first 6 bytes of a SHA-256 over template assets, `avatar.png`, `favicon.ico` and `templateSettings.json`, written as a decimal number (under 2^53). RSS dates are written in UTC. A render skips copying an attachment whose destination has the same size and modification time, and copies keep the source's modification time.
- Publish order: when changes-only is possible, push first (compare-and-swap on the parent), then announce *the same signed record*. On `409`, take that version in, re-render, and retry, at most twice. If the host is unreachable, announce and push later. First and full pushes announce first and upload in the background.
- No push has an overall deadline. A request is cancelled only when its body makes no progress for 2 minutes, or when the host takes over 5 minutes to answer after the body is sent. Uploads with the embedded engine read the version from its blocks, never from the live public folder.
- Go tests run serially: `go test -p 1 ./...` (packages share a kubo repo lock).
- Worker tests: `cd worker && node --experimental-loader ./text-loader.mjs --test test/`.
- wrangler: `npx -y -p node@22 -p wrangler@4.145.0 wrangler …`.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Each release bumps `installer/macos-build-number`. Nothing is committed in `app/` while `installer/release-macos.sh` runs.

## Review Focus
1. **A long first upload on a slow link.** 658 MB at 230 KB/s must finish (no flat cap) and survive the laptop sleeping (it resumes). It must never send files rendered after the version was made. (Task 5: the watchdog, held files, and reading from version blocks; tests cover a stalled and a slow-but-moving body, a resumed push, and a folder edited mid-push.)
2. **An agent posts while the laptop publishes.** The agent's post must never be dropped. (Task 7: the compare-and-swap push gets `409`, the client takes the version in and retries; tested.)
3. **A deleted post.** It must be gone from the next version on crop.top and from the file list sent to the node. (Task 4: `404` plus the node's file list; Task 3: the Go host's rebuild excludes it.)
4. **A diff that would not rebuild the version.** Causes include a bug, missing blocks, or a sharded parent that isn't on this machine. Such a diff is never pushed changes-only; the client sends the whole site. (Task 6/7: `CheckManifest` runs before every changes-only push; tested with a wrong carry list.)
5. **Older clients against the new hosts.** A parent push with no manifest keeps working. (Tasks 3 and 4: the existing parent-push tests stay green.)

---

### Task 1: Stable renders

**Files:**
- Modify: `internal/render/render.go` (`baseContext` at about line 292; `copyFile` at about line 613)
- Modify: `internal/render/rss.go` (the `rfc822` filter at about line 30)
- Test: `internal/render/render_test.go`

**Interfaces:**
- Produces: unexported `buildStamp(pub string) int64`. `copyFile` keeps its signature.

- [ ] **Step 1: Write the failing test** (append to `internal/render/render_test.go`; add `bytes`, `strings` and `time` to the imports if missing)

```go
// Two renders of the same site give the same bytes whatever the clock, RSS
// dates do not depend on the machine's time zone, and an attachment already
// in place is not copied again.
func TestRendersAreStable(t *testing.T) {
	r, s := fixtureRenderer(t)
	ctx := context.Background()
	zone := time.Local
	t.Cleanup(func() { time.Local = zone })
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(s.PublicDir(fixtureID), name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	posts, err := s.Posts(fixtureID)
	if err != nil || len(posts) == 0 {
		t.Fatal("fixture has no posts", err)
	}
	p := posts[0]
	src := filepath.Join(s.PostDir(fixtureID, p.ID), "big.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	p.Attachments = append(p.Attachments, "big.bin")
	if err := s.SavePost(fixtureID, p); err != nil {
		t.Fatal(err)
	}

	time.Local = time.FixedZone("BRT", -3*3600)
	if err := r.Render(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	index, rss := read("index.html"), read("rss.xml")
	dst := filepath.Join(s.PublicDir(fixtureID), p.ID, "big.bin")
	si, _ := os.Stat(src)
	di, err := os.Stat(dst)
	if err != nil || !di.ModTime().Equal(si.ModTime()) {
		t.Fatalf("the copy of an attachment must keep its modification time: %v", err)
	}
	if err := os.Chmod(dst, 0o444); err != nil { // a second copy would fail to open it for writing
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dst, 0o644) })

	time.Sleep(1100 * time.Millisecond) // the old build_timestamp was the second of the render
	if err := r.Render(ctx, fixtureID); err != nil {
		t.Fatalf("an attachment already in place was copied again: %v", err)
	}
	if !bytes.Equal(index, read("index.html")) {
		t.Fatal("index.html changed between two renders of the same site")
	}
	time.Local = time.FixedZone("JST", 9*3600)
	if err := r.Render(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rss, read("rss.xml")) {
		t.Fatal("rss.xml changed with the machine's time zone")
	}
	if strings.Contains(string(rss), "-0300") {
		t.Fatal("rss.xml dates are not in UTC")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -p 1 ./internal/render/ -run TestRendersAreStable -count=1`
Expected: FAIL. The first failure is about the modification time of the attachment copy. After that is fixed: "index.html changed between two renders". After that: "rss.xml changed with the machine's time zone".

- [ ] **Step 3: Implement**

In `internal/render/render.go`, in `baseContext`'s `ctx` map, replace `"build_timestamp":       time.Now().Unix(),` with:

```go
		"build_timestamp":       buildStamp(pub),
```

Add, next to `sha256hex`:

```go
// buildStamp stands in for a build time: templates add it to asset URLs to
// bust caches. It hashes what those URLs point at (template assets, avatar,
// favicon, template settings), so two renders of the same site give the same
// bytes on any machine. Six bytes keep it under 2^53 for templates that do
// arithmetic on it.
func buildStamp(pub string) int64 {
	var paths []string
	filepath.WalkDir(filepath.Join(pub, "assets"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	paths = append(paths, filepath.Join(pub, "avatar.png"), filepath.Join(pub, "favicon.ico"), filepath.Join(pub, "templateSettings.json"))
	h := sha256.New()
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(pub, p)
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(b))
		h.Write(b)
	}
	var n int64
	for _, b := range h.Sum(nil)[:6] {
		n = n<<8 | int64(b)
	}
	return n
}
```

Replace `copyFile` with:

```go
// copyFile copies src to dst unless dst already has src's size and
// modification time. The copy keeps src's modification time, so the next
// render skips it: big attachments are not copied again on every render.
func copyFile(src, dst string) error {
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	if di, err := os.Stat(dst); err == nil && di.Size() == si.Size() && di.ModTime().Equal(si.ModTime()) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, si.ModTime(), si.ModTime())
}
```

Add `io/fs` and `sort` to render.go's imports if missing, and drop `time` if nothing else uses it. In `internal/render/rss.go`, in the `rfc822` filter, change `time.Unix(u, 0).Local().Format(` to `time.Unix(u, 0).UTC().Format(`.

- [ ] **Step 4: Run the tests**

Run: `go test -p 1 ./internal/render/ -count=1`
Expected: PASS (the fixture comparisons do not involve `build_timestamp`).

- [ ] **Step 5: Commit**

```bash
git add internal/render/render.go internal/render/rss.go internal/render/render_test.go
git commit -m "Renders of the same site give the same bytes

build_timestamp hashes what it busts caches for instead of reading the clock,
RSS dates are UTC, and attachments already in place are not copied again.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Engine: rebuild a manifest version, read a version's files, announce a signed record

**Files:**
- Modify: `internal/ipfs/embedded_unixfs.go` (add after `PutBlock`)
- Modify: `internal/ipfs/embedded_ipns.go` (`NamePublish` at about line 52)
- Test: `internal/ipfs/embedded_test.go`

**Interfaces:**
- Produces (all on `*Embedded`):
  - `Rebuild(ctx context.Context, parent, dir string, carry []string) (string, error)` — the host side. The uploads are every file under `dir`.
  - `CheckManifest(ctx context.Context, root, parent string, upload, carry []string) error` — the client side. Uploaded entries are taken from `root`'s blocks, carried ones from `parent`'s folders. Only links are needed, never file data.
  - `type VersionFile struct { Path string; Size int64; CID string }`
  - `Files(ctx context.Context, root string) ([]VersionFile, error)` — every file of a version, sorted by path.
  - `OpenFile(ctx context.Context, root, rel string) (io.ReadSeekCloser, int64, error)`
  - `Announce(ctx context.Context, key, c string, rec []byte) error` — `NamePublish` after the signing.

- [ ] **Step 1: Write the failing tests** (append to `internal/ipfs/embedded_test.go`; add imports `io`, `fmt`, `strings` if missing; `merkledag` is `github.com/ipfs/boxo/ipld/merkledag`, `ft` is `github.com/ipfs/boxo/ipld/unixfs`)

```go
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A manifest rebuild gives the same root as adding the whole new tree, which
// is how a host checks a changes-only push; the client's check agrees; and a
// version's files can be listed and read back from its blocks alone.
func TestRebuildMatchesAFullAdd(t *testing.T) {
	ctx := context.Background()
	e := NewEmbedded(t.TempDir())
	e.Offline = true
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	parent, err := e.AddDir(ctx, writeTree(t, map[string]string{"index.html": "home", "assets/site.css": "css", "assets/font.woff": "font", "p1/index.html": "one", "p1/photo.jpg": "photo", "p2/index.html": "two"}))
	if err != nil {
		t.Fatal(err)
	}
	// the new version: the home page and p1's page changed, p2 deleted, p3 new
	want, err := e.AddDir(ctx, writeTree(t, map[string]string{"index.html": "home 2", "assets/site.css": "css", "assets/font.woff": "font", "p1/index.html": "one 2", "p1/photo.jpg": "photo", "p3/index.html": "three"}))
	if err != nil {
		t.Fatal(err)
	}
	upload, carry := []string{"index.html", "p1/index.html", "p3/index.html"}, []string{"assets", "p1/photo.jpg"}
	got, err := e.Rebuild(ctx, parent, writeTree(t, map[string]string{"index.html": "home 2", "p1/index.html": "one 2", "p3/index.html": "three"}), carry)
	if err != nil || got != want {
		t.Fatalf("rebuild gave %s, %v; adding the whole tree gives %s", got, err, want)
	}
	if err := e.CheckManifest(ctx, want, parent, upload, carry); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := e.CheckManifest(ctx, want, parent, upload, []string{"assets"}); err == nil {
		t.Fatal("a manifest that drops p1/photo.jpg must not check out")
	}
	ups := writeTree(t, map[string]string{"p1/index.html": "x"})
	for _, bad := range [][]string{{"nope"}, {"p1"}, {"assets", "assets/site.css"}, {"../x"}, {""}} {
		if _, err := e.Rebuild(ctx, parent, ups, bad); err == nil {
			t.Fatalf("carry %q must be refused", bad)
		}
	}
	files, err := e.Files(ctx, want)
	if err != nil || len(files) != 6 || files[0].Path != "assets/font.woff" || files[0].Size != 4 || files[0].CID == "" {
		t.Fatalf("files: %v, %v", files, err)
	}
	r, size, err := e.OpenFile(ctx, want, "p1/index.html")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	r.Close()
	if string(b) != "one 2" || size != 5 {
		t.Fatalf("read back %q (%d)", b, size)
	}
}

// Big folders are sharded (HAMT); rebuilding one from carried names and a few
// uploads still matches a full add.
func TestRebuildMatchesAFullAddOfAShardedFolder(t *testing.T) {
	ctx := context.Background()
	e := NewEmbedded(t.TempDir())
	e.Offline = true
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	name := func(i int) string { return fmt.Sprintf("post-%04d-%s.html", i, strings.Repeat("a", 28)) }
	before, after := map[string]string{}, map[string]string{}
	for i := 0; i < 4000; i++ {
		before[name(i)] = fmt.Sprint(i)
		after[name(i)] = fmt.Sprint(i)
	}
	after[name(7)] = "changed"
	delete(after, name(8))
	after["new.html"] = "new"
	parent, err := e.AddDir(ctx, writeTree(t, before))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := e.Block(ctx, parent)
	nd, _ := merkledag.DecodeProtobuf(b)
	if fsn, err := ft.FSNodeFromBytes(nd.Data()); err != nil || fsn.Type() != ft.THAMTShard {
		t.Fatal("the folder is not sharded; raise the count or the name length")
	}
	want, err := e.AddDir(ctx, writeTree(t, after))
	if err != nil {
		t.Fatal(err)
	}
	var carry []string
	for n := range after {
		if n != name(7) && n != "new.html" {
			carry = append(carry, n)
		}
	}
	got, err := e.Rebuild(ctx, parent, writeTree(t, map[string]string{name(7): "changed", "new.html": "new"}), carry)
	if err != nil || got != want {
		t.Fatalf("rebuild gave %s, %v; adding the whole tree gives %s", got, err, want)
	}
	if err := e.CheckManifest(ctx, want, parent, []string{name(7), "new.html"}, carry); err != nil {
		t.Fatalf("check: %v", err)
	}
}

// A record signed first and announced later is what the network then holds.
func TestAnnounceASignedRecord(t *testing.T) {
	ctx := context.Background()
	e := NewEmbedded(t.TempDir())
	e.Offline = true
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	name, _ := e.Keystore().Generate("s")
	const c = "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4"
	rec, err := e.SignRecord("s", c, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Announce(ctx, "s", c, rec); err != nil {
		t.Fatal(err)
	}
	got, err := e.GetRecord(ctx, name)
	if err != nil || string(got) != string(rec) {
		t.Fatalf("the network holds another record: %v", err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -p 1 ./internal/ipfs/ -run 'TestRebuild|TestAnnounce' -count=1`
Expected: FAIL to compile (`e.Rebuild`, `e.CheckManifest`, `e.Files`, `e.OpenFile`, `e.Announce` undefined).

- [ ] **Step 3: Implement the UnixFS side** (append to `internal/ipfs/embedded_unixfs.go`; add imports `errors`, `io`, `io/fs`, `strings` if missing)

```go
// Rebuild adds the files under dir on top of the paths carry names in parent:
// the version a manifest push describes. What is neither uploaded nor carried
// is not in it. A host checks a push by comparing the result with the CID the
// push was signed for. Only parent's folders on the carried paths are read.
func (e *Embedded) Rebuild(ctx context.Context, parent, dir string, carry []string) (string, error) {
	if e.dag == nil {
		return "", fmt.Errorf("node not started")
	}
	pid, err := cid.Decode(parent)
	if err != nil {
		return "", err
	}
	f := &folders{dag: e.dag, cache: map[cid.Cid]map[string]*ipld.Link{}}
	t, err := f.carried(ctx, pid, carry)
	if err != nil {
		return "", err
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		return t.put(filepath.ToSlash(rel), &tree{file: p})
	})
	if err != nil {
		return "", err
	}
	nd, err := t.build(ctx, e.dag)
	if err != nil {
		return "", err
	}
	return nd.Cid().String(), nil
}

// CheckManifest says whether a manifest push of upload and carry on parent
// makes exactly the version root. The uploaded entries are linked from root's
// own folders, so no file data is read, and carried ones need only parent's
// folders. A client checks this before pushing, as the host will after.
func (e *Embedded) CheckManifest(ctx context.Context, root, parent string, upload, carry []string) error {
	if e.dag == nil {
		return fmt.Errorf("node not started")
	}
	rid, err := cid.Decode(root)
	if err != nil {
		return err
	}
	pid, err := cid.Decode(parent)
	if err != nil {
		return err
	}
	f := &folders{dag: e.dag, cache: map[cid.Cid]map[string]*ipld.Link{}}
	t, err := f.carried(ctx, pid, carry)
	if err != nil {
		return err
	}
	for _, rel := range upload {
		parts, err := splitPath(rel)
		if err != nil {
			return err
		}
		l, err := f.link(ctx, rid, parts)
		if err != nil {
			return fmt.Errorf("uploaded %q: %w", rel, err)
		}
		if err := t.put(rel, &tree{node: linkOnly{l.Cid, l.Size}}); err != nil {
			return err
		}
	}
	nd, err := t.build(ctx, memoryDAG())
	if err != nil {
		return err
	}
	if nd.Cid() != rid {
		return fmt.Errorf("the manifest makes %s, not %s", nd.Cid(), root)
	}
	return nil
}

// VersionFile is one file of a version.
type VersionFile struct {
	Path string
	Size int64
	CID  string
}

// Files lists every file of the version root, sorted by path, from this
// node's blocks.
func (e *Embedded) Files(ctx context.Context, root string) ([]VersionFile, error) {
	if e.dag == nil {
		return nil, fmt.Errorf("node not started")
	}
	rid, err := cid.Decode(root)
	if err != nil {
		return nil, err
	}
	var out []VersionFile
	var walk func(rel string, c cid.Cid, tsize uint64) error
	walk = func(rel string, c cid.Cid, tsize uint64) error {
		if c.Type() == cid.Raw { // a small file: one block of raw bytes, sized by its link
			out = append(out, VersionFile{rel, int64(tsize), c.String()})
			return nil
		}
		nd, err := e.dag.Get(ctx, c)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		pn, ok := nd.(*merkledag.ProtoNode)
		if !ok {
			return fmt.Errorf("%s: not a unixfs node", rel)
		}
		fsn, err := ft.FSNodeFromBytes(pn.Data())
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		switch fsn.Type() {
		case ft.TFile, ft.TRaw:
			out = append(out, VersionFile{rel, int64(fsn.FileSize()), c.String()})
			return nil
		case ft.TDirectory, ft.THAMTShard:
			d, err := uio.NewDirectoryFromNode(e.dag, nd)
			if err != nil {
				return err
			}
			links, err := d.Links(ctx)
			if err != nil {
				return err
			}
			for _, l := range links {
				if err := walk(joinRel(rel, l.Name), l.Cid, l.Size); err != nil {
					return err
				}
			}
			return nil
		}
		return fmt.Errorf("%s: unsupported unixfs node", rel)
	}
	if err := walk("", rid, 0); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// OpenFile reads the file at rel in version root from this node's blocks, so
// an upload sends the version's own bytes even if the files on disk have
// changed since it was added.
func (e *Embedded) OpenFile(ctx context.Context, root, rel string) (io.ReadSeekCloser, int64, error) {
	if e.dag == nil {
		return nil, 0, fmt.Errorf("node not started")
	}
	rid, err := cid.Decode(root)
	if err != nil {
		return nil, 0, err
	}
	parts, err := splitPath(rel)
	if err != nil {
		return nil, 0, err
	}
	f := &folders{dag: e.dag, cache: map[cid.Cid]map[string]*ipld.Link{}}
	l, err := f.link(ctx, rid, parts)
	if err != nil {
		return nil, 0, err
	}
	nd, err := e.dag.Get(ctx, l.Cid)
	if err != nil {
		return nil, 0, err
	}
	r, err := uio.NewDagReader(ctx, nd, e.dag)
	if err != nil {
		return nil, 0, err
	}
	return r, int64(r.Size()), nil
}

// tree is a version being put together. Each name is a node taken as it is
// (carried, or an uploaded file already in the version), a file still to
// add, or a folder of more names.
type tree struct {
	kids map[string]*tree
	node ipld.Node
	file string
}

func (t *tree) leaf() bool { return t.node != nil || t.file != "" }

// put places leaf at rel. No path may sit inside one taken whole, and none
// may be placed twice.
func (t *tree) put(rel string, leaf *tree) error {
	parts, err := splitPath(rel)
	if err != nil {
		return err
	}
	at := t
	for i, name := range parts[:len(parts)-1] {
		next, ok := at.kids[name]
		if !ok {
			next = &tree{kids: map[string]*tree{}}
			at.kids[name] = next
		}
		if next.leaf() {
			return fmt.Errorf("%q is inside %q, which is taken whole", rel, strings.Join(parts[:i+1], "/"))
		}
		at = next
	}
	last := parts[len(parts)-1]
	if _, dup := at.kids[last]; dup {
		return fmt.Errorf("%q is given twice, or holds another given path", rel)
	}
	at.kids[last] = leaf
	return nil
}

// build stores the folders of t in dag, bottom up, and returns the root.
// dag-pb sorts links when it encodes, and sharding does not depend on order,
// so the root is the CID an add of the same tree gives.
func (t *tree) build(ctx context.Context, dag ipld.DAGService) (ipld.Node, error) {
	d, err := uio.NewDirectory(dag)
	if err != nil {
		return nil, err
	}
	d.SetCidBuilder(v1Builder)
	names := make([]string, 0, len(t.kids))
	for name := range t.kids {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		k := t.kids[name]
		nd := k.node
		switch {
		case nd != nil:
		case k.file != "":
			nd, err = addFile(ctx, dag, k.file, true, v1Builder)
		default:
			nd, err = k.build(ctx, dag)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := d.AddChild(ctx, name, nd); err != nil {
			return nil, err
		}
	}
	nd, err := d.GetNode()
	if err != nil {
		return nil, err
	}
	return nd, dag.Add(ctx, nd)
}

// folders reads the folders of versions once each, keeping their links by name.
type folders struct {
	dag   ipld.DAGService
	cache map[cid.Cid]map[string]*ipld.Link
}

// link is the link to the entry at parts under the folder root, reading only
// the folders on the way.
func (f *folders) link(ctx context.Context, root cid.Cid, parts []string) (*ipld.Link, error) {
	at := root
	var l *ipld.Link
	for i, name := range parts {
		m, ok := f.cache[at]
		if !ok {
			nd, err := f.dag.Get(ctx, at)
			if err != nil {
				return nil, err
			}
			d, err := uio.NewDirectoryFromNode(f.dag, nd)
			if err != nil {
				return nil, fmt.Errorf("%q is not a folder", strings.Join(parts[:i], "/"))
			}
			links, err := d.Links(ctx)
			if err != nil {
				return nil, err
			}
			m = make(map[string]*ipld.Link, len(links))
			for _, x := range links {
				m[x.Name] = x
			}
			f.cache[at] = m
		}
		if l = m[name]; l == nil {
			return nil, fs.ErrNotExist
		}
		at = l.Cid
	}
	return l, nil
}

// carried starts a version from the paths of parent a manifest carries.
func (f *folders) carried(ctx context.Context, parent cid.Cid, carry []string) (*tree, error) {
	t := &tree{kids: map[string]*tree{}}
	for _, rel := range carry {
		parts, err := splitPath(rel)
		if err != nil {
			return nil, err
		}
		l, err := f.link(ctx, parent, parts)
		if err != nil {
			return nil, fmt.Errorf("carried %q is not in the parent: %w", rel, err)
		}
		if err := t.put(rel, &tree{node: linkOnly{l.Cid, l.Size}}); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// linkOnly is a node known only by its CID and size, which is all a folder
// needs to link it: a carried file or folder need not be on this machine.
type linkOnly struct {
	c    cid.Cid
	size uint64
}

var errLinkOnly = errors.New("only the link of this node is here")

func (n linkOnly) Cid() cid.Cid                                     { return n.c }
func (n linkOnly) Size() (uint64, error)                            { return n.size, nil }
func (n linkOnly) RawData() []byte                                  { return nil }
func (n linkOnly) String() string                                   { return n.c.String() }
func (n linkOnly) Loggable() map[string]interface{}                 { return map[string]interface{}{"cid": n.c} }
func (n linkOnly) Resolve([]string) (interface{}, []string, error)  { return nil, nil, errLinkOnly }
func (n linkOnly) Tree(string, int) []string                        { return nil }
func (n linkOnly) ResolveLink([]string) (*ipld.Link, []string, error) { return nil, nil, errLinkOnly }
func (n linkOnly) Copy() ipld.Node                                  { return n }
func (n linkOnly) Links() []*ipld.Link                              { return nil }
func (n linkOnly) Stat() (*ipld.NodeStat, error)                    { return &ipld.NodeStat{CumulativeSize: int(n.size)}, nil }

// splitPath splits a slash path inside a version into names, refusing any
// that could climb out of it.
func splitPath(rel string) ([]string, error) {
	parts := strings.Split(strings.Trim(rel, "/"), "/")
	for _, s := range parts {
		if s == "" || s == "." || s == ".." {
			return nil, fmt.Errorf("bad path %q", rel)
		}
	}
	return parts, nil
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}
```

- [ ] **Step 4: Split `NamePublish`** (in `internal/ipfs/embedded_ipns.go`)

Replace the body of `NamePublish` from `_, name, _ := e.siteKey(key)` to the end of the function with `return e.Announce(ctx, key, c, b)`, and add:

```go
// Announce sends a record SignRecord made to the network (DHT, pubsub, the
// routing endpoints), reads it back, and provides the version: the second
// half of NamePublish, for a version a host received before the network.
func (e *Embedded) Announce(ctx context.Context, key, c string, rec []byte) error {
	if e.dht == nil {
		return fmt.Errorf("node not started")
	}
	_, name, err := e.siteKey(key)
	if err != nil {
		return err
	}
	id, err := cid.Decode(c)
	if err != nil {
		return err
	}
	if err := e.putRecord(ctx, name, rec); err != nil {
		return err
	}
	if !e.Offline {
		// read our own record back from the network as a check
		if r, err := e.NetworkRecord(ctx, name.String()); err == nil {
			e.Log(fmt.Sprintf("ipns readback: network now has sequence %d -> %s", r.Sequence, r.Value))
		} else {
			e.Log("ipns readback: " + err.Error())
		}
	}
	go func() {
		pctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := e.dht.Provide(pctx, id, true); err != nil {
			e.Log("provide " + c + ": " + err.Error())
		}
	}()
	return nil
}
```

- [ ] **Step 5: Run the tests**

Run: `gofmt -l internal/ipfs && go vet ./internal/ipfs/ && go test -p 1 ./internal/ipfs/ -count=1`
Expected: `gofmt` prints nothing, and all tests pass. If the sharded test reports "the folder is not sharded", raise the count, keeping the test meaningful.

- [ ] **Step 6: Commit**

```bash
git add internal/ipfs/embedded_unixfs.go internal/ipfs/embedded_ipns.go internal/ipfs/embedded_test.go
git commit -m "Engine rebuilds manifest versions and reads versions from blocks

Rebuild and CheckManifest make the version a changes-only push describes
(uploads plus carried paths, nothing else) and match a full add, sharded
folders included. Files and OpenFile read a version from its blocks;
Announce sends a record signed earlier.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Go host: manifest pushes and what an unfinished push left

**Files:**
- Modify: `internal/host/host.go`:
  - `Start` at about line 89;
  - the keys response at about line 455;
  - `serveAPI`: add the `versions/` case;
  - `push` at about line 620;
  - `saveMultipart` at about line 757;
  - `reserved` at about line 78.
- Test: `internal/host/host_test.go`

**Interfaces:**
- Consumes: `(*ipfs.Embedded).Rebuild(ctx, parent, dir string, carry []string) (string, error)` (Task 2).
- Produces:
  - `GET /v0/host/keys/<ipns>` gains `"acceptsManifest": true`.
  - `POST /v0/host/push` takes a `manifest` form field in its final part.
  - `GET /v0/host/versions/<cid>/files` → `[{"path": "...", "size": N}]`, the files a push of `<cid>` has staged but not committed.

- [ ] **Step 1: Write the failing test** (append to `internal/host/host_test.go`; add imports as needed: `bytes`, `mime/multipart`, `path`, `encoding/json`)

First read the existing signed-push test around lines 90-110 and copy exactly how it sets `Host` and the signing host. The helper below assumes the signature covers `crop.test` and the request's `Host` is `crop.test`. Adjust the two marked lines if the existing test does it differently.

```go
// A manifest push is rebuilt from the parent the host holds plus the uploaded
// files: carried paths stay, everything else is gone, and the result must
// hash to the signed CID. Bad manifests are refused, and a push cut short can
// see what it already left here.
func TestManifestPush(t *testing.T) {
	ctx := context.Background()
	h := &Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offline(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	site := offline(t)
	ipnsName, _ := site.Keystore().Generate("site1")
	tree := func(files map[string]string) (string, string) {
		dir := t.TempDir()
		for rel, body := range files {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(body), 0o644)
		}
		c, err := site.AddDir(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		return dir, c
	}
	push := func(c string, seq uint64, parent, part string, files map[string]string, manifest string) (int, string) {
		now := time.Now().Unix()
		sig, _ := site.Keystore().Sign("site1", PushMessage("crop.test", ipnsName, c, seq, now)) // signing host, as the existing test
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for rel, body := range files {
			fw, _ := mw.CreateFormFile("file:"+rel, path.Base(rel))
			fw.Write([]byte(body))
		}
		if manifest != "" {
			mw.WriteField("manifest", manifest)
		}
		mw.Close()
		req, _ := http.NewRequest("POST", srv.URL+"/v0/host/push", &buf)
		req.Host = "crop.test" // as the existing test
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-Croptop-Ipns", ipnsName)
		req.Header.Set("X-Croptop-Cid", c)
		req.Header.Set("X-Croptop-Seq", strconv.FormatUint(seq, 10))
		req.Header.Set("X-Croptop-Time", strconv.FormatInt(now, 10))
		req.Header.Set("X-Croptop-Sig", base64.StdEncoding.EncodeToString(sig))
		if parent != "" {
			req.Header.Set("X-Croptop-Parent", parent)
		}
		if part != "" {
			req.Header.Set("X-Croptop-Part", part)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	v1Files := map[string]string{"index.html": "home", "assets/a.css": "css", "p1/index.html": "one", "p1/photo.jpg": "photo", "p2/index.html": "two"}
	_, v1 := tree(v1Files)
	_, v2 := tree(map[string]string{"index.html": "home 2", "assets/a.css": "css", "p1/index.html": "one", "p1/photo.jpg": "photo", "p3/index.html": "three"})
	if code, body := push(v1, 1, "", "", v1Files, ""); code != 200 {
		t.Fatalf("v1: %d %s", code, body)
	}
	if code, body := push(v2, 2, v1, "", map[string]string{"index.html": "home 2", "p3/index.html": "three"}, `{"carry":["assets","p1"]}`); code != 200 {
		t.Fatalf("manifest push: %d %s", code, body)
	}
	h.mu.Lock()
	held := h.reg.Keys[ipnsName].CID
	h.mu.Unlock()
	if held != v2 { // v2 has no p2: the rebuild left it out
		t.Fatalf("host holds %s, want %s", held, v2)
	}
	_, v3 := tree(map[string]string{"index.html": "home 3"})
	for _, bad := range []struct{ parent, manifest string }{
		{v2, `{"carry":["nope"]}`},             // not in the parent
		{v2, `{"carry":["index.html"]}`},       // uploaded and carried
		{v2, `{"carry":["p1","p1/photo.jpg"]}`}, // one carried path inside another
		{"", `{"carry":[]}`},                    // no parent
	} {
		if code, body := push(v3, 3, bad.parent, "", map[string]string{"index.html": "home 3"}, bad.manifest); code != 400 {
			t.Fatalf("manifest %s on %q: %d %s, want 400", bad.manifest, bad.parent, code, body)
		}
	}
	resp, err := http.Get(srv.URL + "/v0/host/keys/" + ipnsName)
	if err != nil {
		t.Fatal(err)
	}
	var entry struct {
		AcceptsManifest bool `json:"acceptsManifest"`
	}
	json.NewDecoder(resp.Body).Decode(&entry)
	resp.Body.Close()
	if !entry.AcceptsManifest {
		t.Fatal("keys must advertise acceptsManifest")
	}
	// part 1 of 2 of a version stays staged; the listing shows it
	_, v4 := tree(map[string]string{"a.txt": "aaaa", "b.txt": "b"})
	if code, body := push(v4, 4, "", "1/2", map[string]string{"a.txt": "aaaa"}, ""); code != 200 {
		t.Fatalf("part 1: %d %s", code, body)
	}
	resp, err = http.Get(srv.URL + "/v0/host/versions/" + v4 + "/files")
	if err != nil {
		t.Fatal(err)
	}
	var staged []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	json.NewDecoder(resp.Body).Decode(&staged)
	resp.Body.Close()
	if len(staged) != 1 || staged[0].Path != "a.txt" || staged[0].Size != 4 {
		t.Fatalf("staged files: %v", staged)
	}
	if resp, err := http.Get(srv.URL + "/v0/host/versions/not-a-cid/files"); err != nil || resp.StatusCode != 400 {
		t.Fatalf("a bad cid must be refused: %v %v", resp, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -p 1 ./internal/host/ -run TestManifestPush -count=1`
Expected: FAIL. The manifest push gets 409 or a hash mismatch, because the manifest is ignored and the files are added over the parent, which keeps p2.

- [ ] **Step 3: Implement**

`saveMultipart` returns the manifest field too. Change its signature to `func saveMultipart(r *http.Request, dir string) (int, string, error)`, declare `var manifest string` before the loop, make every `return n, …` return `n, manifest, …`, and at the top of the loop body, before the `file:` check, add:

```go
		if part.FormName() == "manifest" {
			b, err := io.ReadAll(io.LimitReader(part, 8<<20))
			if err != nil {
				return n, manifest, err
			}
			manifest = string(b)
			continue
		}
```

In `push`, change `n, err := saveMultipart(r, stage)` to `n, manifest, err := saveMultipart(r, stage)`. Then replace the block from `defer os.RemoveAll(stage)` through the `got, err = h.Engine.AddDir(...)` `else` branch, ending before `if err != nil { http.Error(w, "adding files: "...`, with:

```go
	var carry []string
	if manifest != "" {
		var m struct {
			Carry []string `json:"carry"`
		}
		if err := json.Unmarshal([]byte(manifest), &m); err != nil || parent == "" {
			http.Error(w, "a manifest needs a parent and a carry list", 400)
			return
		}
		carry = append([]string{}, m.Carry...)
	}
	defer os.RemoveAll(stage)
	var got string
	switch {
	case carry != nil:
		// the version is the uploaded files plus the carried paths: nothing else
		if got, err = h.Engine.Rebuild(r.Context(), parent, stage, carry); err != nil {
			http.Error(w, "manifest: "+err.Error(), 400)
			return
		}
		if got != c {
			http.Error(w, fmt.Sprintf("the files and carried paths make %s, not %s", got, c), 400)
			return
		}
	case parent != "":
		got, err = h.Engine.AddOver(r.Context(), parent, stage)
	default:
		got, err = h.Engine.AddDir(r.Context(), stage)
	}
```

In the keys response:

```go
		// acceptsParent: pushes of only what changed are understood here;
		// acceptsManifest: so are pushes that say what to keep, and so delete
		writeJSON(w, 200, struct {
			Entry
			AcceptsParent   bool `json:"acceptsParent"`
			AcceptsManifest bool `json:"acceptsManifest"`
		}{pub, true, true})
```

In `serveAPI`, add a case before the `blocks/` case:

```go
	case strings.HasPrefix(p, "versions/") && strings.HasSuffix(p, "/files") && r.Method == "GET":
		// what an unfinished push of this version left here, so its retry
		// sends only the rest
		c := strings.TrimSuffix(strings.TrimPrefix(p, "versions/"), "/files")
		if !cidRe.MatchString(c) {
			http.Error(w, "bad cid", 400)
			return
		}
		writeJSON(w, 200, stagedFiles(filepath.Join(h.DataDir, "host", "staging", c)))
```

Add, near `nameRe`:

```go
var cidRe = regexp.MustCompile(`^(bafy|Qm)[a-zA-Z0-9]+$`)

type stagedFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// stagedFiles lists the files under dir, as the push that put them there
// named them.
func stagedFiles(dir string) []stagedFile {
	out := []stagedFile{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, stagedFile{filepath.ToSlash(rel), info.Size()})
		return nil
	})
	return out
}
```

In `Start`, before `return nil`, clear out stale staging dirs. A version pushed halfway and then superseded would otherwise keep its files forever:

```go
	// a push left unfinished two days ago will not be resumed
	stage := filepath.Join(h.DataDir, "host", "staging")
	if ents, err := os.ReadDir(stage); err == nil {
		for _, ent := range ents {
			if info, err := ent.Info(); err == nil && time.Since(info.ModTime()) > 48*time.Hour {
				os.RemoveAll(filepath.Join(stage, ent.Name()))
			}
		}
	}
```

Add `"agents": true` to the `reserved` map. The Worker serves `/agents.md` on the bare domain; see Task 4. Add `io/fs` to host.go's imports if missing.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal && go vet ./internal/host/ && go test -p 1 ./internal/host/ ./internal/publish/ -count=1 -timeout 10m`
Expected: `gofmt` prints at most `internal/update/update.go` (pre-existing), and all tests pass. That includes the existing parent-push tests (Review Focus 5).

- [ ] **Step 5: Commit**

```bash
git add internal/host/host.go internal/host/host_test.go
git commit -m "Host takes manifest pushes and lists what an unfinished push left

A manifest push is rebuilt from the parent plus the uploads and must hash
to the signed CID; carried paths missing from the parent, overlaps and
manifests without a parent are refused. GET /v0/host/versions/<cid>/files
lets a retry skip what is already here.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Worker: manifest pushes, what an unfinished push left, and /agents.md

**Files:**
- Modify: `worker/src/index.js`:
  - imports at the top;
  - `RESERVED`, line 11;
  - `serveBare`, the `install.sh` lines at about 55;
  - the `api` keys case at about line 401 and a new `versions/` case;
  - `push` at about line 469;
  - add `saveManifestCarry` and `held` near `saveCarry`.
- Modify: `worker/wrangler.toml` (the `rules` Text globs)
- Modify: `worker/text-loader.mjs` (the extension regex)
- Test: `worker/test/push.test.mjs` (the `r2()` mock's `list` and two new tests)

**Interfaces:**
- Produces:
  - `acceptsManifest: true` on keys entries;
  - a `manifest` form field honored in the final part of a push;
  - `GET /v0/host/versions/<cid>/files` → `[{path, size}]`;
  - `GET /agents.md` (and `/agents`) → `docs/agents.md` as `text/markdown`.

- [ ] **Step 1: Write the failing tests**

In `worker/test/push.test.mjs`, make the `r2()` mock's `list` return sizes. Real R2 list objects carry `size`:

```js
      return { objects: keys.slice(at, at + limit).map((key) => ({ key, size: m.get(key).bytes.length })), truncated: at + limit < keys.length, cursor: String(at + limit) };
```

Append:

```js
test("a manifest push keeps only what it uploads and carries", async () => {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const ipns = ipnsName(new Uint8Array(publicKey.export({ format: "der", type: "spki" }).slice(-32)));
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", SITES: r2(), REGISTRY: kv() };
  const waits = [], ctx = { waitUntil: (p) => waits.push(p) };
  const pulls = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init) => { if (String(u).endsWith("/v0/host/pull")) pulls.push(JSON.parse(init.body)); return new Response("{}", { status: 202 }); };
  const call = (url, init) => worker.fetch(new Request(url, init), env, ctx);
  const push = (cid, seq, files, parent, manifest, part) => {
    const t = Math.floor(Date.now() / 1000);
    const sig = sign(null, Buffer.from(`croptop-push\ncrop.test\n${ipns}\n${cid}\n${seq}\n${t}`), privateKey).toString("base64");
    const fd = new FormData();
    for (const [rel, body] of Object.entries(files)) fd.append("file:" + rel, new Blob([body]), rel.split("/").pop());
    if (manifest) fd.append("manifest", JSON.stringify(manifest));
    const headers = { "X-Croptop-Ipns": ipns, "X-Croptop-Cid": cid, "X-Croptop-Seq": String(seq), "X-Croptop-Time": String(t), "X-Croptop-Sig": sig };
    if (parent) headers["X-Croptop-Parent"] = parent;
    if (part) headers["X-Croptop-Part"] = part;
    return call("https://crop.test/v0/host/push", { method: "POST", headers, body: fd });
  };
  const body = async (url) => { const r = await call(url); return [r.status, await r.text()]; };
  try {
    assert.equal((await push("bafyone", 1, { "index.html": "home", "assets/a.css": "css", "p1/index.html": "one", "p1/photo.jpg": "photo", "p2/index.html": "two" })).status, 200);
    const r = await push("bafytwo", 2, { "index.html": "home 2", "p3/index.html": "three" }, "bafyone", { carry: ["assets", "p1"] });
    assert.equal(r.status, 200, await r.text());
    await Promise.all(waits);
    assert.deepEqual(await body("https://bafytwo.crop.test/index.html"), [200, "home 2"]);
    assert.deepEqual(await body("https://bafytwo.crop.test/assets/a.css"), [200, "css"]);
    assert.deepEqual(await body("https://bafytwo.crop.test/p1/photo.jpg"), [200, "photo"]);
    assert.deepEqual(await body("https://bafytwo.crop.test/p3/index.html"), [200, "three"]);
    assert.equal((await call("https://bafytwo.crop.test/p2/index.html")).status, 404, "neither uploaded nor carried: gone");
    assert.deepEqual(pulls.at(-1).files.sort(), ["assets/a.css", "index.html", "p1/index.html", "p1/photo.jpg", "p3/index.html"]);
    const [, entry] = await body(`https://crop.test/v0/host/keys/${ipns}`);
    assert.equal(JSON.parse(entry).acceptsManifest, true);
    // each bad manifest gets its own fake cid: files stored by a refused push stay under it
    for (const [cid, files, parent, manifest] of [
      ["bafythreea", { "index.html": "3" }, "bafytwo", { carry: ["nope"] }],
      ["bafythreeb", { "p1/x.txt": "x" }, "bafytwo", { carry: ["p1"] }],
      ["bafythreec", { "index.html": "3" }, "bafytwo", { carry: ["p1", "p1/photo.jpg"] }],
      ["bafythreed", { "index.html": "3" }, "", { carry: [] }],
    ]) {
      const res = await push(cid, 3, files, parent, manifest);
      assert.equal(res.status, 400, `${JSON.stringify(manifest)}: ${await res.text()}`);
    }
    // part 1 of 2 stays stored, uncommitted; the listing shows it
    assert.equal((await push("bafyfour", 4, { "a.txt": "aaaa" }, "", null, "1/2")).status, 200);
    assert.deepEqual(JSON.parse((await body("https://crop.test/v0/host/versions/bafyfour/files"))[1]), [{ path: "a.txt", size: 4 }]);
    assert.equal((await call("https://crop.test/v0/host/versions/not-a-cid/files")).status, 400);
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("crop.top/agents.md is the instructions to hand a bot", async () => {
  const env = { DOMAIN: "crop.test", SITES: r2(), REGISTRY: kv() };
  for (const p of ["/agents.md", "/agents"]) {
    const r = await worker.fetch(new Request("https://crop.test" + p), env, { waitUntil() {} });
    assert.equal(r.status, 200);
    assert.match(r.headers.get("content-type"), /text\/markdown/);
    assert.match(await r.text(), /croptop post --key/);
  }
});
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd worker && node --experimental-loader ./text-loader.mjs --test test/`
Expected: FAIL. `p2/index.html` is served (200), there is no `acceptsManifest`, the bad manifests return 200, `/versions/` and `/agents.md` don't exist, and the existing tests still pass.

- [ ] **Step 3: Implement**

Add the import next to the install scripts, and extend the Text rule and the loader to `.md`:

```js
import agentsMd from "../../docs/agents.md";
```

- `worker/wrangler.toml`: `rules = [{ type = "Text", globs = ["**/*.sh", "**/*.ps1", "**/*.md"] }]`
- `worker/text-loader.mjs`: `if (/\.(sh|ps1|md)$/.test(url))`

In `RESERVED`, add `"agents", "agents.md"`. In `serveBare`, after the `install.ps1` line:

```js
  if (p === "/agents.md" || p === "/agents") return new Response(agentsMd, { headers: { "content-type": "text/markdown; charset=utf-8", "cache-control": "public, max-age=300" } });
```

In `api`, add `acceptsManifest: true` next to `acceptsParent: true` in the keys case. Add a case before the `blocks/` line:

```js
  if (p.startsWith("versions/") && p.endsWith("/files") && request.method === "GET") {
    const c = p.slice("versions/".length, -"/files".length);
    if (!/^(bafy|Qm)[a-zA-Z0-9]+$/.test(c)) return text("bad cid", 400);
    return json(await held(env, c));
  }
```

In `push`, right after `const form = await request.formData();`:

```js
  const manifest = typeof form.get("manifest") === "string" ? form.get("manifest") : null;
```

and in the `if (final)` block, replace `if (parent) await saveCarry(env, cid, parent);` with:

```js
    if (manifest !== null) {
      let carry;
      try { carry = JSON.parse(manifest).carry || []; } catch { return text("bad manifest", 400); }
      if (!parent || !Array.isArray(carry)) return text("a manifest needs a parent and a carry list", 400);
      const bad = await saveManifestCarry(env, cid, parent, carry);
      if (bad) return text(bad, 400);
    } else if (parent) await saveCarry(env, cid, parent);
```

Add after `saveCarry`:

```js
// saveManifestCarry records a manifest push: the version is exactly its
// uploaded files plus the carried paths, each a file or a whole folder of
// the parent. Anything else of the parent is left behind, so deletions
// work. It returns why the manifest is refused, or "".
async function saveManifestCarry(env, cid, parent, carry) {
  const from = new Map(await carryOf(env, parent)); // the parent's files carried from older versions
  for (const rel of await filesOf(env, parent)) from.set(rel, parent);
  const uploaded = await filesOf(env, cid);
  const paths = carry.map((raw) => String(raw).replace(/^\/+|\/+$/g, ""));
  const out = new Map();
  for (const p of paths) {
    if (!p || p.split("/").some((s) => s === "" || s === "." || s === "..")) return `bad carried path ${p}`;
    if (paths.some((q) => q !== p && p.startsWith(q + "/"))) return `carried path ${p} is inside another carried path`;
    if (uploaded.some((rel) => rel === p || rel.startsWith(p + "/"))) return `uploaded files overlap carried path ${p}`;
    let found = false;
    if (from.has(p)) { out.set(p, from.get(p)); found = true; }
    for (const [rel, src] of from) if (rel.startsWith(p + "/")) { out.set(rel, src); found = true; }
    if (!found) return `carried path ${p} is not in the parent`;
  }
  await env.SITES.put(`carry/${cid}.json`, JSON.stringify(Object.fromEntries(out)));
  carries.set(cid, out);
  return "";
}

// held lists the files stored for a version, with their sizes: what an
// unfinished push left here, so its retry sends only the rest.
async function held(env, cid) {
  const out = [];
  let cursor;
  do {
    const page = await env.SITES.list({ prefix: `sites/${cid}/`, cursor, limit: 1000 });
    for (const o of page.objects) out.push({ path: o.key.slice(`sites/${cid}/`.length), size: o.size });
    cursor = page.truncated ? page.cursor : undefined;
  } while (cursor);
  return out;
}
```

- [ ] **Step 4: Run the tests**

Run: `node --check worker/src/index.js && cd worker && node --experimental-loader ./text-loader.mjs --test test/`
Expected: all pass (12 existing + 2 new = 14).

- [ ] **Step 5: Commit**

```bash
git add worker/src/index.js worker/wrangler.toml worker/text-loader.mjs worker/test/push.test.mjs
git commit -m "Worker takes manifest pushes; lists unfinished pushes; serves /agents.md

A manifest push's version is its uploads plus the carried files or folders
of the parent, so deletions reach crop.top and the node; bad manifests get
400. GET /v0/host/versions/<cid>/files lets a retry skip what is stored.
crop.top/agents.md serves docs/agents.md.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Client uploads that finish: no flat limit, resume, the version's own bytes

**Files:**
- Modify: `internal/publish/push.go`:
  - the `pushBatch`/`pushChunk` consts at about line 80;
  - `Push`;
  - `pushDir`;
  - new helpers.
- Modify: `internal/publish/post.go` (the `pushDir` call at about line 165; the `hostKey` struct; `hostEntry`)
- Test: `internal/publish/push_test.go`

**Interfaces:**
- Consumes: `(*ipfs.Embedded).Files` and `OpenFile` (Task 2), through the `versionReader` interface below; the hosts' `versions/<cid>/files` (Tasks 3–4).
- Produces:
  - `type pushSpec struct { Parent string; Files []string; Carry []string; Dir string }`;
  - `func (p *Publisher) pushDir(ctx context.Context, site *store.Site, key, cid string, seq uint64, spec pushSpec) error`;
  - `type hostConflict struct{ msg string }` (an error; `pushDir` returns it on `409`);
  - `func sendWatched(req *http.Request) (*http.Response, error)`;
  - vars `stallAfter`, `replyWithin` (`time.Duration`), `pushBatch`, `pushChunk` (`int64`);
  - `hostKey.AcceptsManifest bool` (json `acceptsManifest`);
  - `var errNoVersion` (`hostEntry` wraps it when the host holds no version).

- [ ] **Step 1: Write the failing tests** (append to `internal/publish/push_test.go`; add imports as needed)

```go
// An upload keeps going while its body moves, however slowly, and is given
// up when it stops moving or the host never answers.
func TestUploadsKeepGoingWhileTheyMove(t *testing.T) {
	stall, reply := stallAfter, replyWithin
	stallAfter, replyWithin = 300*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { stallAfter, replyWithin = stall, reply })
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/silent" {
			<-release // the body is in; the answer never comes
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	defer close(release) // runs before srv.Close, which waits for the handler
	send := func(path string, body io.Reader) error {
		req, _ := http.NewRequest("POST", srv.URL+path, body)
		resp, err := sendWatched(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, resp.Body)
		return resp.Body.Close()
	}
	if err := send("/", &slowReader{n: 12, every: 50 * time.Millisecond}); err != nil {
		t.Fatalf("a slow body that keeps moving was given up: %v", err)
	}
	stuck := make(chan struct{})
	defer close(stuck)
	start := time.Now()
	if err := send("/", &stuckReader{wait: stuck}); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("a body that stopped moving was not given up in time: %v after %s", err, time.Since(start))
	}
	if err := send("/silent", strings.NewReader("all of it")); err == nil {
		t.Fatal("a host that never answers was waited on forever")
	}
}

type slowReader struct {
	n     int
	every time.Duration
}

func (r *slowReader) Read(b []byte) (int, error) {
	if r.n == 0 {
		return 0, io.EOF
	}
	time.Sleep(r.every)
	r.n--
	b[0] = 'x'
	return 1, nil
}

type stuckReader struct{ wait chan struct{} }

func (r *stuckReader) Read(b []byte) (int, error) {
	<-r.wait
	return 0, io.EOF
}

// A push sends the version's own bytes even when the folder changed after it
// was added, and a push cut short resumes: the retry skips what the host
// already holds.
func TestPushSendsTheVersionAndResumes(t *testing.T) {
	ctx := context.Background()
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	failLast := true
	var sent int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v0/host/push" {
			part := strings.SplitN(r.Header.Get("X-Croptop-Part"), "/", 2)
			mu.Lock()
			fail := failLast && len(part) == 2 && part[0] == part[1] && part[1] != "1" // the final part of a split push
			if fail {
				failLast = false
			}
			mu.Unlock()
			if fail {
				http.Error(w, "cut short", 500)
				return
			}
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			sent += int64(len(b))
			mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(b))
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	batch := pushBatch
	pushBatch = 64 << 10 // the fixture site splits into several parts
	t.Cleanup(func() { pushBatch = batch })

	laptop := offlineNode(t)
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	ipnsName, _ := laptop.Keystore().Generate(fixtureID)
	site, _ := s.Site(fixtureID)
	site.IPNS = ipnsName
	SetHost(site, srv.URL)
	s.SaveSite(site)
	p := &Publisher{Store: s, Node: laptop, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: laptop}}
	if err := p.Render.Render(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	c, err := laptop.AddDir(ctx, s.PublicDir(fixtureID))
	if err != nil {
		t.Fatal(err)
	}
	laptop.SignRecord(fixtureID, c, 1)
	// a render after the version was made must not leak into its upload
	os.WriteFile(filepath.Join(s.PublicDir(fixtureID), "index.html"), []byte("rendered later"), 0o644)

	if err := p.Push(ctx, site, c, 1); err == nil {
		t.Fatal("the cut-short push must fail")
	}
	mu.Lock()
	first := sent
	sent = 0
	mu.Unlock()
	if err := p.Push(ctx, site, c, 1); err != nil {
		t.Fatalf("retry: %v", err) // the host checks the files hash to c: a leaked file fails here
	}
	mu.Lock()
	retry := sent
	mu.Unlock()
	// resumed, the retry sends about one part (the one cut short); from scratch, the whole site
	if first <= retry || retry > pushBatch+(16<<10) {
		t.Fatalf("the retry sent %d bytes after %d went up the first time: it did not resume", retry, first)
	}
	e, err := hostEntry(ctx, srv.URL, ipnsName)
	if err != nil || e.CID != c || !e.AcceptsManifest {
		t.Fatalf("host entry %+v, %v", e, err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -p 1 ./internal/publish/ -run 'TestUploadsKeepGoing|TestPushSendsTheVersion' -count=1`
Expected: FAIL to compile (`stallAfter`, `replyWithin`, `sendWatched` undefined; `pushBatch` is a const; `AcceptsManifest` undefined).

- [ ] **Step 3: Implement**

In `push.go`, replace the `pushBatch`/`pushChunk` consts with:

```go
// A part of a push holds files up to pushBatch bytes; bigger files go alone
// in pushChunk pieces. Variables so tests can make sites split.
var pushBatch, pushChunk int64 = 16 << 20, 64 << 20

// stallAfter is how long an upload may send nothing before it is given up;
// replyWithin bounds the wait for the host's answer once all of it is sent.
// A push has no deadline otherwise, so a slow link still finishes.
var stallAfter, replyWithin = 2 * time.Minute, 5 * time.Minute
```

Add:

```go
// pushSpec says what a push sends. With no Dir the files come from the
// version's own blocks, so an upload sends exactly that version even if the
// site is rendered again while it runs.
type pushSpec struct {
	Parent string   // the version the host must still hold for the push to apply
	Files  []string // the version's files to send; nil sends them all
	Carry  []string // non-nil makes a manifest push: the version is Files plus these paths of Parent
	Dir    string   // send the files under this directory instead (post --key's staging dir)
}

// hostConflict is a push the host refused because the site moved on (409).
type hostConflict struct{ msg string }

func (e *hostConflict) Error() string { return e.msg }

// versionReader reads a version's files from the engine's blocks: the
// embedded engine does, the kubo one does not.
type versionReader interface {
	Files(ctx context.Context, root string) ([]ipfs.VersionFile, error)
	OpenFile(ctx context.Context, root, rel string) (io.ReadSeekCloser, int64, error)
}

type pushFile struct {
	rel  string
	size int64
}

// pushFiles lists what a push sends, with sizes, and how to open each file.
func (p *Publisher) pushFiles(ctx context.Context, cid string, spec pushSpec) ([]pushFile, func(string) (io.ReadCloser, error), error) {
	want := map[string]bool{}
	for _, f := range spec.Files {
		want[f] = true
	}
	var files []pushFile
	if spec.Dir == "" {
		vr, ok := p.Node.(versionReader)
		if !ok {
			return nil, nil, fmt.Errorf("this engine cannot read a version's files; push from a folder")
		}
		all, err := vr.Files(ctx, cid)
		if err != nil {
			return nil, nil, err
		}
		for _, f := range all {
			if spec.Files == nil || want[f.Path] {
				files = append(files, pushFile{f.Path, f.Size})
			}
		}
		return files, func(rel string) (io.ReadCloser, error) {
			r, _, err := vr.OpenFile(ctx, cid, rel)
			return r, err
		}, nil
	}
	err := filepath.WalkDir(spec.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(spec.Dir, path)
		if rel = filepath.ToSlash(rel); spec.Files == nil || want[rel] {
			files = append(files, pushFile{rel, info.Size()})
		}
		return nil
	})
	return files, func(rel string) (io.ReadCloser, error) {
		return os.Open(filepath.Join(spec.Dir, filepath.FromSlash(rel)))
	}, err
}

// heldFiles asks the host which files of version cid it already holds, from
// an earlier attempt that did not finish. A host that cannot say holds none.
func heldFiles(ctx context.Context, base, cid string) map[string]int64 {
	hctx, cancel := context.WithTimeout(ctx, hostTimeout)
	defer cancel()
	b, status, err := httpGet(hctx, base+"/v0/host/versions/"+cid+"/files")
	if err != nil || status != 200 {
		return nil
	}
	var list []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	if json.Unmarshal(b, &list) != nil {
		return nil
	}
	out := make(map[string]int64, len(list))
	for _, f := range list {
		out[f.Path] = f.Size
	}
	return out
}

// skipHeld drops the files the host already holds at the same size. It keeps
// the smallest one, so the final part, which commits the version, still
// carries a file.
func skipHeld(files []pushFile, have map[string]int64) []pushFile {
	var out []pushFile
	smallest := -1
	for i, f := range files {
		if size, ok := have[f.rel]; !ok || size != f.size {
			out = append(out, f)
		}
		if smallest < 0 || f.size < files[smallest].size {
			smallest = i
		}
	}
	if len(out) == 0 && smallest >= 0 {
		out = []pushFile{files[smallest]}
	}
	return out
}

// sendWatched sends req and returns its response. It cancels the request if
// the body stops moving for stallAfter, or if the host has not answered
// replyWithin after the body was sent. There is no deadline beyond that.
func sendWatched(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	w := &watched{last: time.Now()}
	if req.Body != nil {
		w.r = req.Body
		req.Body = w
	} else {
		w.done = true
	}
	req = req.WithContext(ctx)
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(stallAfter / 10)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if w.overdue() {
					cancel()
					return
				}
			}
		}
	}()
	resp, err := http.DefaultClient.Do(req)
	close(stop)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelBody{resp.Body, cancel}
	return resp, nil
}

// watched is a request body that records when it last moved.
type watched struct {
	r    io.ReadCloser
	mu   sync.Mutex
	last time.Time
	done bool // all of it was sent; now waiting for the answer
}

func (w *watched) Read(b []byte) (int, error) {
	n, err := w.r.Read(b)
	w.mu.Lock()
	if n > 0 || err == io.EOF {
		w.last = time.Now()
	}
	if err == io.EOF {
		w.done = true
	}
	w.mu.Unlock()
	return n, err
}

func (w *watched) Close() error { return w.r.Close() }

func (w *watched) overdue() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	limit := stallAfter
	if w.done {
		limit = replyWithin
	}
	return time.Since(w.last) > limit
}

// cancelBody ends a watched request's context once its answer is read.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
```

Replace `Push` and `pushDir` with the following. The signing, record, blocks and part protocol are unchanged.

```go
// Push sends a whole version to the site's host. With the embedded engine
// the files come from the version's blocks; with kubo, from the rendered
// folder as before.
func (p *Publisher) Push(ctx context.Context, site *store.Site, cid string, seq uint64) error {
	spec := pushSpec{}
	if _, ok := p.Node.(versionReader); !ok {
		spec.Dir = p.Store.PublicDir(site.ID)
	}
	return p.pushDir(ctx, site, site.ID, cid, seq, spec)
}

// pushDir pushes a version's files, signed with the keystore key named key.
// With spec.Parent the host adds them on top of that version, which it must
// still hold; with spec.Carry it keeps only them and the carried paths. The
// version's folder blocks ride along, so the next reader can list it from the
// host before any IPFS peer has it. A push cut short resumes: the files the
// host already holds for this version are not sent again.
func (p *Publisher) pushDir(ctx context.Context, site *store.Site, key, cid string, seq uint64, spec pushSpec) error {
	base := HostOf(site)
	domain, err := hostDomain(base)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	sig, err := p.Node.Keystore().Sign(key, host.PushMessage(domain, site.IPNS, cid, seq, now))
	if err != nil {
		return err
	}
	var record string
	if rs, ok := p.Node.(interface{ Record(string) []byte }); ok {
		if rec := rs.Record(key); len(rec) > 0 {
			record = base64.StdEncoding.EncodeToString(rec)
		}
	}
	var blocks map[string][]byte
	if bs, ok := p.Node.(interface {
		DirBlocks(context.Context, string) (map[string][]byte, error)
	}); ok {
		blocks, _ = bs.DirBlocks(ctx, cid)
	}
	files, open, err := p.pushFiles(ctx, cid, spec)
	if err != nil {
		return err
	}
	if have := heldFiles(ctx, base, cid); len(have) > 0 {
		files = skipHeld(files, have)
	}
	signed := func(req *http.Request) {
		req.Header.Set("X-Croptop-Ipns", site.IPNS)
		req.Header.Set("X-Croptop-Cid", cid)
		req.Header.Set("X-Croptop-Seq", strconv.FormatUint(seq, 10))
		req.Header.Set("X-Croptop-Time", strconv.FormatInt(now, 10))
		req.Header.Set("X-Croptop-Sig", base64.StdEncoding.EncodeToString(sig))
		if record != "" {
			req.Header.Set("X-Croptop-Record", record)
		}
		if spec.Parent != "" {
			req.Header.Set("X-Croptop-Parent", spec.Parent)
		}
	}
	answer := func(resp *http.Response, what string) (string, error) {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		switch resp.StatusCode {
		case 200:
			return string(body), nil
		case 409:
			return "", &hostConflict{fmt.Sprintf("%s: %s", base, strings.TrimSpace(string(body)))}
		}
		return "", fmt.Errorf("%s: %s: %s", what, resp.Status, strings.TrimSpace(string(body)))
	}
	// big files first, in chunks; the host reassembles them
	var small []pushFile
	for _, f := range files {
		if f.size <= pushBatch {
			small = append(small, f)
			continue
		}
		chunks := int((f.size + pushChunk - 1) / pushChunk)
		rc, err := open(f.rel)
		if err != nil {
			return err
		}
		upload := ""
		for i := 0; i < chunks; i++ {
			size := pushChunk
			if rest := f.size - int64(i)*pushChunk; rest < size {
				size = rest
			}
			req, err := http.NewRequestWithContext(ctx, "POST", base+"/v0/host/push", io.LimitReader(rc, size))
			if err != nil {
				rc.Close()
				return err
			}
			req.ContentLength = size
			req.Header.Set("Content-Type", "application/octet-stream")
			signed(req)
			req.Header.Set("X-Croptop-File", f.rel)
			req.Header.Set("X-Croptop-Chunk", fmt.Sprintf("%d/%d", i+1, chunks))
			if upload != "" {
				req.Header.Set("X-Croptop-Upload", upload)
			}
			resp, err := sendWatched(req)
			if err != nil {
				rc.Close()
				return err
			}
			body, err := answer(resp, fmt.Sprintf("%s chunk %d of %d", f.rel, i+1, chunks))
			if err != nil {
				rc.Close()
				return err
			}
			var out struct{ Upload string }
			json.Unmarshal([]byte(body), &out)
			if out.Upload != "" {
				upload = out.Upload
			}
		}
		rc.Close()
		p.log("pushed %s in %d chunks", f.rel, chunks)
	}
	var batches [][]pushFile
	var cur []pushFile
	var curSize int64
	for _, f := range small {
		if len(cur) > 0 && curSize+f.size > pushBatch {
			batches = append(batches, cur)
			cur, curSize = nil, 0
		}
		cur = append(cur, f)
		curSize += f.size
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	for i, batch := range batches {
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		final := i == len(batches)-1
		go func() {
			var err error
			for _, f := range batch {
				part, e := mw.CreateFormFile("file:"+f.rel, filepath.Base(f.rel))
				if e != nil {
					err = e
					break
				}
				rc, e := open(f.rel)
				if e != nil {
					err = e
					break
				}
				_, e = io.Copy(part, rc)
				rc.Close()
				if e != nil {
					err = e
					break
				}
			}
			if err == nil && final {
				for c, data := range blocks {
					var part io.Writer
					if part, err = mw.CreateFormFile("block:"+c, c); err == nil {
						_, err = part.Write(data)
					}
					if err != nil {
						break
					}
				}
				if err == nil && spec.Carry != nil {
					m, _ := json.Marshal(map[string][]string{"carry": spec.Carry})
					err = mw.WriteField("manifest", string(m))
				}
			}
			if err == nil {
				err = mw.Close()
			}
			pw.CloseWithError(err)
		}()
		req, err := http.NewRequestWithContext(ctx, "POST", base+"/v0/host/push", pr)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		signed(req)
		req.Header.Set("X-Croptop-Part", fmt.Sprintf("%d/%d", i+1, len(batches)))
		resp, err := sendWatched(req)
		if err != nil {
			pr.CloseWithError(err)
			return err
		}
		if _, err := answer(resp, fmt.Sprintf("part %d of %d", i+1, len(batches))); err != nil {
			return err
		}
	}
	if len(batches) > 1 {
		p.log("pushed %s in %d parts", site.Name, len(batches))
	}
	return nil
}
```

Add `sync` and `github.com/mejango/croptop/internal/ipfs` to push.go's imports.

In `post.go`:
- Change the push call to `p.pushDir(ctx, site, keyName, cid, seq, pushSpec{Parent: entry.CID, Dir: changed})`.
- Add `AcceptsManifest bool `json:"acceptsManifest"`` to `hostKey`.
- Add `var errNoVersion = errors.New("no version there")`.
- In `hostEntry`, make the "holds no version" error wrap it, keeping the message: `fmt.Errorf("%s holds no version of %s; publish the site once from the console, which pushes it there (%w)", hostURL, ipnsName, errNoVersion)`. Add `errors` to the imports if missing.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal && go vet ./internal/publish/ && go test -p 1 ./internal/publish/ -count=1 -timeout 10m`
Expected: all pass, including `TestPublishPushesDefaultAndCustomHost` (the kubo engine pushes from the folder) and `TestPostWithOnlyTheKey`.

- [ ] **Step 5: Commit**

```bash
git add internal/publish/push.go internal/publish/post.go internal/publish/push_test.go
git commit -m "Uploads finish on slow links, resume, and send the version itself

A push is given up only when it stops moving (2 min) or the host stops
answering (5 min), never at a flat 10 minutes; a retry skips what the host
holds; with the embedded engine the bytes come from the version's blocks,
so a render during a long upload cannot leak into it. 409 is a typed error.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Client: what changed since the host's version

**Files:**
- Create: `internal/publish/changes.go`
- Modify: `internal/publish/post.go` (`links` takes a smaller interface)
- Test: `internal/publish/changes_test.go`

**Interfaces:**
- Consumes: `Links`, `PutBlock`, `Files`, `CheckManifest` (Task 2); `p.links` (post.go).
- Produces:
  - `type changesEngine interface` (below);
  - `func (p *Publisher) changes(ctx context.Context, eng changesEngine, hostURL, root, parent string) (upload, carry []string, err error)`.

- [ ] **Step 1: Write the failing test** (`internal/publish/changes_test.go`)

```go
package publish

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mejango/croptop/internal/host"
)

func writeSite(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The changes since the host's version are the files to upload and the
// largest unchanged files and folders to carry. Deleted ones are in neither.
// The result checks out against the new version even on a machine that has
// only the new version, reading the parent's folders from the host.
func TestChangesSinceTheHostsVersion(t *testing.T) {
	ctx := context.Background()
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	old := map[string]string{"index.html": "home", "assets/site.css": "css", "assets/font.woff": "font", "p1/index.html": "one", "p1/photo.jpg": "photo", "p2/index.html": "two", "notes.txt": "a file that becomes a folder"}
	now := map[string]string{"index.html": "home 2", "assets/site.css": "css", "assets/font.woff": "font", "p1/index.html": "one 2", "p1/photo.jpg": "photo", "p3/index.html": "three", "notes.txt/index.html": "now a folder"}
	wantUp := []string{"index.html", "notes.txt/index.html", "p1/index.html", "p3/index.html"}
	wantCarry := []string{"assets", "p1/photo.jpg"}

	laptop := offlineNode(t)
	parent, err := laptop.AddDir(ctx, writeSite(t, old))
	if err != nil {
		t.Fatal(err)
	}
	root, err := laptop.AddDir(ctx, writeSite(t, now))
	if err != nil {
		t.Fatal(err)
	}
	p := &Publisher{Node: laptop}
	up, carry, err := p.changes(ctx, laptop, srv.URL, root, parent)
	if err != nil || !reflect.DeepEqual(up, wantUp) || !reflect.DeepEqual(carry, wantCarry) {
		t.Fatalf("changes: upload %v carry %v, %v", up, carry, err)
	}
	if err := laptop.CheckManifest(ctx, root, parent, up, carry); err != nil {
		t.Fatalf("check: %v", err)
	}

	// another machine has only the new version; the host has the parent and its folder blocks
	h.Engine.AddDir(ctx, writeSite(t, old)) // the host holds the parent's blocks
	other := offlineNode(t)
	root2, err := other.AddDir(ctx, writeSite(t, now))
	if err != nil || root2 != root {
		t.Fatal(err)
	}
	q := &Publisher{Node: other}
	up, carry, err = q.changes(ctx, other, srv.URL, root, parent)
	if err != nil || !reflect.DeepEqual(up, wantUp) || !reflect.DeepEqual(carry, wantCarry) {
		t.Fatalf("changes from the host's folders: upload %v carry %v, %v", up, carry, err)
	}
	if err := other.CheckManifest(ctx, root, parent, up, carry); err != nil {
		t.Fatalf("check without the parent's files: %v", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -p 1 ./internal/publish/ -run TestChangesSinceTheHostsVersion -count=1`
Expected: FAIL to compile (`p.changes` undefined).

- [ ] **Step 3: Implement**

In `post.go`, add the interface below and change `links`'s parameter from `eng postEngine` to `eng blockLister`. Every caller passes a `postEngine` or a `changesEngine`, both of which satisfy it.

```go
// blockLister lists folders, storing blocks fetched from a host first.
type blockLister interface {
	Links(ctx context.Context, c string) (map[string]string, error)
	PutBlock(ctx context.Context, c string, data []byte) error
}
```

Create `internal/publish/changes.go`:

```go
package publish

import (
	"context"
	"io"
	"sort"
	"strings"

	"github.com/mejango/croptop/internal/ipfs"
)

// changesEngine is what changes-only publishing needs from the IPFS engine:
// the embedded one has it, the kubo one does not.
type changesEngine interface {
	blockLister
	Files(ctx context.Context, root string) ([]ipfs.VersionFile, error)
	OpenFile(ctx context.Context, root, rel string) (io.ReadSeekCloser, int64, error)
	CheckManifest(ctx context.Context, root, parent string, upload, carry []string) error
	SignRecord(key, c string, seq uint64) ([]byte, error)
	Announce(ctx context.Context, key, c string, rec []byte) error
}

// changes says how version root differs from parent, a version the host
// holds. It returns the files to upload and the paths to carry, each the
// largest unchanged file or folder. Anything of parent in neither list is
// deleted. parent's folders come from this node's blocks or, when it lacks
// them, from the host, checked against their CIDs. Where a folder cannot be
// compared, all its files are uploaded: more bytes, never a wrong version.
func (p *Publisher) changes(ctx context.Context, eng changesEngine, hostURL, root, parent string) (upload, carry []string, err error) {
	files, err := eng.Files(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	dirs := map[string]bool{}
	for _, f := range files {
		for d := f.Path; strings.Contains(d, "/"); {
			d = d[:strings.LastIndex(d, "/")]
			dirs[d] = true
		}
	}
	under := func(at string) []string {
		var out []string
		for _, f := range files {
			if f.Path == at || strings.HasPrefix(f.Path, at+"/") {
				out = append(out, f.Path)
			}
		}
		return out
	}
	var walk func(rel, now, before string) (up, keep []string, err error)
	walk = func(rel, now, before string) (up, keep []string, err error) {
		nl, err := eng.Links(ctx, now)
		if err != nil {
			return nil, nil, err
		}
		bl, err := p.links(ctx, eng, hostURL, before)
		if err != nil {
			return nil, nil, err
		}
		for name, c := range nl {
			at := name
			if rel != "" {
				at = rel + "/" + name
			}
			old, ok := bl[name]
			if ok && old == c {
				keep = append(keep, at)
				continue
			}
			if ok && dirs[at] {
				if u, k, err := walk(at, c, old); err == nil {
					up, keep = append(up, u...), append(keep, k...)
					continue
				}
			}
			up = append(up, under(at)...)
		}
		return up, keep, nil
	}
	upload, carry, err = walk("", root, parent)
	sort.Strings(upload)
	sort.Strings(carry)
	return upload, carry, err
}
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal && go vet ./internal/publish/ && go test -p 1 ./internal/publish/ -count=1 -timeout 10m`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/publish/changes.go internal/publish/changes_test.go internal/publish/post.go
git commit -m "Client works out what changed since the host's version

The upload list and the carry list (largest unchanged files and folders)
come from comparing folder CIDs, reading the parent's folders locally or
from the host; what neither list names is deleted.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Publish pushes only what changed, before announcing

**Files:**
- Modify: `internal/publish/publish.go` (`Publish`; new `publishOnce`, `publishChanges`, `published`, `warm`)
- Test: `internal/publish/changes_test.go` (append)

**Interfaces:**
- Consumes:
  - `p.changes`, `changesEngine` (Task 6);
  - `pushDir`, `pushSpec`, `hostConflict`, `hostKey.AcceptsManifest` (Task 5);
  - `CheckManifest`, `SignRecord`, `Announce` (Task 2);
  - `p.takeIn` (existing).
- Produces: `Publish` keeps its signature and `Result`. It gains the behavior in the Global Constraints' "Publish order".

- [ ] **Step 1: Write the failing test** (append to `internal/publish/changes_test.go`; add imports `bytes`, `io`, `net/http`, `strings`, `sync`, `render`, `store`, `templates`)

```go
// A second publish sends only what changed, as a manifest on the host's
// version, and the host then holds exactly the new version. If the host
// refuses it once because the site moved on, the publish takes that version
// in and tries again.
func TestPublishSendsOnlyWhatChanged(t *testing.T) {
	ctx := context.Background()
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sent int64
	manifests, refuse := 0, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v0/host/push" {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			sent += int64(len(b))
			isManifest := bytes.Contains(b, []byte(`name="manifest"`))
			if isManifest {
				manifests++
			}
			doRefuse := refuse && isManifest
			refuse = refuse && !isManifest
			mu.Unlock()
			if doRefuse {
				http.Error(w, "the site changed during this push; post again", 409)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(b))
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	laptop := offlineNode(t)
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	ipnsName, _ := laptop.Keystore().Generate(fixtureID)
	site, _ := s.Site(fixtureID)
	site.IPNS = ipnsName
	SetHost(site, srv.URL)
	s.SaveSite(site)
	p := &Publisher{Store: s, Node: laptop, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: laptop}, SkipPrewarm: true, Wait: true}
	push := func() (int64, int, Result) {
		mu.Lock()
		sent, manifests = 0, 0
		mu.Unlock()
		res, err := p.Publish(ctx, fixtureID, false)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		return sent, manifests, res
	}
	// SkipPrewarm skips the background upload of the announce-first path, so the first version goes up by hand
	first, err := p.Publish(ctx, fixtureID, false)
	if err != nil {
		t.Fatal(err)
	}
	site, _ = s.Site(fixtureID)
	if err := p.Push(ctx, site, first.CID, first.Sequence); err != nil {
		t.Fatal(err)
	}
	full := int64(0)
	filepath.WalkDir(s.PublicDir(fixtureID), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				full += info.Size()
			}
		}
		return nil
	})

	posts, _ := s.Posts(fixtureID)
	posts[0].Content = posts[0].Content + "\n\nedited"
	s.SavePost(fixtureID, posts[0])
	bytesSent, used, res := push()
	if used != 1 || bytesSent >= full/2 {
		t.Fatalf("a one-post edit sent %d bytes in %d manifest pushes; the whole site is %d", bytesSent, used, full)
	}
	if e, err := hostEntry(ctx, srv.URL, ipnsName); err != nil || e.CID != res.CID {
		t.Fatalf("host holds %+v, %v; published %s", e, err, res.CID)
	}

	mu.Lock()
	refuse = true
	mu.Unlock()
	posts[0].Content = posts[0].Content + " again"
	s.SavePost(fixtureID, posts[0])
	_, used, res = push()
	if used != 2 {
		t.Fatalf("after a 409 the publish should push again once: %d manifest pushes", used)
	}
	if e, err := hostEntry(ctx, srv.URL, ipnsName); err != nil || e.CID != res.CID {
		t.Fatalf("after the retry the host holds %+v, %v; published %s", e, err, res.CID)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -p 1 ./internal/publish/ -run TestPublishSendsOnlyWhatChanged -count=1`
Expected: FAIL: "a one-post edit sent 0 bytes in 0 manifest pushes". Publish only announces, and the push is skipped with SkipPrewarm.

- [ ] **Step 3: Implement**

Replace `Publish` (from `// Publish renders, adds, and publishes one site.` to the end of the function) with:

```go
// Publish renders, adds, and publishes one site. When the host can take only
// what changed, the version goes to the host first and is announced after.
// If someone posted meanwhile (an agent, another machine), that version is
// taken in and the site is rendered and pushed again, at most twice.
// Otherwise the version is announced first and uploaded in the background.
func (p *Publisher) Publish(ctx context.Context, siteID string, force bool) (Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	base := ""
	for attempt := 0; ; attempt++ {
		res, behind, err := p.publishOnce(ctx, siteID, force, base)
		if behind == nil || err != nil {
			return res, err
		}
		if attempt == 2 {
			if site, err := p.Store.Site(siteID); err == nil {
				return Result{}, fmt.Errorf("%s keeps getting newer versions of %s; publish again in a moment", HostOf(site), site.Name)
			}
			return Result{}, fmt.Errorf("the site keeps getting newer versions; publish again in a moment")
		}
		site, err := p.Store.Site(siteID)
		if err != nil {
			return Result{}, err
		}
		if err := p.takeIn(ctx, site, behind); err != nil {
			return Result{}, err
		}
		if site, _ = p.Store.Site(siteID); site.PublishedElsewhere {
			return Result{}, ErrPublishedElsewhere
		}
		base = strings.TrimPrefix(behind.Value, "/ipfs/")
	}
}

// publishOnce makes one attempt. behind is a newer version to take in before
// the next attempt. base is the version this machine builds on: its last
// publish, or a version just taken in.
func (p *Publisher) publishOnce(ctx context.Context, siteID string, force bool, base string) (Result, *ipfs.Record, error) {
	site, err := p.Store.Site(siteID)
	if err != nil {
		return Result{}, nil, err
	}
	if !p.Node.Keystore().Has(site.ID) {
		return Result{}, nil, fmt.Errorf("no IPNS key for %s on this machine; run `croptop key import %s <file.pem>`", site.Name, site.ID)
	}
	p.log("rendering %s", site.Name)
	if err := p.Render.Render(ctx, siteID); err != nil {
		return Result{}, nil, fmt.Errorf("render: %w", err)
	}
	p.log("adding to IPFS")
	cid, err := p.Node.AddDir(ctx, p.Store.PublicDir(siteID))
	if err != nil {
		return Result{}, nil, err
	}
	site, _ = p.Store.Site(siteID) // render may have changed post files, not the site, but reload anyway
	rec, netErr := p.latestRecord(ctx, site)
	if netErr == nil {
		p.log("network has sequence %d -> %s", rec.Sequence, rec.Value)
	} else {
		p.log("network record: %v", netErr)
	}
	if base == "" {
		base = deref(site.LastPublishedCID)
	}
	seq, err := nextSequence(site.IPNSSequence, base, rec, netErr, force)
	if err == ErrPublishedElsewhere && !force && rec != nil {
		return Result{}, rec, nil // take it in, then try again
	}
	if err != nil {
		if err == ErrPublishedElsewhere {
			p.saveSite(site.ID, func(s *store.Site) { s.PublishedElsewhere = true })
		}
		return Result{}, nil, err
	}
	if !force {
		if res, behind, done, err := p.publishChanges(ctx, site, cid, seq, base); done || behind != nil || err != nil {
			return res, behind, err
		}
	}
	p.log("publishing %s at sequence %d", cid, seq)
	pctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	if err := p.Node.NamePublish(pctx, site.ID, cid, seq); err != nil {
		return Result{}, nil, err
	}
	if err := p.published(site.ID, cid, seq); err != nil {
		return Result{}, nil, err
	}
	if !p.SkipPrewarm {
		if p.Wait {
			if err := p.Push(ctx, site, cid, seq); err != nil {
				p.log("push to %s failed: %v", HostOf(site), err)
			} else {
				p.log("pushed %s to %s", site.Name, HostOf(site))
			}
			p.warm(site, cid)
		} else {
			go func() {
				ctx := context.Background()
				if err := p.Push(ctx, site, cid, seq); err != nil {
					p.log("push to %s failed: %v", HostOf(site), err)
				} else {
					p.log("pushed %s to %s", site.Name, HostOf(site))
				}
				p.warm(site, cid)
			}() // Task 8 replaces this with the per-site queue
		}
	}
	return Result{CID: cid, Sequence: seq}, nil, nil
}

// publishChanges sends only what changed since the version the host holds,
// then announces the same record. done is false when that is not possible:
// another engine, an older host, a host holding another version, a diff
// that does not check out, or a host that cannot be reached. The caller then
// announces first and uploads in the background. behind is the version the
// host has instead, when another push got there first.
func (p *Publisher) publishChanges(ctx context.Context, site *store.Site, cid string, seq uint64, base string) (res Result, behind *ipfs.Record, done bool, err error) {
	eng, ok := p.Node.(changesEngine)
	if !ok || base == "" {
		return Result{}, nil, false, nil
	}
	hostURL := HostOf(site)
	hctx, cancel := context.WithTimeout(ctx, hostTimeout)
	e, herr := hostEntry(hctx, hostURL, site.IPNS)
	cancel()
	if herr != nil || !e.AcceptsManifest || e.CID != base || e.CID == cid {
		return Result{}, nil, false, nil
	}
	upload, carry, err := p.changes(ctx, eng, hostURL, cid, e.CID)
	if err == nil {
		err = eng.CheckManifest(ctx, cid, e.CID, upload, carry)
	}
	if err != nil {
		p.log("changes since %s: %v; sending the whole site", e.CID, err)
		return Result{}, nil, false, nil
	}
	rec, err := eng.SignRecord(site.ID, cid, seq)
	if err != nil {
		return Result{}, nil, false, err
	}
	p.log("pushing %d changed files of %s on top of %s", len(upload), cid, e.CID)
	err = p.pushDir(ctx, site, site.ID, cid, seq, pushSpec{Parent: e.CID, Files: upload, Carry: carry})
	var hc *hostConflict
	switch {
	case errors.As(err, &hc):
		p.log("%v; taking the newer version in", hc)
		hctx, cancel := context.WithTimeout(ctx, hostTimeout)
		cur, herr := hostEntry(hctx, hostURL, site.IPNS)
		cancel()
		if herr != nil {
			return Result{}, nil, false, err
		}
		return Result{}, &ipfs.Record{Value: "/ipfs/" + cur.CID, Sequence: cur.Sequence}, false, nil
	case err != nil:
		p.log("push to %s failed: %v; announcing first, the upload follows", hostURL, err)
		return Result{}, nil, false, nil
	}
	p.log("publishing %s at sequence %d", cid, seq)
	pctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	if err := eng.Announce(pctx, site.ID, cid, rec); err != nil {
		return Result{}, nil, true, err
	}
	if err := p.published(site.ID, cid, seq); err != nil {
		return Result{}, nil, true, err
	}
	if !p.SkipPrewarm {
		if p.Wait {
			p.warm(site, cid)
		} else {
			go p.warm(site, cid)
		}
	}
	return Result{CID: cid, Sequence: seq}, nil, true, nil
}

// published records a version as this machine's latest.
func (p *Publisher) published(siteID, cid string, seq uint64) error {
	now := store.Now()
	return p.saveSite(siteID, func(s *store.Site) {
		s.IPNSSequence, s.LastPublishedCID, s.LastPublished, s.PublishedElsewhere = seq, &cid, &now, false
	})
}

// warm asks the gateways to fetch a new version.
func (p *Publisher) warm(site *store.Site, cid string) {
	p.log("asking gateways to fetch the new version")
	p.prewarm(context.Background(), site, cid)
	p.prewarmAll(site, cid)
}
```

Add `errors` and `strings` to publish.go's imports if missing.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal && go vet ./internal/publish/ && go test -p 1 ./internal/publish/ ./internal/server/ -count=1 -timeout 10m`
Expected: all pass. The existing publish tests use the kubo fake engine, so they keep the announce-first path.

There is one intended change. `TestPublishRefusesWhenElsewhere`'s publish now tries to take the other version in first. Its host is unreachable, so the take-in fails, and the publish still returns `ErrPublishedElsewhere` and marks the site. If that test asserts the exact fake-ipfs calls, allow the take-in attempt. Do not remove the assertion that nothing was published.

- [ ] **Step 5: Commit**

```bash
git add internal/publish/publish.go internal/publish/changes_test.go
git commit -m "Publish pushes only what changed, then announces

When the host holds this machine's last version and takes manifests, the
publish sends the changed files and the carry list first (compare-and-swap
on the parent) and announces the same record after; on 409 it takes the
newer version in and tries again, at most twice.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Background uploads, one per site, newest wins, until the host has it

**Files:**
- Modify: `internal/publish/publish.go`:
  - the `Publisher` struct;
  - `publishOnce`'s background branch;
  - `CatchUp`;
  - new `queuePush`, `runPushes`, `pushVersion`.
- Test: `internal/publish/changes_test.go` (append)

**Interfaces:**
- Consumes: `pushDir`, `Push`, `p.changes`, `CheckManifest`, `hostEntry`, `errNoVersion`.
- Produces: `func (p *Publisher) queuePush(site *store.Site, cid string, seq uint64)`.

- [ ] **Step 1: Write the failing test** (append to `changes_test.go`)

```go
// While one upload runs, a newer version waits in its place and only the
// newest goes up after. A site whose host has no version is queued by the
// minute's catch-up.
func TestBackgroundPushesKeepOnlyTheNewest(t *testing.T) {
	ctx := context.Background()
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var mu sync.Mutex
	var cids []string
	blocked := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v0/host/push" {
			mu.Lock()
			if len(cids) == 0 || cids[len(cids)-1] != r.Header.Get("X-Croptop-Cid") {
				cids = append(cids, r.Header.Get("X-Croptop-Cid"))
			}
			wait := blocked
			mu.Unlock()
			if wait {
				<-release
			}
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	defer func() {
		mu.Lock()
		if blocked {
			blocked = false
			close(release)
		}
		mu.Unlock()
	}()

	laptop := offlineNode(t)
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	ipnsName, _ := laptop.Keystore().Generate(fixtureID)
	site, _ := s.Site(fixtureID)
	site.IPNS = ipnsName
	SetHost(site, srv.URL)
	s.SaveSite(site)
	p := &Publisher{Store: s, Node: laptop, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: laptop}, SkipPrewarm: true}
	version := func(text string) (string, uint64) {
		posts, _ := s.Posts(fixtureID)
		posts[0].Content = text
		s.SavePost(fixtureID, posts[0])
		p.Render.Render(ctx, fixtureID)
		c, err := laptop.AddDir(ctx, s.PublicDir(fixtureID))
		if err != nil {
			t.Fatal(err)
		}
		site, _ := s.Site(fixtureID)
		site.IPNSSequence++
		site.LastPublishedCID = &c
		s.SaveSite(site)
		laptop.SignRecord(fixtureID, c, site.IPNSSequence)
		return c, site.IPNSSequence
	}
	a, sa := version("a")
	site, _ = s.Site(fixtureID)
	p.queuePush(site, a, sa)
	time.Sleep(200 * time.Millisecond) // a's upload is now blocked at the host
	b, sb := version("b")
	site, _ = s.Site(fixtureID)
	p.queuePush(site, b, sb)
	c, sc := version("c")
	site, _ = s.Site(fixtureID)
	p.queuePush(site, c, sc)
	mu.Lock()
	blocked = false
	close(release)
	mu.Unlock()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if e, err := hostEntry(ctx, srv.URL, ipnsName); err == nil && e.CID == c {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the newest version never reached the host")
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	for _, x := range cids {
		if x == b {
			mu.Unlock()
			t.Fatalf("a version replaced while waiting was pushed anyway: %v", cids)
		}
	}
	mu.Unlock()

	// a host with no version of the site gets it from the minute's catch-up
	h2 := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h2.Start(); err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewServer(h2)
	defer srv2.Close()
	site, _ = s.Site(fixtureID)
	SetHost(site, srv2.URL)
	s.SaveSite(site)
	if err := p.CatchUp(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(30 * time.Second)
	for {
		if e, err := hostEntry(ctx, srv2.URL, ipnsName); err == nil && e.CID == c {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("catch-up did not send the site to a host that had no version of it")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -p 1 ./internal/publish/ -run TestBackgroundPushesKeepOnlyTheNewest -count=1`
Expected: FAIL to compile (`p.queuePush` undefined).

- [ ] **Step 3: Implement**

Add to `Publisher`:

```go
	// pushMu guards pending and pushing: one background upload runs per site,
	// and the newest version waiting replaces an older one.
	pushMu  sync.Mutex
	pending map[string]*pushJob
	pushing map[string]bool
```

Add:

```go
type pushJob struct {
	site *store.Site
	cid  string
	seq  uint64
}

// queuePush uploads a version in the background, one upload per site at a
// time. A version queued while another uploads replaces any older one
// waiting, so after a long upload only the newest goes up, as a changes-only
// push when it can be. A failed upload is retried with growing waits while
// it is still the newest.
func (p *Publisher) queuePush(site *store.Site, cid string, seq uint64) {
	p.pushMu.Lock()
	defer p.pushMu.Unlock()
	if p.pending == nil {
		p.pending, p.pushing = map[string]*pushJob{}, map[string]bool{}
	}
	p.pending[site.ID] = &pushJob{site, cid, seq}
	if !p.pushing[site.ID] {
		p.pushing[site.ID] = true
		go p.runPushes(site.ID)
	}
}

func (p *Publisher) runPushes(siteID string) {
	wait := time.Minute
	for {
		p.pushMu.Lock()
		j := p.pending[siteID]
		delete(p.pending, siteID)
		if j == nil {
			p.pushing[siteID] = false
			p.pushMu.Unlock()
			return
		}
		p.pushMu.Unlock()
		err := p.pushVersion(context.Background(), j)
		if err == nil {
			p.log("pushed %s to %s", j.site.Name, HostOf(j.site))
			wait = time.Minute
			continue
		}
		p.log("push of %s to %s failed: %v; trying again in %s", j.site.Name, HostOf(j.site), err, wait)
		p.pushMu.Lock()
		if p.pending[siteID] == nil {
			p.pending[siteID] = j
		}
		p.pushMu.Unlock()
		time.Sleep(wait)
		if wait < 30*time.Minute {
			wait *= 2
		}
	}
}

// pushVersion sends a version the network already has to the site's host:
// only what changed when the host holds an earlier version, otherwise every
// file. An upload cut short resumes where it stopped.
func (p *Publisher) pushVersion(ctx context.Context, j *pushJob) error {
	if eng, ok := p.Node.(changesEngine); ok {
		hostURL := HostOf(j.site)
		hctx, cancel := context.WithTimeout(ctx, hostTimeout)
		e, err := hostEntry(hctx, hostURL, j.site.IPNS)
		cancel()
		switch {
		case err == nil && (e.CID == j.cid || e.Sequence > j.seq):
			return nil // already there, or the host moved on past this version
		case err == nil && e.AcceptsManifest:
			if upload, carry, err := p.changes(ctx, eng, hostURL, j.cid, e.CID); err == nil && eng.CheckManifest(ctx, j.cid, e.CID, upload, carry) == nil {
				return p.pushDir(ctx, j.site, j.site.ID, j.cid, j.seq, pushSpec{Parent: e.CID, Files: upload, Carry: carry})
			}
		}
	}
	return p.Push(ctx, j.site, j.cid, j.seq)
}
```

In `publishOnce`, replace the `go func() { … }() // Task 8 replaces this …` block with:

```go
			p.queuePush(site, cid, seq)
			go p.warm(site, cid)
```

In `CatchUp`, replace

```go
	if err != nil || e.CID == *site.LastPublishedCID || e.Sequence < site.IPNSSequence {
		return nil // nothing newer there, or our own push is still on its way
	}
```

with

```go
	switch {
	case errors.Is(err, errNoVersion), err == nil && e.Sequence < site.IPNSSequence && e.CID != *site.LastPublishedCID:
		// the host is behind this machine: an upload was cut short (the app quit, the
		// laptop slept) or never made; send it, resuming what the host holds
		p.queuePush(site, *site.LastPublishedCID, site.IPNSSequence)
		return nil
	case err != nil || e.CID == *site.LastPublishedCID || e.Sequence < site.IPNSSequence:
		return nil
	}
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal && go vet ./internal/publish/ && go test -p 1 ./internal/publish/ ./internal/server/ -count=1 -timeout 10m`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/publish/publish.go internal/publish/changes_test.go
git commit -m "Background uploads: one per site, newest wins, until the host has it

A version queued during a long upload replaces older ones waiting; failed
uploads retry with growing waits; and every minute a site whose host is
behind (or has no version yet) is queued again, resuming what is there.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Docs

**Files:**
- Modify: `docs/host.md` (API section), `README.md` (publishing, around the "IPFS engine" section), `docs/superpowers/specs/2026-09-30-scaling-and-agent-economy-design.md` (end of section B)

- [ ] **Step 1: `docs/host.md` API list.** Add `acceptsManifest` to the keys entry description. Then add:

```markdown
- `POST /v0/host/push` with a `manifest` form field in the final part:
  `{"carry": [paths]}`, files or whole folders of `X-Croptop-Parent` to keep.
  The version is exactly the uploaded files plus the carried paths, so a
  publish can delete. A carried path the parent lacks, overlapping paths, or
  a manifest without a parent is `400`. The Go host rebuilds the version and
  checks it hashes to `X-Croptop-Cid`.
- `GET /v0/host/versions/<cid>/files`: `[{"path", "size"}]`, the files a push
  of `<cid>` left here without finishing, so its retry sends only the rest.
- `GET /agents.md` (crop.top): the instructions to hand a bot (docs/agents.md).
```

- [ ] **Step 2: README.** Under the publishing description (search for "A publish writes the IPNS record"), add a paragraph:

```markdown
A publish uploads only what changed since the version the site's host holds:
the changed files plus a list of what to keep, so deleted posts go too. The
first upload of a site sends everything, in the background, and survives a
slow link or a sleeping laptop: it stops only when nothing moves for two
minutes, and the next try resumes where it stopped.
```

- [ ] **Step 3: Spec.** At the end of section B, before `## D.`, add:

```markdown
**Added while planning B (2026-09-30).** jango.eth (658 MB, a 230 KB/s uplink)
never reached crop.top: every full upload hit the client's flat 10-minute
limit. B therefore also gives up an upload only when it stops moving (2 min
idle, 5 min for the host's answer), resumes an interrupted upload from what
the host already holds (`GET /v0/host/versions/<cid>/files`), reads uploads
from the version's blocks so a render during a long upload cannot corrupt it,
and runs one background upload per site with the newest version winning.
```

- [ ] **Step 4: Commit**

```bash
git add docs/host.md README.md docs/superpowers/specs/2026-09-30-scaling-and-agent-economy-design.md
git commit -m "Document changes-only publishing and resumable uploads

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: The P2P gate: publishing never waits on the host

Added 2026-10-01, with the user's approval. See the spec's "crop.top helps but is
never required" and Testing. Task 7 already falls back to announcing first when
the host cannot be reached. This task pins that with a test and bounds how long a
hung host can hold a publish up. Run it after Tasks 7–8 land, since it guards both.

**Files:**
- Modify: `internal/publish/publish.go`. `hostTimeout` and `networkTimeout` become
  vars, so the test can shorten them.
- Modify: `internal/ipfs/peers.go`. Add `Addrs` and `Dial` below. A4 later uses
  `Dial` for configured peers.
- Create: `internal/publish/resilience_test.go`

**Interfaces:**
- Produces:
  - `func (e *Embedded) Addrs() []string`: every listen address, loopback
    included, each ending `/p2p/<id>`;
  - `func (e *Embedded) Dial(ctx context.Context, addrs []string) error`: connects
    to each peer, trying all its addresses, and returns the first peer it could
    not reach;
  - `var hostTimeout, networkTimeout` (`time.Duration`; unchanged values 15 s and
    25 s).

- [ ] **Step 1: Write the failing test** (`internal/publish/resilience_test.go`)

```go
package publish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

// Publishing never waits on the site's host. With the host hung (it takes the
// connection and never answers), a publish still returns within the host and
// network timeouts, and another peer resolves the name to the new version and
// fetches it from the publisher. Every later sub-project keeps this green.
func TestPublishingNeedsNoHost(t *testing.T) {
	ctx := context.Background()
	ht, nt := hostTimeout, networkTimeout
	hostTimeout, networkTimeout = 300*time.Millisecond, 2*time.Second
	t.Cleanup(func() { hostTimeout, networkTimeout = ht, nt })

	reached := make(chan struct{}, 16)
	release := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
		<-release
	}))
	defer hung.Close()
	defer close(release) // runs before Close, which waits for the handlers

	laptop, peer := offlineNode(t), offlineNode(t)
	if err := peer.Dial(ctx, laptop.Addrs()); err != nil {
		t.Fatal(err)
	}
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	name, _ := laptop.Keystore().Generate(fixtureID)
	site, _ := s.Site(fixtureID)
	site.IPNS = name
	SetHost(site, hung.URL)
	s.SaveSite(site)
	p := &Publisher{Store: s, Node: laptop, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: laptop}, SkipPrewarm: true}

	start := time.Now()
	res, err := p.Publish(ctx, fixtureID, false)
	if err != nil {
		t.Fatalf("publish with the host hung: %v", err)
	}
	// two host lookups (the newest record, then what changed) and one network lookup
	if took, limit := time.Since(start), 2*hostTimeout+networkTimeout+5*time.Second; took > limit {
		t.Fatalf("publish waited %s on a hung host, limit %s", took, limit)
	}
	select {
	case <-reached:
	default:
		t.Fatal("the publish never asked the host, so this test proves nothing")
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	got, err := peer.Resolve(rctx, name)
	if err != nil || strings.TrimPrefix(got, "/ipfs/") != res.CID {
		t.Fatalf("the peer resolves %s to %q, %v; want %s", name, got, err, res.CID)
	}
	if _, err := peer.Block(rctx, res.CID); err != nil {
		t.Fatalf("the peer cannot fetch the version from the publisher: %v", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -p 1 ./internal/publish/ -run TestPublishingNeedsNoHost -count=1`
Expected: FAIL to compile (`Dial` and `Addrs` are undefined, and `hostTimeout` is a
constant).

- [ ] **Step 3: Implement**

In `publish.go`, turn the three timeouts into vars. Keep their values:

```go
// Vars so tests can shorten them.
var (
	networkTimeout = 25 * time.Second
	hostTimeout    = 15 * time.Second
	publishTimeout = 3 * time.Minute
)
```

In `internal/ipfs/peers.go`:

```go
// Addrs is every address this node listens on, loopback included, each ending
// in its peer ID: what another node on this machine dials.
func (e *Embedded) Addrs() []string {
	if e.host == nil {
		return nil
	}
	var out []string
	for _, a := range e.host.Addrs() {
		out = append(out, a.String()+"/p2p/"+e.host.ID().String())
	}
	return out
}

// Dial connects to the peers at addrs (each ending /p2p/<id>), trying all of a
// peer's addresses, and returns the first peer it could not reach.
func (e *Embedded) Dial(ctx context.Context, addrs []string) error {
	if e.host == nil {
		return fmt.Errorf("node not started")
	}
	var ms []ma.Multiaddr
	for _, a := range addrs {
		m, err := ma.NewMultiaddr(a)
		if err != nil {
			return err
		}
		ms = append(ms, m)
	}
	infos, err := peer.AddrInfosFromP2pAddrs(ms...)
	if err != nil {
		return err
	}
	for _, ai := range infos {
		if err := e.host.Connect(ctx, ai); err != nil {
			return err
		}
	}
	return nil
}
```

Add the multiaddr import (`ma "github.com/multiformats/go-multiaddr"`) if peers.go
lacks it.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal && go vet ./internal/... && go test -p 1 ./internal/publish/ ./internal/ipfs/ -count=1 -timeout 10m`
Expected: all pass. Mutation check: make `latestRecord` return the host's error
instead of falling back. The gate must fail.

- [ ] **Step 5: Commit**

```bash
git add internal/publish/publish.go internal/publish/resilience_test.go internal/ipfs/peers.go
git commit -m "Gate: publishing never waits on the host

A hung host costs a publish at most its timeouts; another peer resolves
the name and fetches the version from the publisher.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Ship B

Run by the main session. Before any deploy, tag or release, confirm with the user.

- [ ] **Step 1: Full test run:** `go vet ./... && go test -p 1 ./... -count=1 -timeout 15m && (cd worker && node --experimental-loader ./text-loader.mjs --test test/) && (cd apps/macos && swift test)`. Expect every Go package `ok`, Worker `# fail 0`, and Swift `0 failures`.
- [ ] **Step 2: Merge.** Bump `installer/macos-build-number` to 1158 and commit "Changes-only publishing and resumable uploads (build 1158)". Fast-forward main and push.
- [ ] **Step 3: Deploy the node.** Build a clean `git archive` folder plus the `templates/croptop` submodule, use `mktemp -d` (not `rm -rf`), then `railway up <dir> --path-as-root --service croptop-host --environment production --ci`. Verify:
  - `GET /v0/host/keys/<ipns>` on the Railway URL shows `"acceptsManifest":true`;
  - `GET /v0/host/versions/bafyx/files` → `[]`;
  - `health?peer=1` shows a healthy peer count.
- [ ] **Step 4: Deploy the Worker:** `cd worker && npx -y -p node@22 -p wrangler@4.145.0 wrangler deploy -c wrangler.toml`. Verify:
  - `https://crop.top/agents.md` → 200, `text/markdown`;
  - a keys entry shows `acceptsManifest: true`;
  - `https://crop.top/v0/host/versions/bafyx/files` → `[]`;
  - `/`, `/follo/` and `/directory` → 200.
- [ ] **Step 5: Release.** Tag `v0.13.19`, push, and wait for goreleaser. Run `installer/release-macos.sh 0.13.19` with the user's notary key (`SPARKLE_KEY_FILE=~/Documents/croptop-signing/sparkle-ed25519.key AC_API_KEY_PATH=<p8> AC_API_KEY_ID=<id> AC_API_ISSUER_ID=<issuer>`). Check the appcast shows `<sparkle:version>1158`. Add a release note: publishes upload only what changed; first uploads resume. (Save & publish and the host fix shipped in v0.13.18.)
- [ ] **Step 6: Live check** (with the user's go-ahead, since it publishes their site):
  1. Once the app has updated, publish JANGO. The log shows the full upload going out in the background, with no "context deadline exceeded".
  2. Over the next ~50 minutes, `https://crop.top/v0/host/keys/<jango ipns>` gains a `cid`.
  3. Then edit one post and publish again. The log shows "pushing N changed files", and crop.top holds the new CID within a minute.
  4. Kill any test processes started.
