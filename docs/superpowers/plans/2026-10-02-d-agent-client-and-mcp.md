# D: Agent client and MCP — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** an agent posts to a croptop site in about 3 seconds with nothing but its key and no open ports. Retries are safe (`--id`), output is scriptable (`--json`), an agent can start its own site (`key new`, `--create`), a post still goes out over IPFS when the host is down, and any MCP client can do all of it through `croptop mcp`.

**Architecture:**
- **HTTP first, P2P as the fallback.** `post --key` starts its engine offline (loopback only). Everything it reads comes from the host over HTTP and is checked against CIDs: the root folder's blocks (a big folder's shards too), the few files a post needs, and the newest signed record from the host's routing endpoint. If the host lacks a block, the engine goes online and the post runs again with IPFS reads. If the host cannot be reached even after retries, the post goes out over IPFS: announced with the key, and the command stays up until another peer provides it.
- **Hosts answer the newest record.** The owner's app renews its record every 10 minutes without pushing. The routing endpoint now answers the higher sequence of the pushed record and the DHT copy, so an agent signs above the owner's renewals.
- **For agents.**
  - `--id` derives the post ID from a stable key, so a retry returns the first post instead of posting again.
  - `--json` prints a machine-readable result.
  - `key new` and `--create` let an agent start its own site.
  - `croptop mcp` serves `post` and `site` tools over stdio with the official Go SDK.

**Tech Stack:** Go 1.27 (boxo v0.42.2, go-libp2p-kad-dht v0.42.1, and new: github.com/modelcontextprotocol/go-sdk v1.8.0), Cloudflare Worker (JS), wrangler 4.145.0, Railway.

**Spec:** `docs/superpowers/specs/2026-09-30-scaling-and-agent-economy-design.md`: section D, including "When the host is down", and the Testing section's P2P gate for `post --key`. D does not include the old "Carried over from G" item (now A4), nor the sale tools (F). Five additions go beyond the spec's text. Each says why:
- **Task 1 fixes a B bug found in the v0.13.19 live check (2026-10-02).** A background upload of a version this machine holds only in part is retried every 32 minutes, forever. Such a version was taken in from another machine, which reads only the posts it lacks. Four of the owner's sites do this now.
- **Task 2 pulls in one item of C's list: routing GETs answer the higher sequence of the registry's and the DHT's copies.** D needs it to choose a sequence above the owner's renewals. Without it, an agent's post sits below the laptop's renewed record, and IPFS readers keep the laptop's older version.
- **Task 7 (the owner's console):**
  - when the host cannot be reached, the minute's catch-up looks at the network;
  - it provides a version it takes in at once.

  The spec has the agent wait "until another peer holds the version (a provider other than itself)". Without these, the console takes the version in only at its next 10-minute renewal and provides it only every 12 hours, so the wait would almost always time out.
- **Task 8: when no peer took the post in time, the agent points the name back at the version it built on.** The spec leaves the record pointing at blocks only the agent had. Such a record breaks both readers:
  - IPFS readers cannot read the site;
  - the owner's console fails to take it in and marks the site "published elsewhere".

  Pointing back keeps the site readable, and posting again works. It costs one more signed record. A peer that fetched the version without announcing it sees the name move back, but the agent was told the post failed.
- **`--json` adds `onHost`.** It is false when the post went out over IPFS only.

## Global Constraints
- `post --key` and `croptop mcp` start the embedded engine with `Offline = true` (loopback only, no bootstrap, no DHT peers). P2P comes on only through `Publisher.Online`: when the host lacks a block (`errNeedsNetwork`), or when the host cannot be reached.
- Reads from a host are checked against CIDs: blocks by `PutBlock`, files by `FileCID`.
- **Sequence:** above the host's version and above the routing endpoint's newest record. If that record names another version than the host's at a higher sequence, refuse with "the network has a newer version of this site (sequence N) than <host> (sequence M); publish it from the machine that made it, then post again".
- **`--id <key>`:**
  - the post ID is the SHA-256 of `<ipns>/<key>`: first 16 bytes, version nibble 5, variant `10`, written as an uppercase UUID;
  - if the host's version already has that post, the result is `existing: true` and nothing is posted.

  Without `--id`, one command keeps one random post ID across its retries.
- **`--json`:**
  - prints one JSON object on stdout: `{"url","cid","sequence","postId","site","existing","onHost"}`, where `site` is the site's IPNS name;
  - `post --key` and `croptop mcp` write log lines to stderr, never stdout.
- **Retries:**
  - a 409, a 429, a 5xx, or no answer is tried again three times, after 2 s, 5 s and 10 s;
  - each attempt reads the host's version again;
  - any other 4xx is not retried.
- **Host down:** after the retries, if the last error was no answer or a 5xx:
  1. Post over IPFS on top of the network's version.
  2. Sign one above its sequence and announce.
  3. Wait until a provider other than this machine holds the version, at most `--linger` (default 10 minutes).
  4. On timeout, if the network still has this post's record, point the name back at the version the post was built on. Then exit non-zero.
- `croptop key new` prints a fresh PEM key on stdout and the site's IPNS name on stderr.
- **`post --create "<name>"`:**
  - makes the site's first version when the host holds none for the key;
  - is refused when the network already has a record for the key;
  - is ignored when the host holds a version.
- **`croptop mcp`:**
  - runs over stdio with go-sdk v1.8.0, with the tools `post` and `site`;
  - takes the key from `CROPTOP_KEY` (the PEM) or `CROPTOP_KEY_FILE` (its path), and no tool returns it;
  - takes files as `{path}` or `{name, base64}`, images, video and audio only, and a path may not lead elsewhere through a symlink.
- crop.top is never required. `TestPublishingNeedsNoHost` stays green, and Task 8 adds `TestPostingNeedsNoHost`.
- **Commands:**
  - Go tests run serially: `go test -p 1 ./...`, because packages share a kubo repo lock.
  - Worker tests: `cd worker && node --experimental-loader ./text-loader.mjs --test test/`.
  - wrangler: `npx -y -p node@22 -p wrangler@4.145.0 wrangler …`.
- **Commits and release:**
  - Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
  - The release is v0.13.20, build 1159.
  - Nothing is committed in `app/` while `installer/release-macos.sh` runs.

## Review Focus
1. **An agent retries a post whose answer was lost.** The second attempt must find the post (`existing: true`), never post twice. Task 5's test commits a push and loses its answer.
2. **The owner's laptop renewed since its last push.** The agent must sign above those renewals. Task 2 tests both hosts' answers; Task 3 tests the post's sequence after a renewal.
3. **A big site, and a version whose folder block the host lacks.** A big site has a sharded root folder; the host lacks folder blocks for versions pushed before hosts kept them. The first case must post over HTTP alone. The second must turn P2P on once and still post. Task 3 tests `FetchDir` on a sharded folder, and a post whose host lacks the root block.
4. **crop.top is down while an agent posts.** The post must reach the owner's machine over IPFS, and the agent must exit 0 once it has. If no peer takes it, the site must still resolve to a readable version. Task 8 has the gate test and the point-back test.
5. **Anything else on stdout.** `post --key --json` and `croptop mcp` must print only the JSON result, or only the protocol. Task 6 and Task 9 test both commands' stdout in-process.

## Files
| File | What changes |
|---|---|
| `internal/publish/publish.go` | uploads skip versions held in part (1); `Online`, `goOnline`, `offline` (3); catch-up from the network, `publishedElsewhere`, take-ins provide (7) |
| `internal/publish/post.go` | `Post` split into `postCall`; HTTP-only reads, routing sequence (3); `--id` (4); retries (5); `--create` (6); posting over IPFS (8); `View`, `KeyName` (9) |
| `internal/publish/push.go` | `hostRefusal.status`, `errHostBusy` (5) |
| `internal/publish/sync.go` | `pull` takes the host to read from (7) |
| `internal/ipfs/embedded_ipns.go` | `ParseRecord` (2) |
| `internal/ipfs/embedded_unixfs.go` | `IsNotHeld` (1), `FetchDir` (3) |
| `internal/ipfs/embedded.go` | `GoOnline` (3) |
| `internal/host/host.go` | routing GET answers the newest record (2) |
| `worker/src/index.js` | the same on crop.top; `parseRecord` cannot hang (2) |
| `internal/agent/mcp.go` (new) | the MCP server (9) |
| `cmd/croptop/main.go` | offline start, stderr logs (3); `--id`, `--json` (4); `key new`, `--create` (6); `--linger` (8); `mcp` (9) |
| `cmd/croptop/main_test.go` (new) | the agent commands end to end; stdout holds only results (6, 9) |
| `docs/agents.md`, `README.md`, `docs/host.md` | (10) |

---

### Task 1: Background uploads skip versions this machine holds only in part

Found in the v0.13.19 live check. The app's log showed this for four sites:

```
push of Sana Floresta to https://crop.top failed: 431D57E6-…/_grid.png: block was not found locally (offline): ipld: could not find bafybei…; trying again in 32m0s
```

Each site's last version was taken in from another machine. `pull` reads only the posts this machine lacks, so it holds that version's root, which `holds` checks, but not every file. `CatchUp` queues the version because crop.top has none. The upload reads blocks this machine never had, and it is retried forever. The machine that published the version can upload it, and so does this machine's next publish of the site.

**Files:**
- Modify: `internal/ipfs/embedded_unixfs.go` (add `IsNotHeld` after `PutBlock`)
- Modify: `internal/publish/publish.go`:
  - the `Publisher` struct;
  - `holds` (about line 258);
  - `runPushes` (about line 298).
- Test: `internal/publish/background_test.go`

**Interfaces:**
- Produces:
  - `func ipfs.IsNotHeld(err error) bool`;
  - the unexported field `Publisher.partial map[string]bool`, guarded by `pushMu`.

- [ ] **Step 1: Write the failing test** (append to `internal/publish/background_test.go`)

```go
// A version this machine holds only in part, such as one taken in from
// another machine (which reads only the posts it lacks), cannot be uploaded
// from here. Its upload stops at the first missing block and the version is
// not queued again: the machine that published it uploads it, and so does
// this machine's next publish of the site.
func TestAnUploadOfAVersionHeldInPartIsNotRetried(t *testing.T) {
	ctx := context.Background()
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	// the version, made on another machine: this one has only its root block
	elsewhere, laptop := offlineNode(t), offlineNode(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("made on the other machine"), 0o644)
	c, err := elsewhere.AddDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	root, err := elsewhere.Block(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := laptop.PutBlock(ctx, c, root); err != nil {
		t.Fatal(err)
	}

	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	name, err := laptop.Keystore().Generate(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	site, _ := s.Site(fixtureID)
	site.IPNS, site.LastPublishedCID, site.IPNSSequence = name, &c, 3
	rememberVersion(site, c)
	SetHost(site, srv.URL)
	if err := s.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var logs []string
	p := &Publisher{Store: s, Node: laptop, Log: func(l string) { mu.Lock(); logs = append(logs, l); mu.Unlock() }}

	if err := p.CatchUp(ctx, fixtureID); err != nil { // the host holds no version: the upload is queued
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		p.pushMu.Lock()
		busy := p.pushing[fixtureID]
		p.pushMu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the upload is still being retried")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i := 0; i < 2; i++ { // two more minutes of the app running
		if err := p.CatchUp(ctx, fixtureID); err != nil {
			t.Fatal(err)
		}
	}
	p.pushMu.Lock()
	queued := p.pending[fixtureID] != nil || p.pushing[fixtureID]
	p.pushMu.Unlock()
	if queued {
		t.Fatal("the version was queued again")
	}
	mu.Lock()
	defer mu.Unlock()
	if all := strings.Join(logs, "\n"); strings.Contains(all, "trying again") || strings.Count(all, "holds only part of") != 1 {
		t.Fatalf("the log:\n%s", all)
	}
}
```

Add `net/http/httptest` and `github.com/mejango/croptop/internal/host` to the test file's imports.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test -p 1 ./internal/publish/ -run TestAnUploadOfAVersionHeldInPartIsNotRetried -count=1`
Expected: FAIL with "the upload is still being retried" (the first retry waits a minute).

- [ ] **Step 3: Implement**

In `internal/ipfs/embedded_unixfs.go`, after `PutBlock`:

```go
// IsNotHeld says whether err comes from reading a block this node does not
// hold, in a read of local blocks only (Files, OpenFile, CheckManifest).
func IsNotHeld(err error) bool { return ipld.IsNotFound(err) }
```

In `internal/publish/publish.go`, add to `Publisher` after `wake`:

```go
	// partial holds versions this machine has only part of, such as one taken
	// in from another machine, which reads only the posts it lacks. They
	// cannot be uploaded from here, so they are never queued again. Guarded by
	// pushMu.
	partial map[string]bool
```

Replace `holds` with:

```go
// holds says whether this machine can upload version c: it has the root block,
// and no upload of c found a block missing. An engine that cannot say is taken
// to.
func (p *Publisher) holds(ctx context.Context, c string) bool {
	p.pushMu.Lock()
	part := p.partial[c]
	p.pushMu.Unlock()
	if part {
		return false
	}
	if b, ok := p.Node.(interface {
		Block(context.Context, string) ([]byte, error)
	}); ok {
		_, err := b.Block(ctx, c)
		return err == nil
	}
	return true
}
```

In `runPushes`, add a case to the `switch` after the `errSuperseded` case:

```go
		case ipfs.IsNotHeld(err):
			// taken in from another machine: only the posts this one lacked were
			// read, so the rest is not here to send
			p.log("%s: this machine holds only part of %s (%v); the machine that published it uploads it, and so does the next publish here", j.site.Name, j.cid, err)
			p.pushMu.Lock()
			if p.partial == nil {
				p.partial = map[string]bool{}
			}
			p.partial[j.cid] = true
			p.pushMu.Unlock()
			continue
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal && go vet ./internal/... && go test -p 1 ./internal/publish/ ./internal/ipfs/ -count=1 -timeout 15m`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ipfs/embedded_unixfs.go internal/publish/publish.go internal/publish/background_test.go
git commit -m "Background uploads skip versions this machine holds only in part

A version taken in from another machine has only the posts this one lacked;
uploading it failed on a missing block and was retried every 32 minutes,
forever. It is now dropped once, and never queued again.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Hosts answer the newest record

The owner's app renews its record every 10 minutes without pushing. It sends each renewal to its host's routing endpoint, and the node keeps it in its DHT store. Today a pushed site's routing GET answers the record from its last push. From now on the answer is the higher sequence of the two.

**Files:**
- Modify: `internal/ipfs/embedded_ipns.go`:
  - add `ParseRecord`;
  - `NetworkRecord` uses the same reader.
- Modify: `internal/host/host.go`:
  - `serveRouting`'s GET case (about line 535);
  - add `newerRecord`.
- Modify: `worker/src/index.js`:
  - `routing` (about line 377);
  - `parseRecord` (about line 729).
- Test:
  - `internal/ipfs/embedded_test.go`;
  - `internal/host/host_test.go`;
  - `worker/test/push.test.mjs`.

**Interfaces:**
- Produces:
  - `func ipfs.ParseRecord(name string, rec []byte) (*ipfs.Record, error)`, which validates `rec` against `name`;
  - a routing GET that answers the newer of the pushed record and the DHT copy, on both hosts.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ipfs/embedded_test.go`:

```go
// ParseRecord reads a record only when it is valid for the name.
func TestParseRecord(t *testing.T) {
	e := NewEmbedded(t.TempDir())
	name, _ := e.Keystore().Generate("a")
	other, _ := e.Keystore().Generate("b")
	const c = "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4"
	rec, err := e.SignRecord("a", c, 7)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseRecord(name, rec)
	if err != nil || r.Value != "/ipfs/"+c || r.Sequence != 7 {
		t.Fatalf("%+v, %v", r, err)
	}
	for what, b := range map[string][]byte{"garbage": []byte("garbage"), "nothing": nil} {
		if _, err := ParseRecord(name, b); err == nil {
			t.Errorf("%s was read as a record", what)
		}
	}
	if _, err := ParseRecord(other, rec); err == nil {
		t.Error("another name's record was read")
	}
}
```

Append to `internal/host/host_test.go`:

```go
// A pushed site's routing answer is its newest record. The owner's machine
// renews its record every ten minutes without pushing, and sends each renewal
// here; an older record sent here does not replace the pushed one.
func TestRoutingAnswersTheNewestRecord(t *testing.T) {
	ctx := context.Background()
	site, eng := offline(t), offline(t)
	h := &Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: eng}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	name, _ := site.Keystore().Generate("p")
	const c = "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4"
	pushed, _ := site.SignRecord("p", c, 9)
	h.mu.Lock()
	h.reg.Keys[name] = &Entry{IPNS: name, Record: pushed}
	h.mu.Unlock()
	get := func() []byte {
		resp, err := http.Get(srv.URL + "/routing/v1/ipns/" + name)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return b
	}
	put := func(rec []byte) {
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/routing/v1/ipns/"+name, bytes.NewReader(rec))
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("PUT: %v %v", resp, err)
		}
		resp.Body.Close()
	}
	// the node keeps what it relays; wait until it holds want
	held := func(want []byte) {
		deadline := time.Now().Add(10 * time.Second)
		for {
			if b, err := eng.GetRecord(ctx, name); err == nil && bytes.Equal(b, want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the node never kept the record it was sent")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	older, _ := site.SignRecord("p", c, 5)
	put(older)
	held(older)
	if !bytes.Equal(get(), pushed) {
		t.Fatal("an older record replaced the pushed one")
	}
	renewal, _ := site.SignRecord("p", c, 12)
	put(renewal)
	held(renewal)
	if !bytes.Equal(get(), renewal) {
		t.Fatal("the owner's renewal is newer than the pushed record, and was not answered")
	}
}
```

In `worker/test/push.test.mjs`:
1. Change the import line `import worker, { republish } from "../src/index.js";` to `import worker, { republish, parseRecord } from "../src/index.js";`.
2. In the test "names resolve and route through the node, not delegated-ipfs.dev", replace the lines

```js
    await env.REGISTRY.put("key:" + pushed, JSON.stringify({ ipns: pushed, cid: "bafyx", sequence: 1, record: btoa("signed") }));
    const own = await call("/routing/v1/ipns/" + pushed);
    assert.equal(new TextDecoder().decode(await own.arrayBuffer()), "signed");
    assert.equal(calls.length, 0, "a pushed name is answered from the registry");
```

with

```js
    const held = recordBytes("/ipfs/bafyx", 5); // newer than the node's copy (3)
    await env.REGISTRY.put("key:" + pushed, JSON.stringify({ ipns: pushed, cid: "bafyx", sequence: 5, record: btoa(String.fromCharCode(...held)) }));
    const own = await call("/routing/v1/ipns/" + pushed);
    assert.deepEqual(new Uint8Array(await own.arrayBuffer()), held, "the registry's record is the newer");
    calls.length = 0;
```

3. Append:

```js
test("a pushed name's routing answer is the newer of the registry's record and the node's", async () => {
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", SITES: r2(), REGISTRY: kv() };
  const realFetch = globalThis.fetch;
  let nodeSeq = 9;
  globalThis.fetch = async (u) => {
    if (String(u).startsWith("https://node.test/routing/v1/ipns/")) {
      if (nodeSeq < 0) throw new Error("connection refused");
      return new Response(recordBytes("/ipfs/bafyrenewed", nodeSeq), { headers: { "content-type": "application/vnd.ipfs.ipns-record" } });
    }
    return new Response("nope", { status: 404 });
  };
  const name = "k51qzi5uqu5dlgq33myrm8ik5j5m87add6c1vwaz2ubxhobjtibprs7nc9bnto";
  const get = async () => new Uint8Array(await (await worker.fetch(new Request("https://crop.test/routing/v1/ipns/" + name), env, { waitUntil() {} })).arrayBuffer());
  try {
    const pushed = recordBytes("/ipfs/bafyx", 7);
    await env.REGISTRY.put("key:" + name, JSON.stringify({ ipns: name, cid: "bafyx", sequence: 7, record: btoa(String.fromCharCode(...pushed)) }));
    assert.deepEqual(await get(), recordBytes("/ipfs/bafyrenewed", 9), "the owner's renewal on the node is newer");
    nodeSeq = 3;
    assert.deepEqual(await get(), pushed, "an older copy on the node does not win");
    nodeSeq = -1;
    assert.deepEqual(await get(), pushed, "with the node down, the registry's record");
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("a record cut off inside a number parses to nothing, without hanging", () => {
  assert.deepEqual(parseRecord(Uint8Array.from([0x28, 0x80])), {});
  assert.deepEqual(parseRecord(Uint8Array.from([0x0a, 0x05, 0x2f])), { value: "/" });
});
```

The second assertion reads a value field shorter than its declared length. `slice` stops at the end of the bytes, so the value is the one byte "/".

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -p 1 ./internal/ipfs/ -run TestParseRecord -count=1 ; go test -p 1 ./internal/host/ -run TestRoutingAnswersTheNewestRecord -count=1 ; (cd worker && node --experimental-loader ./text-loader.mjs --test --test-name-pattern="newer of the registry" test/)`
Expected:
- Go: `ParseRecord` is undefined.
- Host: "the owner's renewal is newer than the pushed record, and was not answered".
- Worker: "the owner's renewal on the node is newer".

Do not run the cut-off record test before Step 3: the old `parseRecord` never returns on it.

- [ ] **Step 3: Implement**

In `internal/ipfs/embedded_ipns.go`, add after `ValidateRecord`:

```go
// ParseRecord reads what rec points at, once it is checked to be a valid
// record for name.
func ParseRecord(nameStr string, rec []byte) (*Record, error) {
	if _, err := validRecord(nameStr, rec); err != nil {
		return nil, err
	}
	r, err := ipns.UnmarshalRecord(rec)
	if err != nil {
		return nil, err
	}
	return readRecord(r)
}

// readRecord is what a record says: its value, sequence and validity.
func readRecord(rec *ipns.Record) (*Record, error) {
	seq, _ := rec.Sequence()
	value, err := rec.Value()
	if err != nil {
		return nil, err
	}
	r := &Record{Value: value.String(), Sequence: seq}
	r.Validity, _ = rec.Validity()
	return r, nil
}
```

In `NetworkRecord`, replace everything after `b, err := e.GetRecord(ctx, nameStr)` and its error check with:

```go
	rec, err := ipns.UnmarshalRecord(b)
	if err != nil {
		return nil, err
	}
	return readRecord(rec)
```

In `internal/host/host.go`, replace the `case http.MethodGet, http.MethodHead:` block of `serveRouting` with:

```go
	case http.MethodGet, http.MethodHead:
		h.mu.Lock()
		var rec []byte
		if e := h.reg.Keys[name]; e != nil {
			rec = e.Record
		}
		h.mu.Unlock()
		// The owner's machine renews its record every ten minutes without
		// pushing, and sends each renewal here: the DHT copy can be newer than
		// the pushed one. A record this node holds comes back first, so a pushed
		// site waits only heldWait for it.
		wait := 10 * time.Second // the Worker waits 12 s for this node
		if rec != nil {
			wait = heldWait
		}
		slots := h.lookupSlots // the release must go back to the channel the slot came from
		select {
		case slots <- struct{}{}:
			found, _ := func() ([]byte, error) { // the slot is free again as soon as the search ends
				defer func() { <-slots }()
				ctx, cancel := context.WithTimeout(r.Context(), wait)
				defer cancel()
				return h.Engine.GetRecord(ctx, name)
			}()
			rec = newerRecord(name, rec, found)
		default:
			if rec == nil { // a pushed site needs no slot
				w.Header().Set("Retry-After", "10")
				http.Error(w, "busy", 429)
				return
			}
		}
		if rec == nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.ipfs.ipns-record")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Write(rec)
```

Add near `serveRouting`:

```go
// heldWait is how long a routing GET for a pushed site looks for a newer
// record in this node's DHT store: a held record comes back at once.
const heldWait = 500 * time.Millisecond

// newerRecord is whichever of a and b has the higher sequence; a missing or
// invalid one loses, and a tie keeps a.
func newerRecord(name string, a, b []byte) []byte {
	rb, err := ipfs.ParseRecord(name, b)
	if err != nil {
		return a
	}
	if ra, err := ipfs.ParseRecord(name, a); err == nil && ra.Sequence >= rb.Sequence {
		return a
	}
	return b
}
```

In `worker/src/index.js`, replace the GET part at the top of `routing`. The current code is:

```js
  if (get) {
    const e = await entryByKey(env, name);
    if (e && e.record) {
      const decoded = fromBase64(e.record);
      if (decoded) return new Response(request.method === "HEAD" ? null : decoded, { headers: raw });
    }
  }
```

Replace it with:

```js
  if (get) {
    const e = await entryByKey(env, name);
    const held = e && e.record ? fromBase64(e.record) : null;
    if (held) {
      // the owner's machine renews its record every ten minutes without
      // pushing, and the node keeps those renewals: answer the newer
      const r = env.NODE && await fetch(`${env.NODE}/routing/v1/ipns/${name}`, { headers: { ...UA, Accept: raw["content-type"] }, signal: AbortSignal.timeout(3000) }).catch(() => null);
      const fromNode = r && r.ok ? new Uint8Array(await r.arrayBuffer().catch(() => new ArrayBuffer(0))) : null;
      const best = fromNode && (parseRecord(fromNode).sequence || 0) > (parseRecord(held).sequence || 0) ? fromNode : held;
      return new Response(request.method === "HEAD" ? null : best, { headers: raw });
    }
  }
```

Replace `parseRecord` with a version that cannot run past the end of its bytes:

```js
function parseRecord(b) {
  let i = 0;
  const varint = () => { let r = 0n, s = 0n; for (;;) { if (i >= b.length || s > 63n) throw new Error("cut off"); const c = b[i++]; r |= BigInt(c & 127) << s; s += 7n; if (c < 128) return r; } };
  const out = {};
  try {
    while (i < b.length) {
      const key = Number(varint()), field = key >> 3, wt = key & 7;
      if (wt === 0) { const v = varint(); if (field === 5) out.sequence = Number(v); }
      else if (wt === 2) { const len = Number(varint()); const bytes = b.slice(i, i + len); i += len; if (field === 1) out.value = new TextDecoder().decode(bytes); }
      else break;
    }
  } catch {
    return {};
  }
  return out;
}
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal && go vet ./internal/... && go test -p 1 ./internal/ipfs/ ./internal/host/ ./internal/publish/ -count=1 -timeout 15m && (cd worker && node --experimental-loader ./text-loader.mjs --test test/)`
Expected: all pass; Worker `# fail 0`.

- [ ] **Step 5: Commit**

```bash
git add internal/ipfs/embedded_ipns.go internal/ipfs/embedded_test.go internal/host/host.go internal/host/host_test.go worker/src/index.js worker/test/push.test.mjs
git commit -m "Hosts answer the newest record a site has

The owner's app renews its record without pushing; the routing endpoint
now answers the higher sequence of the pushed record and the DHT copy, so
an agent can sign above the owner's renewals. parseRecord no longer loops
on a record cut off inside a number.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 3: `post --key` reads from the host alone, and goes online only when it must

**What changes for an agent:**
- `post --key` starts its engine offline: loopback only, no bootstrap, so no open ports and no 45-second wait for a routing table.
- **Reads come from the host over HTTP, each checked against its CID:**
  - the root folder's blocks: for a big site, the folder's first block and the shards under it, never the files;
  - the four files a post needs;
  - the newest signed record, from the host's routing endpoint.
- **Sequence:** the routing record decides the post's sequence, replacing today's DHT lookup.
- **Fallback:** if the host cannot serve a block, the engine goes online (`Publisher.Online`) and the post is made again with IPFS reads. That happens for a version pushed before hosts kept folder blocks.

`Post` is split so later tasks can add to it:
- `postCall` holds what every attempt shares;
- `once` is one attempt against the host;
- `onto` builds the new version on top of a base.

**Files:**
- Modify: `internal/ipfs/embedded_unixfs.go` (add `FetchDir` after `DirBlocks`)
- Modify: `internal/ipfs/embedded.go` (add `GoOnline` after `Stop`)
- Modify: `internal/publish/post.go`:
  - `NewPost`, `Posted` and `Post`;
  - `blockLister`, `links` and `readFile`;
  - new: `postCall`, `once`, `onto`, `siteOf`, `sequenceAbove`, `routingRecord`, `hostBlock`, `offline`, `goOnline`, `errNeedsNetwork`.
- Modify: `internal/publish/publish.go` (the `Publisher` struct)
- Modify: `cmd/croptop/main.go`:
  - the `app` struct;
  - `run`'s `post` case;
  - `open`;
  - `println`.
- Test:
  - `internal/ipfs/embedded_test.go`;
  - `internal/publish/post_test.go`.

**Interfaces:**
- Consumes: `ipfs.ParseRecord` (Task 2).
- Produces:
  - `func (e *Embedded) FetchDir(ctx context.Context, c string, get func(string) ([]byte, error)) error`;
  - `func (e *Embedded) GoOnline(ctx context.Context) error`;
  - `Publisher.Online func(ctx context.Context) error`;
  - unexported:
    - `(p *Publisher) offline() bool` and `goOnline(ctx) error`;
    - `var errNeedsNetwork`;
    - `type postCall struct{ p; eng; host, key, ipns string; np NewPost; id string }` with `once(ctx) (Posted, error)` and `onto(ctx, host, base, name, tmp string) (*built, error)`;
    - `type built struct{ cid string; site *store.Site; post *store.Post; changed string }`;
    - `siteOf(st, pub, c, ipns, name, host string) (*store.Site, error)`;
    - `routingRecord(ctx, hostURL, ipns string) (*ipfs.Record, error)`, which answers nil, nil when the host has no record.
  - `Posted` in its final form, with JSON tags; later tasks fill `Existing` (Task 4) and `OnHost: false` (Task 8).
  - Test helper `agentRig`, used by Tasks 4–6:
    - `newAgentRig(t)`;
    - `.agent() (*Publisher, *ipfs.Embedded)`;
    - `.setFault(func(w, req) bool)` and `.failNext(prefix string, statuses ...int)`;
    - `.reset()` and `.asks(method, prefix string) int`;
    - `.routedAt(seq uint64)`;
    - fields `url`, `host`, `pem`, `ipns`, `laptop`, `base`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ipfs/embedded_test.go`:

```go
// FetchDir stores a folder's blocks taken from elsewhere, each checked
// against its CID: a big folder's shards too, never its files. Listing the
// folder then needs no network.
func TestFetchDirTakesAFoldersBlocksFromElsewhere(t *testing.T) {
	ctx := context.Background()
	start := func() *Embedded {
		e := NewEmbedded(t.TempDir())
		e.Offline = true
		if err := e.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { e.Stop() })
		return e
	}
	a, d := start(), start()
	many := map[string]string{}
	name := func(i int) string { return fmt.Sprintf("post-%04d-%s.html", i, strings.Repeat("a", 200)) }
	for i := 0; i < 1500; i++ {
		many[name(i)] = fmt.Sprint(i)
	}
	root, err := a.AddDir(ctx, writeTree(t, many))
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := a.DirBlocks(ctx, root)
	if err != nil || len(dirs) < 2 {
		t.Fatalf("the folder is not sharded (%d blocks, %v); raise the count or the name length", len(dirs), err)
	}
	var asked []string
	get := func(c string) ([]byte, error) { asked = append(asked, c); return a.Block(ctx, c) }
	if err := d.FetchDir(ctx, root, get); err != nil {
		t.Fatal(err)
	}
	if len(asked) != len(dirs) {
		t.Fatalf("fetched %d blocks; the folder has %d", len(asked), len(dirs))
	}
	for _, c := range asked {
		if dirs[c] == nil {
			t.Fatalf("fetched %s, which is not one of the folder's blocks", c)
		}
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if links, err := d.Links(lctx, root); err != nil || len(links) != len(many) {
		t.Fatalf("listing after FetchDir: %d entries, %v", len(links), err)
	}
	asked = nil
	if err := d.FetchDir(ctx, root, get); err != nil || len(asked) != 0 {
		t.Fatalf("a folder already here: asked for %d blocks, %v", len(asked), err)
	}
	forged := func(string) ([]byte, error) { return []byte("forged"), nil }
	if err := start().FetchDir(ctx, root, forged); err == nil {
		t.Fatal("a block that does not hash to its CID was stored")
	}
}

// A node stopped and started again (as GoOnline does, with P2P on) keeps its
// blocks and keys.
func TestARestartedNodeKeepsItsBlocks(t *testing.T) {
	ctx := context.Background()
	e := NewEmbedded(t.TempDir())
	e.Offline = true
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	c, err := e.AddDir(ctx, writeTree(t, map[string]string{"a.html": "a"}))
	if err != nil {
		t.Fatal(err)
	}
	name, _ := e.Keystore().Generate("k")
	if err := e.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Block(ctx, c); err != nil {
		t.Fatalf("a block is gone after the restart: %v", err)
	}
	if got, _ := e.Keystore().Name("k"); got != name {
		t.Fatal("a key is gone after the restart")
	}
}
```

Add `time` to the imports of `embedded_test.go` if missing.

Append to `internal/publish/post_test.go`, adding `time` to its imports:

```go
// agentRig is a host holding the fixture site, which a laptop published, and
// what an agent needs to post to it: nothing but the key. fault, if set, sees
// each request first and may answer it instead of the host.
type agentRig struct {
	t      *testing.T
	url    string       // the host, as agents reach it
	host   http.Handler // the host itself, behind fault
	pem    []byte       // the site's key
	ipns   string
	laptop *ipfs.Embedded
	base   Result // the version the laptop pushed
	mu     sync.Mutex
	fault  func(w http.ResponseWriter, req *http.Request) bool
	asked  []string // "METHOD path" of each request since reset
}

func newAgentRig(t *testing.T) *agentRig {
	t.Helper()
	ctx := context.Background()
	r := &agentRig{t: t}
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	r.host = h
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.asked = append(r.asked, req.Method+" "+req.URL.Path)
		fault := r.fault
		r.mu.Unlock()
		if fault != nil && fault(w, req) {
			return
		}
		h.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	r.url = srv.URL
	r.laptop = offlineNode(t)
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	var err error
	if r.ipns, err = r.laptop.Keystore().Generate(fixtureID); err != nil {
		t.Fatal(err)
	}
	site, _ := s.Site(fixtureID)
	site.IPNS = r.ipns
	SetHost(site, r.url)
	if err := s.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	lp := &Publisher{Store: s, Node: r.laptop, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: r.laptop}}
	if err := lp.Render.Render(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	c, err := r.laptop.AddDir(ctx, s.PublicDir(fixtureID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.laptop.SignRecord(fixtureID, c, 5); err != nil {
		t.Fatal(err)
	}
	if err := lp.Push(ctx, site, c, 5); err != nil {
		t.Fatal(err)
	}
	r.base = Result{CID: c, Sequence: 5}
	if r.pem, err = r.laptop.Keystore().ExportPEM(fixtureID); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *agentRig) setFault(f func(w http.ResponseWriter, req *http.Request) bool) {
	r.mu.Lock()
	r.fault = f
	r.mu.Unlock()
}

// failNext answers the next requests whose path starts with prefix with
// statuses, one each, then lets them through to the host.
func (r *agentRig) failNext(prefix string, statuses ...int) {
	r.setFault(func(w http.ResponseWriter, req *http.Request) bool {
		r.mu.Lock()
		if !strings.HasPrefix(req.URL.Path, prefix) || len(statuses) == 0 {
			r.mu.Unlock()
			return false
		}
		status := statuses[0]
		statuses = statuses[1:]
		r.mu.Unlock()
		http.Error(w, "injected", status)
		return true
	})
}

// reset forgets the requests seen so far.
func (r *agentRig) reset() {
	r.mu.Lock()
	r.asked = nil
	r.mu.Unlock()
}

// asks counts the requests seen since reset with this method and path prefix.
func (r *agentRig) asks(method, prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, a := range r.asked {
		if strings.HasPrefix(a, method+" "+prefix) {
			n++
		}
	}
	return n
}

// agent is a new machine with nothing but the key, as each `croptop post
// --key` is: its own node without P2P, and an empty data directory.
func (r *agentRig) agent() (*Publisher, *ipfs.Embedded) {
	node := offlineNode(r.t)
	st := &store.Store{Root: r.t.TempDir()}
	return &Publisher{Store: st, Node: node, Render: &render.Renderer{Store: st, Templates: templates.FS, CIDs: node}}, node
}

// routedAt waits until the host's routing endpoint answers sequence seq for
// the site.
func (r *agentRig) routedAt(seq uint64) {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if rec, err := routingRecord(context.Background(), r.url, r.ipns); err == nil && rec != nil && rec.Sequence == seq {
			return
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("the host's routing endpoint never answered sequence %d", seq)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// An agent with P2P off posts with what the host serves alone. When the host
// lacks a folder block (a version pushed before hosts kept them), P2P is
// turned on once and the post still goes through, reading over IPFS.
func TestPostOverHTTPOnly(t *testing.T) {
	ctx := context.Background()
	r := newAgentRig(t)
	ap, an := r.agent()
	online := 0
	ap.Online = func(context.Context) error { online++; return nil }
	first, err := ap.Post(ctx, r.url, r.pem, NewPost{Title: "Over HTTP"})
	if err != nil || online != 0 {
		t.Fatalf("post: %v; P2P turned on %d times", err, online)
	}
	if first.Site != r.ipns || first.PostID == "" || !first.OnHost || first.Existing {
		t.Fatalf("posted %+v", first)
	}
	// the next agent finds the host without the root block of the version it
	// builds on; only the first agent has it, over P2P
	r.setFault(func(w http.ResponseWriter, req *http.Request) bool {
		if req.URL.Path != "/v0/host/blocks/"+first.CID {
			return false
		}
		http.Error(w, "not found", 404)
		return true
	})
	bp, bn := r.agent()
	bp.Online = func(ctx context.Context) error { online++; return bn.Dial(ctx, an.Addrs()) }
	second, err := bp.Post(ctx, r.url, r.pem, NewPost{Title: "After turning P2P on"})
	if err != nil || online != 1 {
		t.Fatalf("post: %v; P2P turned on %d times", err, online)
	}
	e, err := hostEntry(ctx, r.url, r.ipns)
	if err != nil || e.CID != second.CID || second.Sequence != first.Sequence+1 {
		t.Fatalf("the host holds %+v, %v; posted %+v after %+v", e, err, second, first)
	}
	b, _, err := httpGet(ctx, r.url+"/ipfs/"+second.CID+"/planet.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Over HTTP", "After turning P2P on"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("planet.json lacks %q", want)
		}
	}
}

// The owner's machine renews its record every ten minutes without pushing,
// and sends each renewal to its host's routing endpoint. An agent posts above
// those renewals, or IPFS readers would keep the owner's older record. A
// newer version there than the host's is the owner's latest publish, still
// uploading: posting on the host's would drop it, so the agent refuses.
func TestPostGoesAboveTheOwnersRenewals(t *testing.T) {
	ctx := context.Background()
	r := newAgentRig(t)
	r.laptop.RoutingPuts = []string{r.url + "/routing/v1/ipns/"}
	announce := func(c string, seq uint64) {
		// no DHT peers here: the record goes to the host's routing endpoint only
		if err := r.laptop.NamePublish(ctx, fixtureID, c, seq); err != nil && !strings.Contains(err.Error(), "failed to find any peer in table") {
			t.Fatal(err)
		}
		r.routedAt(seq)
	}
	announce(r.base.CID, 9) // a renewal: the same version, above the pushed sequence 5
	ap, _ := r.agent()
	res, err := ap.Post(ctx, r.url, r.pem, NewPost{Title: "Above the renewals"})
	if err != nil || res.Sequence != 10 {
		t.Fatalf("posted %+v, %v; the owner renewed at sequence 9", res, err)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("the owner's next version, still uploading"), 0o644)
	newer, err := r.laptop.AddDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	announce(newer, 14)
	bp, _ := r.agent()
	if _, err := bp.Post(ctx, r.url, r.pem, NewPost{Title: "Would drop the owner's publish"}); err == nil || !strings.Contains(err.Error(), "the network has a newer version of this site (sequence 14)") {
		t.Fatalf("posted over a newer version: %v", err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -p 1 ./internal/ipfs/ -run 'TestFetchDir|TestARestartedNode' -count=1 ; go test -p 1 ./internal/publish/ -run 'TestPostOverHTTPOnly|TestPostGoesAboveTheOwnersRenewals' -count=1`
Expected: FAIL to compile:
- `FetchDir`, `Publisher.Online` and `routingRecord` are undefined;
- `Posted` has no `Site`, `PostID`, `OnHost` or `Existing`.

- [ ] **Step 3: Implement the engine half**

In `internal/ipfs/embedded_unixfs.go`, after `DirBlocks`:

```go
// FetchDir stores the blocks that make up the folder c, taking from get those
// this node lacks and checking each against its CID: the folder's own block
// and, for a big (sharded) folder, its shards. Listing c, or adding to it
// with AddOver, then needs no network. Files and subfolders are not fetched.
func (e *Embedded) FetchDir(ctx context.Context, c string, get func(string) ([]byte, error)) error {
	if e.bstore == nil {
		return fmt.Errorf("node not started")
	}
	id, err := cid.Decode(c)
	if err != nil {
		return err
	}
	var walk func(id cid.Cid) error
	walk = func(id cid.Cid) error {
		var data []byte
		if b, err := e.bstore.Get(ctx, id); err == nil {
			data = b.RawData()
		} else {
			if data, err = get(id.String()); err != nil {
				return err
			}
			if err := e.PutBlock(ctx, id.String(), data); err != nil {
				return err
			}
		}
		nd, err := merkledag.DecodeProtobuf(data)
		if err != nil {
			return err
		}
		fsn, err := ft.FSNodeFromBytes(nd.Data())
		if err != nil {
			return err
		}
		if fsn.Type() != ft.THAMTShard {
			return nil // a plain folder is one block
		}
		// a shard's links to its child shards are named by the slot alone;
		// longer names are entries
		pad := len(fmt.Sprintf("%X", fsn.Fanout()-1))
		for _, l := range nd.Links() {
			if len(l.Name) == pad {
				if err := walk(l.Cid); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(id)
}
```

In `internal/ipfs/embedded.go`, after `Stop`:

```go
// GoOnline turns P2P on in a node started Offline: it restarts listening on
// every interface, joins the network and serves bitswap. Blocks and keys stay.
// A node already online is left as it is.
func (e *Embedded) GoOnline(ctx context.Context) error {
	e.mu.Lock()
	off := e.Offline
	e.mu.Unlock()
	if !off {
		return nil
	}
	if err := e.Stop(); err != nil {
		return err
	}
	e.mu.Lock()
	e.Offline = false
	e.mu.Unlock()
	return e.Start(ctx)
}
```

- [ ] **Step 4: Implement the publisher half**

In `internal/publish/publish.go`, add to `Publisher` after `Gate`:

```go
	// Online, if set, turns P2P on in an engine started without it: post --key
	// and croptop mcp start offline and read from the host alone. Until it has
	// run, a block or file the host cannot serve is errNeedsNetwork at once,
	// not a wait on IPFS.
	Online   func(ctx context.Context) error
	onlineMu sync.Mutex
	isOnline bool
```

In `internal/publish/post.go`, replace everything from `// Posted is where a new post went.` through the end of `func (p *Publisher) Post` with:

```go
// Posted is where a new post went: what `croptop post --json` prints and the
// MCP post tool returns.
type Posted struct {
	URL      string `json:"url"`      // the post's page
	CID      string `json:"cid"`      // the site's version with the post
	Sequence uint64 `json:"sequence"` // that version's sequence
	PostID   string `json:"postId"`
	Site     string `json:"site"`     // the site's IPNS name
	Existing bool   `json:"existing"` // the site already had the post: nothing was posted
	OnHost   bool   `json:"onHost"`   // false when the host could not be reached and the post went out over IPFS only
}

// postEngine is what Post needs beyond ipfs.Engine; the embedded engine has it.
type postEngine interface {
	blockLister
	AddOver(ctx context.Context, base, dir string) (string, error)
	SignRecord(key, c string, seq uint64) ([]byte, error)
	FileCID(ctx context.Context, path string) (string, error)
}

// errNeedsNetwork is a read the host cannot serve while P2P is off, such as a
// folder of a version pushed before hosts kept folder blocks.
var errNeedsNetwork = errors.New("the host cannot serve this; reading it over IPFS")

// offline says whether reads must come from the host alone: the engine was
// started without P2P and Online has not run.
func (p *Publisher) offline() bool {
	p.onlineMu.Lock()
	defer p.onlineMu.Unlock()
	return p.Online != nil && !p.isOnline
}

// goOnline turns P2P on, once.
func (p *Publisher) goOnline(ctx context.Context) error {
	p.onlineMu.Lock()
	defer p.onlineMu.Unlock()
	if p.Online == nil || p.isOnline {
		return nil
	}
	if err := p.Online(ctx); err != nil {
		return fmt.Errorf("turning P2P on: %w", err)
	}
	p.isOnline = true
	return nil
}

// Post adds one post to a site from nothing but its key, for machines that
// keep no copy of the site, such as an agent's short-lived worker. It builds
// on the version hostURL holds, renders only the new post, and pushes only
// what changed: the post's folder, planet.json, rss.xml, and pages for new
// tags. Every other file stays as the owner's machine rendered it. The host
// refuses the push if the site changed in the meantime, so no post is lost,
// and it announces the new version, so this machine can exit right after.
//
// Everything is read from the host over HTTP and checked against CIDs. If the
// host cannot serve a block, P2P is turned on (Online) and the post is made
// again, reading over IPFS.
func (p *Publisher) Post(ctx context.Context, hostURL string, key []byte, np NewPost) (Posted, error) {
	eng, ok := p.Node.(postEngine)
	if !ok {
		return Posted{}, errors.New("posting with a key needs the embedded engine (croptop --engine embedded)")
	}
	if strings.TrimSpace(np.Title+np.Content) == "" && len(np.Files) == 0 {
		return Posted{}, errors.New("nothing to post: give a title, content, or files")
	}
	ks := p.Node.Keystore()
	keyName := "post-" + store.NewID() // not the site's id: a planet.json must not choose which key gets replaced
	if err := importKey(ks, keyName, key); err != nil {
		return Posted{}, fmt.Errorf("key: %w", err)
	}
	defer ks.Delete(keyName)
	ipnsName, err := ks.Name(keyName)
	if err != nil {
		return Posted{}, err
	}
	c := &postCall{p: p, eng: eng, host: hostURL, key: keyName, ipns: ipnsName, np: np, id: store.NewID()}
	res, err := c.once(ctx)
	if errors.Is(err, errNeedsNetwork) {
		p.log("%v", err)
		if err = p.goOnline(ctx); err == nil {
			res, err = c.once(ctx)
		}
	}
	return res, err
}

// postCall is one Post: what each attempt of it shares.
type postCall struct {
	p    *Publisher
	eng  postEngine
	host string // the host it posts through
	key  string // the keystore name the site's key is under
	ipns string // the site
	np   NewPost
	id   string // the new post's ID, the same in each attempt
}

// once posts on top of the version the host holds: one attempt.
func (c *postCall) once(ctx context.Context) (Posted, error) {
	hctx, cancel := context.WithTimeout(ctx, hostTimeout)
	entry, err := hostEntry(hctx, c.host, c.ipns)
	cancel()
	if err != nil {
		return Posted{}, err
	}
	if !entry.AcceptsParent {
		// an older host would take the post for the whole site and lose the rest
		return Posted{}, fmt.Errorf("%s cannot add a post to a site yet; it needs updating", c.host)
	}
	seq, err := c.sequenceAbove(ctx, entry)
	if err != nil {
		return Posted{}, err
	}
	tmp, err := os.MkdirTemp(c.p.Store.Root, "post-*")
	if err != nil {
		return Posted{}, err
	}
	defer os.RemoveAll(tmp)
	b, err := c.onto(ctx, c.host, entry.CID, entry.Name, tmp)
	if err != nil {
		return Posted{}, err
	}
	if _, err := c.eng.SignRecord(c.key, b.cid, seq); err != nil {
		return Posted{}, err
	}
	c.p.log("pushing %s at sequence %d", b.cid, seq)
	if err := c.p.pushDir(ctx, b.site, c.key, b.cid, seq, pushSpec{Parent: entry.CID, Dir: b.changed}); err != nil {
		return Posted{}, fmt.Errorf("push to %s: %w", c.host, err)
	}
	return c.posted(b, b.cid, seq, true), nil
}

// sequenceAbove is the sequence a post signs: above the host's version, and
// above the newest record the host's routing endpoint has, such as the
// owner's renewals, which raise the sequence without touching the host. A
// newer version there than the host's is the owner's latest publish, still
// uploading: posting on the host's would drop it.
func (c *postCall) sequenceAbove(ctx context.Context, e *hostKey) (uint64, error) {
	seq := e.Sequence + 1
	rec, err := routingRecord(ctx, c.host, c.ipns)
	switch {
	case err != nil:
		c.p.log("the newest record %s has: %v; posting above its version only", c.host, err)
	case rec != nil && rec.Sequence > e.Sequence && rec.Value != "/ipfs/"+e.CID:
		return 0, fmt.Errorf("the network has a newer version of this site (sequence %d) than %s (sequence %d); publish it from the machine that made it, then post again", rec.Sequence, c.host, e.Sequence)
	case rec != nil:
		seq = max(seq, rec.Sequence+1)
	}
	return seq, nil
}

// routingRecord is the newest record the host's routing endpoint has for
// ipns, checked against the name; nil when it has none.
func routingRecord(ctx context.Context, hostURL, ipns string) (*ipfs.Record, error) {
	hctx, cancel := context.WithTimeout(ctx, hostTimeout)
	defer cancel()
	b, status, err := httpGet(hctx, hostURL+"/routing/v1/ipns/"+ipns)
	switch {
	case err != nil:
		return nil, err
	case status == 404:
		return nil, nil
	case status != 200:
		return nil, fmt.Errorf("%d %s", status, strings.TrimSpace(string(b)))
	}
	return ipfs.ParseRecord(ipns, b)
}

// built is a site's new version, with one more post, ready to send.
type built struct {
	cid     string // the new version
	site    *store.Site
	post    *store.Post
	changed string // the folder of what differs from the version it was built on
}

// onto builds the version that adds c.np to version base, reading what the
// post needs of base from host ("" reads over IPFS): the root listing,
// planet.json, templateSettings.json, avatar.png and index.html. Only the new
// post is rendered; every other file stays as it is in base. name is what the
// host calls the site; tmp holds the work.
func (c *postCall) onto(ctx context.Context, host, base, name, tmp string) (*built, error) {
	published := filepath.Join(tmp, "published")
	c.p.log("reading %s", base)
	links, err := c.p.readVersion(ctx, c.eng, host, base, published)
	if err != nil {
		return nil, err
	}
	st := &store.Store{Root: filepath.Join(tmp, "store")}
	site, err := siteOf(st, published, base, c.ipns, name, c.host)
	if err != nil {
		return nil, err
	}
	if err := navigationPages(st, site.ID, published); err != nil {
		return nil, err
	}
	post, err := addPost(st, site.ID, c.np, c.id)
	if err != nil {
		return nil, err
	}
	r := &render.Renderer{Store: st, Templates: c.p.Render.Templates, TemplateFor: c.p.Render.TemplateFor, CIDs: c.p.Render.CIDs, FFmpeg: c.p.Render.FFmpeg, Log: c.p.Render.Log, Only: post.ID}
	if err := r.Render(ctx, site.ID); err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	changed := filepath.Join(tmp, "changed")
	if err := os.MkdirAll(changed, 0o755); err != nil {
		return nil, err
	}
	names := []string{post.ID, "planet.json", "rss.xml"}
	for t := range post.Tags {
		if page := render.TagPage(t); page != "" && links[page] == "" {
			names = append(names, page)
		}
	}
	for _, n := range names {
		src := filepath.Join(st.PublicDir(site.ID), n)
		if _, err := os.Stat(src); err != nil {
			continue // a template without tag pages
		}
		if err := os.Rename(src, filepath.Join(changed, n)); err != nil {
			return nil, err
		}
	}
	cid, err := c.eng.AddOver(ctx, base, changed)
	if err != nil {
		return nil, err
	}
	return &built{cid: cid, site: site, post: post, changed: changed}, nil
}

// posted is the result of a post: version cid at seq holds b's post.
func (c *postCall) posted(b *built, cid string, seq uint64, onHost bool) Posted {
	return Posted{URL: render.BrowserURL(b.site, b.post), CID: cid, Sequence: seq, PostID: b.post.ID, Site: c.ipns, OnHost: onHost}
}

// siteOf rebuilds in st the site whose published files from version c are in
// pub, and checks that it is the site at ipns. name is what the host calls the
// site, for sites published before planet.json carried it; host is where the
// site pushes.
func siteOf(st *store.Store, pub, c, ipns, name, host string) (*store.Site, error) {
	var head struct {
		ID   string `json:"id"`
		IPNS string `json:"ipns"`
	}
	if b, err := os.ReadFile(filepath.Join(pub, "planet.json")); err != nil || json.Unmarshal(b, &head) != nil || head.ID == "" {
		return nil, fmt.Errorf("%s is not a Croptop site (no planet.json with an id)", c)
	}
	if head.IPNS != ipns {
		return nil, fmt.Errorf("that version is the site %s, not %s", head.IPNS, ipns)
	}
	if err := rebuildSource(st, head.ID, pub); err != nil {
		return nil, err
	}
	site, err := st.Site(head.ID)
	if err != nil {
		return nil, err
	}
	if NameOf(site) == "" && name != "" {
		setRaw(site, NameKey, name)
	}
	SetHost(site, host)
	return site, st.SaveSite(site)
}
```

Change `addPost` to take the post's ID: its signature becomes `func addPost(st *store.Store, siteID string, np NewPost, id string) (*store.Post, error)`. Its first line, `id, empty := store.NewID(), ""`, becomes `empty := ""`. In `post_test.go`, its one caller becomes `addPost(s, fixtureID, NewPost{Title: "Old post", Files: []string{photo}}, store.NewID())`.

Replace `blockLister`, `links` and `readFile` with:

```go
// blockLister lists folders, storing blocks fetched from a host first.
type blockLister interface {
	Links(ctx context.Context, c string) (map[string]string, error)
	PutBlock(ctx context.Context, c string, data []byte) error
	Block(ctx context.Context, c string) ([]byte, error) // only what this node holds
	FetchDir(ctx context.Context, c string, get func(string) ([]byte, error)) error
}

// links lists the directory c. Its blocks (a big folder's shards too) come
// from the host first, which has a version the moment it is pushed, each
// checked against its CID. IPFS is the fallback, and while P2P is off a block
// the host cannot serve is errNeedsNetwork at once. hostURL "" reads over
// IPFS only. A listing cut short is an error, never a part of one: the engine
// answers a sharded folder whose other blocks never came with what it had read
// when the time ran out, and no error.
func (p *Publisher) links(ctx context.Context, eng blockLister, hostURL, c string) (map[string]string, error) {
	if hostURL != "" {
		err := eng.FetchDir(ctx, c, func(b string) ([]byte, error) { return hostBlock(ctx, hostURL, b) })
		if err != nil && p.offline() {
			return nil, fmt.Errorf("folder %s: %v: %w", c, err, errNeedsNetwork)
		}
	}
	lctx, cancel := context.WithTimeout(ctx, ipfsFetchTimeout)
	defer cancel()
	links, err := eng.Links(lctx, c)
	if err == nil && lctx.Err() != nil {
		return nil, fmt.Errorf("%s: %w", c, lctx.Err())
	}
	return links, err
}

// hostBlock is one raw block from the host. A host that hangs costs at most
// hostTimeout.
func hostBlock(ctx context.Context, hostURL, c string) ([]byte, error) {
	hctx, cancel := context.WithTimeout(ctx, hostTimeout)
	defer cancel()
	b, status, err := httpGet(hctx, hostURL+"/v0/host/blocks/"+c)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("%s has no block %s (%d)", hostURL, c, status)
	}
	return b, nil
}

// readFile puts the file at rel in version root into dest. want is its CID
// from its folder's listing: bytes from the host are kept only if they hash
// to it, otherwise the file comes over IPFS, or, while P2P is off, the read
// is errNeedsNetwork. hostURL "" reads over IPFS only.
func (p *Publisher) readFile(ctx context.Context, eng postEngine, hostURL, root, rel, want, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if hostURL != "" {
		segs := strings.Split(rel, "/")
		for i := range segs {
			segs[i] = url.PathEscape(segs[i])
		}
		if b, status, err := httpGet(ctx, hostURL+"/ipfs/"+root+"/"+strings.Join(segs, "/")); err == nil && status == 200 && os.WriteFile(dest, b, 0o644) == nil {
			if got, _ := eng.FileCID(ctx, dest); got == want {
				return nil
			}
			p.log("%s from %s does not match %s; reading it over IPFS", rel, hostURL, root)
			os.Remove(dest)
		}
	}
	if p.offline() {
		return fmt.Errorf("%s: %w", rel, errNeedsNetwork)
	}
	gctx, cancel := context.WithTimeout(ctx, ipfsFetchTimeout)
	defer cancel()
	return p.Node.Get(gctx, "/ipfs/"+want, dest)
}
```

- [ ] **Step 5: Start `post --key` offline, with logs on stderr**

In `cmd/croptop/main.go`:
1. Add `"io"` to the imports.
2. Add a field to the `app` struct: `offline bool // post --key and mcp: no P2P until the publisher needs it`.
3. Replace `func println(s string) { fmt.Println(s) }` with:

```go
// logs is where log lines go: stdout, except for post --key and mcp, whose
// stdout carries only their result, or the MCP protocol.
var logs io.Writer = os.Stdout

func println(s string) { fmt.Fprintln(logs, s) }
```

4. In `run`'s first `switch`, in the `post` case, replace `a.dataDir = tmp` with:

```go
		a.dataDir, a.offline, logs = tmp, true, os.Stderr
```

5. In `open`, the embedded branch becomes:

```go
	var online func(context.Context) error
	if a.cfg.EngineName() == "embedded" {
		e := ipfs.NewEmbedded(a.dataDir)
		e.Log = logf
		e.RoutingPuts = ipfs.DefaultRoutingPuts
		e.PeersURL = ipfs.DefaultPeersURL
		// post --key and mcp start without P2P: everything comes from the host
		// over HTTP, and the publisher turns P2P on only when the host cannot serve
		e.Offline = a.offline
		if a.offline {
			online = e.GoOnline
		}
		a.engine = e
	} else {
```

The `else` branch stays as it is. At the end of `open`, the publisher line becomes:

```go
	a.pub = &publish.Publisher{Store: a.store, Node: a.engine, Render: r, Log: println, Wait: a.pubWait, Online: online}
```

- [ ] **Step 6: Run the tests**

Run: `gofmt -l internal cmd && go vet ./... && go test -p 1 ./internal/ipfs/ ./internal/publish/ -count=1 -timeout 15m`
Expected: all pass, the B tests included. They exercise `links` through changes-only publishing and take-ins, and their hosts serve folder blocks now fetched through `FetchDir`.

**Mutation check:** make `links` ignore `p.offline()`. `TestPostOverHTTPOnly` must then fail: the second post waits on IPFS instead of turning P2P on. Task 11's live check measures the time a post takes against crop.top.

- [ ] **Step 7: Commit**

```bash
git add internal/ipfs/embedded_unixfs.go internal/ipfs/embedded.go internal/ipfs/embedded_test.go internal/publish/post.go internal/publish/publish.go internal/publish/post_test.go cmd/croptop/main.go
git commit -m "post --key reads from the host alone; P2P only when it must

The engine starts offline. Folder blocks (a big site's shards too), the
files a post needs, and the newest record come from the host over HTTP,
checked against CIDs; the record sets a sequence above the owner's
renewals. A block the host cannot serve turns P2P on, once.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: `--id` and `--json`

**What changes:**
- **`--id <key>`** makes the post ID the SHA-256 of `<ipns>/<key>`, written as an uppercase UUID with version nibble 5.
- **A post that is already there.** If the version on the host already has that post, the command returns it (`existing: true`) and posts nothing. The same check runs on every attempt, so a command whose earlier attempt landed never posts twice.
- **`--json`** prints the `Posted` result as one JSON object.

**Files:**
- Modify: `internal/publish/post.go`:
  - `NewPost`, `Post`, `built`, `onto`, `once` and `posted`;
  - add `postIDFor`.
- Modify: `cmd/croptop/main.go`:
  - flags, `flagsFirst`'s `boolFlags`;
  - the `post` case of the last `switch`;
  - the usage text.
- Test: `internal/publish/post_test.go`

**Interfaces:**
- Consumes:
  - `postCall`, `built`, `Posted`, `agentRig` (Task 3);
  - `addPost(st, siteID, np, id)` (Task 3).
- Produces:
  - `NewPost.ID string`;
  - `postIDFor(ipns, key string) string`;
  - `built.existing bool`.

- [ ] **Step 1: Write the failing tests** (append to `internal/publish/post_test.go`)

```go
// The post ID --id gives is the same for the same key on the same site,
// different on another site, and looks like every other post ID.
func TestPostIDFor(t *testing.T) {
	a := postIDFor("k51site", "task-1")
	if a != postIDFor("k51site", "task-1") {
		t.Fatal("the same key gave two IDs")
	}
	if a == postIDFor("k51other", "task-1") || a == postIDFor("k51site", "task-2") {
		t.Fatal("another site or key gave the same ID")
	}
	if !regexp.MustCompile(`^[0-9A-F]{8}-[0-9A-F]{4}-5[0-9A-F]{3}-[89AB][0-9A-F]{3}-[0-9A-F]{12}$`).MatchString(a) {
		t.Fatalf("%s is not an uppercase UUID with version 5", a)
	}
}

// Posting again with the same --id, from another machine even, finds the
// first post and posts nothing; another --id posts.
func TestPostWithTheSameIDPostsOnce(t *testing.T) {
	ctx := context.Background()
	r := newAgentRig(t)
	ap, _ := r.agent()
	first, err := ap.Post(ctx, r.url, r.pem, NewPost{Title: "Once", ID: "task-1"})
	if err != nil || first.Existing || first.PostID != postIDFor(r.ipns, "task-1") {
		t.Fatalf("first: %+v, %v", first, err)
	}
	bp, _ := r.agent()
	r.reset()
	again, err := bp.Post(ctx, r.url, r.pem, NewPost{Title: "Once", ID: "task-1"})
	if err != nil || !again.Existing || again.PostID != first.PostID || again.CID != first.CID || again.URL != first.URL || again.Sequence != first.Sequence {
		t.Fatalf("again: %+v, %v; first %+v", again, err, first)
	}
	if n := r.asks("POST", "/v0/host/push"); n != 0 {
		t.Fatalf("pushed %d times for a post that was there", n)
	}
	other, err := bp.Post(ctx, r.url, r.pem, NewPost{Title: "Twice", ID: "task-2"})
	if err != nil || other.Existing || other.Sequence != first.Sequence+1 {
		t.Fatalf("another id: %+v, %v", other, err)
	}
}
```

Add `regexp` to the test file's imports.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -p 1 ./internal/publish/ -run 'TestPostIDFor|TestPostWithTheSameIDPostsOnce' -count=1`
Expected: FAIL to compile (`postIDFor` and `NewPost.ID` are undefined).

- [ ] **Step 3: Implement**

In `NewPost`, add after `Files`:

```go
	// ID, if set, names the post: the same ID on the same site always gives
	// the same post ID, so a command run again finds its post instead of
	// adding it twice.
	ID string
```

In `Post`, the line building `c` becomes:

```go
	id := store.NewID()
	if np.ID != "" {
		id = postIDFor(ipnsName, np.ID)
	}
	c := &postCall{p: p, eng: eng, host: hostURL, key: keyName, ipns: ipnsName, np: np, id: id}
```

Add after `Post`:

```go
// postIDFor is the post ID --id key gives on the site at ipns: the SHA-256 of
// "<ipns>/<key>" written as an uppercase UUID (version 5, RFC 4122 variant),
// the form of every post ID.
func postIDFor(ipns, key string) string {
	b := sha256.Sum256([]byte(ipns + "/" + key))
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}
```

Add `crypto/sha256` to `post.go`'s imports.

In `built`, add `existing bool // the version already has the post; nothing was built`.

In `onto`, right after the `siteOf` call and its error check:

```go
	if old, err := st.Post(site.ID, c.id); err == nil {
		return &built{cid: base, site: site, post: old, existing: true}, nil
	}
```

In `once`, right after `b, err := c.onto(...)` and its error check:

```go
	if b.existing {
		return c.posted(b, entry.CID, entry.Sequence, true), nil
	}
```

In `posted`, set `Existing: b.existing` in the returned `Posted`.

In `cmd/croptop/main.go`:
1. Add the flags next to `postHost`:

```go
	jsonOut := fs.Bool("json", false, "print the result as one JSON object (post --key)")
	postID := fs.String("id", "", "a stable key for the post, such as a task ID: posting again with it returns the first post (post --key)")
```

2. Add `"json": true` to `flagsFirst`'s `boolFlags`. Without it, `--json` would swallow the argument after it.
3. Replace the `post` case of the last `switch` with:

```go
	case "post":
		res, err := a.pub.Post(ctx, strings.TrimSuffix(*postHost, "/"), postKey, publish.NewPost{Title: *title, Content: *content, Tags: *tags, Files: rest, ID: *postID})
		if err != nil {
			return err
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(res)
		}
		verb := "posted"
		if res.Existing {
			verb = "already posted"
		}
		fmt.Printf("%s %s\n  cid      %s\n  sequence %d\n", verb, res.URL, res.CID, res.Sequence)
		return nil
```

4. In `usage`, the `post --key` lines become:

```
  croptop post --key site.pem --title "…" [--content "…"] [--tags a,b] [--host url] [--id key] [--json] [files]
                           post without a copy of the site, on top of the version its host
                           (crop.top unless --host) holds; for agents and machines that keep no state.
                           --id makes a retry return the first post; --json prints the result as JSON
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal cmd && go vet ./... && go test -p 1 ./internal/publish/ -count=1 -timeout 15m`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/publish/post.go internal/publish/post_test.go cmd/croptop/main.go
git commit -m "post --key: --id posts once however often it runs; --json

The same --id on the same site gives the same post ID; a version that has
the post returns it (existing) and nothing is pushed. --json prints the
result as one JSON object.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 5: Retries

**What is retried:** a 409, a 429, a 5xx, or no answer at all. Each is tried again three times, after 2 s, 5 s and 10 s. Each attempt reads the host's version again, so a site that moved meanwhile is built on afresh.

**What is not retried:** any other 4xx. Neither is an error that is not the host's: "no version there", "the network has a newer version", or a bad tag.

The classification lives in two functions. Task 7 and Task 8 reuse `unreachable` for "the host is down":
- `retryable`;
- `unreachable`: no answer, or a 5xx.

**Files:**
- Modify: `internal/publish/push.go`:
  - `hostRefusal` gains a status;
  - `answer`'s 429 wraps `errHostBusy`.
- Modify: `internal/publish/post.go`:
  - `hostEntry`'s errors are typed;
  - `Post` calls `retrying`;
  - add `retrying`, `retryable`, `unreachable`, `postRetryWaits`.
- Test: `internal/publish/post_test.go`

**Interfaces:**
- Consumes: `postCall.once` (Task 3).
- Produces:
  - `type hostRefusal struct{ msg string; status int }`;
  - `var errHostBusy`;
  - `func unreachable(err error) bool`, `func retryable(err error) bool`;
  - `func (c *postCall) retrying(ctx) (Posted, error)`;
  - `var postRetryWaits []time.Duration`.

- [ ] **Step 1: Write the failing tests** (append to `internal/publish/post_test.go`)

```go
// What counts as the host not answering: no answer at all, or a server error.
// A refusal, a conflict, "no version here", a busy host and a caller that gave
// up are answers, or not the host's doing.
func TestUnreachable(t *testing.T) {
	for _, tc := range []struct {
		what string
		err  error
		want bool
	}{
		{"connection refused", &url.Error{Op: "Get", URL: "https://crop.top", Err: errors.New("connection refused")}, true},
		{"timed out", &url.Error{Op: "Get", URL: "https://crop.top", Err: context.DeadlineExceeded}, true},
		{"bad gateway", &hostRefusal{"502", 502}, true},
		{"refused", &hostRefusal{"400", 400}, false},
		{"conflict", &hostConflict{"moved"}, false},
		{"no version", fmt.Errorf("x: %w", errNoVersion), false},
		{"busy", fmt.Errorf("x: %w", errHostBusy), false},
		{"the caller gave up", &url.Error{Op: "Get", URL: "https://crop.top", Err: context.Canceled}, false},
		{"no error", nil, false},
	} {
		if got := unreachable(tc.err); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.what, got, tc.want)
		}
	}
}

// A post the host turned down for now is tried again, reading the host each
// time: busy (429), failing (5xx), the site moved meanwhile (409). A refusal
// (4xx) is not.
func TestPostRetriesWhatMayPassLater(t *testing.T) {
	ctx := context.Background()
	waits := postRetryWaits
	postRetryWaits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { postRetryWaits = waits })
	r := newAgentRig(t)
	for _, tc := range []struct {
		what     string
		path     string
		statuses []int
		posts    bool
		attempts int // each attempt reads the host's version first
	}{
		{"the host is failing", "/v0/host/keys/", []int{503, 502}, true, 3},
		{"the host is busy", "/v0/host/keys/", []int{429}, true, 2},
		{"the site moved while the post was made", "/v0/host/push", []int{409}, true, 2},
		{"the push failed on the host", "/v0/host/push", []int{500, 500, 500}, true, 4},
		{"the host refuses the post", "/v0/host/push", []int{400}, false, 1},
		{"the host keeps failing", "/v0/host/keys/", []int{503, 503, 503, 503}, false, 4},
	} {
		r.failNext(tc.path, tc.statuses...)
		r.reset()
		ap, _ := r.agent()
		_, err := ap.Post(ctx, r.url, r.pem, NewPost{Title: tc.what})
		if posted := err == nil; posted != tc.posts {
			t.Errorf("%s: posted %v (%v), want %v", tc.what, posted, err, tc.posts)
		}
		if n := r.asks("GET", "/v0/host/keys/"); n != tc.attempts {
			t.Errorf("%s: %d attempts, want %d", tc.what, n, tc.attempts)
		}
	}
}

// A push the host committed but whose answer never came is not posted twice:
// the next attempt finds the post on the host and returns it. One command
// keeps one post ID across its attempts, --id or not.
func TestALostAnswerIsNotPostedTwice(t *testing.T) {
	ctx := context.Background()
	waits := postRetryWaits
	postRetryWaits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { postRetryWaits = waits })
	r := newAgentRig(t)
	lost := false
	r.setFault(func(w http.ResponseWriter, req *http.Request) bool {
		if lost || req.Method != "POST" || req.URL.Path != "/v0/host/push" {
			return false
		}
		lost = true
		r.host.ServeHTTP(httptest.NewRecorder(), req) // the host commits the post
		http.Error(w, "bad gateway", 502)            // and its answer is lost
		return true
	})
	ap, _ := r.agent()
	res, err := ap.Post(ctx, r.url, r.pem, NewPost{Title: "Exactly once"})
	if err != nil || !res.Existing {
		t.Fatalf("%+v, %v", res, err)
	}
	e, err := hostEntry(ctx, r.url, r.ipns)
	if err != nil || e.CID != res.CID {
		t.Fatalf("the host holds %+v, %v; the post says %s", e, err, res.CID)
	}
	b, _, err := httpGet(ctx, r.url+"/ipfs/"+e.CID+"/planet.json")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "Exactly once"); n != 1 {
		t.Fatalf("planet.json has the post %d times", n)
	}
}
```

Add `errors` and `fmt` to the test file's imports if missing.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -p 1 ./internal/publish/ -run 'TestUnreachable|TestPostRetriesWhatMayPassLater|TestALostAnswerIsNotPostedTwice' -count=1`
Expected: FAIL to compile:
- `unreachable`, `errHostBusy` and `postRetryWaits` are undefined;
- `hostRefusal` has one field.

- [ ] **Step 3: Implement**

In `internal/publish/push.go`, replace `hostRefusal` with:

```go
// hostRefusal is any other answer than 200, 409 or 429: the host took the
// request and said no. A 4xx will not change if the same is sent again; a
// 5xx is the host failing, which a later try may not meet.
type hostRefusal struct {
	msg    string
	status int
}
```

Add to the `var` block that holds `errStalled`:

```go
	// errHostBusy is a host's 429: try again later.
	errHostBusy = errors.New("host is busy")
```

In `pushDir`'s `answer`, the end becomes:

```go
		msg := fmt.Sprintf("%s: %s: %s", what, resp.Status, strings.TrimSpace(string(body)))
		if resp.StatusCode == http.StatusTooManyRequests {
			return "", fmt.Errorf("%s: %w", msg, errHostBusy) // later, not never
		}
		return "", &hostRefusal{msg, resp.StatusCode}
```

In `internal/publish/post.go`, replace `hostEntry`'s body after the `httpGet` error check with:

```go
	var e hostKey
	switch {
	case status == 404 || (status == 200 && json.Unmarshal(b, &e) == nil && e.CID == ""):
		return nil, fmt.Errorf("%s holds no version of %s; publish the site once from the console, which pushes it there (%w)", hostURL, ipnsName, errNoVersion)
	case status == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%s: %w", hostURL, errHostBusy)
	case status != 200 || e.CID == "":
		return nil, &hostRefusal{fmt.Sprintf("%s: %d %s", hostURL, status, strings.TrimSpace(string(b))), status}
	}
	return &e, nil
```

Add after `postCall`:

```go
// postRetryWaits are the waits before each retry of a post the host turned
// down for now, or never answered. A variable so tests can shorten them.
var postRetryWaits = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}

// retrying makes the post, trying again (postRetryWaits) while the host turns
// it down for now. Each attempt reads the host's version afresh.
func (c *postCall) retrying(ctx context.Context) (Posted, error) {
	for i := 0; ; i++ {
		res, err := c.once(ctx)
		if err == nil || i == len(postRetryWaits) || !retryable(err) {
			return res, err
		}
		c.p.log("%v; trying again in %s", err, postRetryWaits[i])
		select {
		case <-ctx.Done():
			return Posted{}, ctx.Err()
		case <-time.After(postRetryWaits[i]):
		}
	}
}

// retryable says whether a post the host did not take may go through if sent
// again: the site moved meanwhile (409), the host is busy (429) or failing
// (5xx), or no answer came.
func retryable(err error) bool {
	var hc *hostConflict
	return errors.As(err, &hc) || errors.Is(err, errHostBusy) || unreachable(err)
}

// unreachable says whether err means the host could not be asked: no answer
// (refused, timed out, cut off) or a server error (5xx). Any other answer,
// such as a refusal, a conflict or "no version here", is the host speaking;
// a caller that gave up is no fault of the host's.
func unreachable(err error) bool {
	var hr *hostRefusal
	var ue *url.Error
	switch {
	case err == nil, errors.Is(err, context.Canceled):
		return false
	case errors.As(err, &hr):
		return hr.status >= 500
	}
	return errors.As(err, &ue) || errors.Is(err, errStalled) || errors.Is(err, errNoAnswer)
}
```

In `Post`, both calls `c.once(ctx)` become `c.retrying(ctx)`.

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal && go vet ./internal/... && go test -p 1 ./internal/publish/ -count=1 -timeout 15m`
Expected: all pass, B's background tests included. `runPushes` retries every error as before; `pushChangesOrAll` still sends the whole version on any `hostRefusal`.

- [ ] **Step 5: Commit**

```bash
git add internal/publish/push.go internal/publish/post.go internal/publish/post_test.go
git commit -m "post --key retries what may pass later

A 409, 429, 5xx or no answer is tried again three times, reading the
host afresh each time; a refusal is not. One command keeps one post ID,
so an attempt that landed with its answer lost is found, not repeated.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: `croptop key new` and `post --create`

**What an agent can do:** start a site of its own, with no console:
- `croptop key new > site.pem` prints a fresh key, and the site's address goes to stderr;
- `croptop post --key site.pem --create "<name>" …` makes the site's first version, holding the post, when the host holds none for the key.

**`--create` is refused when the host's routing endpoint already has a record for the key.** The site then exists elsewhere, and a new one would replace it. When the host holds a version, `--create` is ignored.

This task also adds the first command-line test. It runs the agent's commands in-process and checks that stdout holds only the key and the JSON results.

**Files:**
- Modify: `internal/publish/post.go`:
  - `NewPost`;
  - `once`;
  - add `create`.
- Modify: `cmd/croptop/main.go`:
  - the first `switch` (`key new`);
  - the `--create` flag;
  - the last `post` case;
  - the usage text;
  - add `keyNew`.
- Create: `cmd/croptop/main_test.go`
- Test: `internal/publish/post_test.go`

**Interfaces:**
- Consumes:
  - `postCall`, `routingRecord`, `addPost(…, id)`, `Posted` (Task 3);
  - `retrying` (Task 5).
- Produces:
  - `NewPost.Create string`;
  - `func (c *postCall) create(ctx) (Posted, error)`;
  - `func keyNew() error` (package main);
  - test helpers in package main:
    - `testHost(t) string` (the URL of a host on an offline engine);
    - `stdout(t, f func() error) []byte`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/publish/post_test.go`:

```go
// An agent with a fresh key starts its own site: --create makes the first
// version, holding the post, when the host has none for the key; later posts
// go on top, and --create then changes nothing. A key whose site the network
// already knows is never given a new one.
func TestPostCreatesASite(t *testing.T) {
	ctx := context.Background()
	r := newAgentRig(t)
	keys := offlineNode(t)
	name, _ := keys.Keystore().Generate("bot")
	pem, _ := keys.Keystore().ExportPEM("bot")
	ap, _ := r.agent()
	if _, err := ap.Post(ctx, r.url, pem, NewPost{Title: "No site yet"}); !errors.Is(err, errNoVersion) {
		t.Fatalf("without --create: %v", err)
	}
	first, err := ap.Post(ctx, r.url, pem, NewPost{Title: "Hello", Create: "Bot"})
	if err != nil || first.Sequence != 1 || first.Site != name || first.Existing {
		t.Fatalf("create: %+v, %v", first, err)
	}
	if e, err := hostEntry(ctx, r.url, name); err != nil || e.CID != first.CID {
		t.Fatalf("the host holds %+v, %v", e, err)
	}
	second, err := ap.Post(ctx, r.url, pem, NewPost{Title: "And again", Create: "Ignored"})
	if err != nil || second.Sequence != 2 {
		t.Fatalf("on top: %+v, %v", second, err)
	}
	b, _, err := httpGet(ctx, r.url+"/ipfs/"+second.CID+"/planet.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"Bot"`, "Hello", "And again"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("planet.json lacks %s", want)
		}
	}
	if strings.Contains(string(b), "Ignored") {
		t.Error("--create renamed a site the host holds")
	}

	// a site the network knows, which this host does not hold, is not replaced
	keys.RoutingPuts = []string{r.url + "/routing/v1/ipns/"}
	other, _ := keys.Keystore().Generate("elsewhere")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("published elsewhere"), 0o644)
	c, err := keys.AddDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := keys.NamePublish(ctx, "elsewhere", c, 3); err != nil && !strings.Contains(err.Error(), "failed to find any peer in table") {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if rec, err := routingRecord(ctx, r.url, other); err == nil && rec != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the record never reached the host's routing endpoint")
		}
		time.Sleep(50 * time.Millisecond)
	}
	opem, _ := keys.Keystore().ExportPEM("elsewhere")
	if _, err := ap.Post(ctx, r.url, opem, NewPost{Title: "Would replace it", Create: "New"}); err == nil || !strings.Contains(err.Error(), "already has a site") {
		t.Fatalf("--create over a site the network knows: %v", err)
	}
}
```

Create `cmd/croptop/main_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
)

// testHost is a host on an engine without P2P, as in the publish tests.
func testHost(t *testing.T) string {
	t.Helper()
	e := ipfs.NewEmbedded(t.TempDir())
	e.Offline = true
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Stop() })
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: e}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// stdout runs f and returns what it printed on stdout.
func stdout(t *testing.T, f func() error) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	real := os.Stdout
	os.Stdout = w
	out := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); out <- b }()
	ferr := f()
	os.Stdout = real
	w.Close()
	b := <-out
	if ferr != nil {
		t.Fatalf("%v (stdout %q)", ferr, b)
	}
	return b
}

// An agent's commands, end to end against a host: a new key, a new site with
// its first post, then the same post again. stdout holds only the key and the
// JSON results; log lines go to stderr.
func TestAgentCommands(t *testing.T) {
	url := testHost(t)
	pem := stdout(t, func() error { return run([]string{"key", "new"}) })
	if !bytes.HasPrefix(pem, []byte("-----BEGIN PRIVATE KEY-----")) {
		t.Fatalf("key new printed %q", pem)
	}
	key := filepath.Join(t.TempDir(), "site.pem")
	if err := os.WriteFile(key, pem, 0o600); err != nil {
		t.Fatal(err)
	}
	post := func(args ...string) map[string]any {
		out := stdout(t, func() error {
			return run(append([]string{"post", "--key", key, "--host", url, "--json"}, args...))
		})
		var res map[string]any
		if err := json.Unmarshal(out, &res); err != nil {
			t.Fatalf("stdout is not one JSON object: %q", out)
		}
		for _, k := range []string{"url", "cid", "sequence", "postId", "site", "existing", "onHost"} {
			if _, ok := res[k]; !ok {
				t.Errorf("the result has no %q: %s", k, out)
			}
		}
		return res
	}
	first := post("--create", "Bot", "--title", "Hello", "--id", "task-1")
	if first["existing"] != false || first["onHost"] != true || first["sequence"] != 1.0 {
		t.Fatalf("first post: %v", first)
	}
	again := post("--title", "Hello", "--id", "task-1")
	if again["existing"] != true || again["postId"] != first["postId"] || again["cid"] != first["cid"] {
		t.Fatalf("the same post again: %v; first %v", again, first)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -p 1 ./internal/publish/ -run TestPostCreatesASite -count=1 ; go test -p 1 ./cmd/croptop/ -count=1`
Expected: FAIL. `NewPost.Create` is undefined. `key new` fails with "usage: croptop key export <site> | key import <site> <file.pem>".

- [ ] **Step 3: Implement**

In `NewPost`, add after `ID`:

```go
	// Create, if set, is the name of a new site to start when the host holds
	// no version for the key: the first version holds the post.
	Create string
```

In `once`, right after `hostEntry` and `cancel()`, before `if err != nil`:

```go
	if errors.Is(err, errNoVersion) && c.np.Create != "" {
		return c.create(ctx)
	}
```

Add after `once`:

```go
// create starts a new site for the key, named c.np.Create, whose first
// version holds the post. Only for a key with no site anywhere the host can
// see: a record means the site exists, and a new one would replace it.
func (c *postCall) create(ctx context.Context) (Posted, error) {
	rec, err := routingRecord(ctx, c.host, c.ipns)
	if err != nil {
		return Posted{}, fmt.Errorf("asking %s whether this key has a site: %w", c.host, err)
	}
	if rec != nil {
		return Posted{}, fmt.Errorf("this key already has a site (sequence %d, %s) that %s does not hold; --create starts new sites only", rec.Sequence, strings.TrimPrefix(rec.Value, "/ipfs/"), c.host)
	}
	tmp, err := os.MkdirTemp(c.p.Store.Root, "post-*")
	if err != nil {
		return Posted{}, err
	}
	defer os.RemoveAll(tmp)
	st := &store.Store{Root: filepath.Join(tmp, "store")}
	now, no := store.Now(), false
	site := &store.Site{ID: store.NewID(), Name: c.np.Create, IPNS: c.ipns, TemplateName: "Croptop", Created: now, Updated: now, Archived: &no, Tags: map[string]string{}}
	SetHost(site, c.host)
	if err := st.SaveSite(site); err != nil {
		return Posted{}, err
	}
	if meta, err := render.LoadMeta(c.p.Render.Templates); err == nil {
		if err := st.SaveTemplateSettings(site.ID, meta.SettingsWithDefaults(nil)); err != nil {
			return Posted{}, err
		}
	}
	post, err := addPost(st, site.ID, c.np, c.id)
	if err != nil {
		return Posted{}, err
	}
	r := &render.Renderer{Store: st, Templates: c.p.Render.Templates, TemplateFor: c.p.Render.TemplateFor, CIDs: c.p.Render.CIDs, FFmpeg: c.p.Render.FFmpeg, Log: c.p.Render.Log}
	if err := r.Render(ctx, site.ID); err != nil {
		return Posted{}, fmt.Errorf("render: %w", err)
	}
	cid, err := c.p.Node.AddDir(ctx, st.PublicDir(site.ID))
	if err != nil {
		return Posted{}, err
	}
	const seq = 1
	if _, err := c.eng.SignRecord(c.key, cid, seq); err != nil {
		return Posted{}, err
	}
	c.p.log("pushing the first version of %s, %s", c.np.Create, cid)
	if err := c.p.pushDir(ctx, site, c.key, cid, seq, pushSpec{Dir: st.PublicDir(site.ID)}); err != nil {
		return Posted{}, fmt.Errorf("push to %s: %w", c.host, err)
	}
	return c.posted(&built{site: site, post: post}, cid, seq, true), nil
}
```

In `cmd/croptop/main.go`:
1. Add the flag next to `postID`:

```go
	create := fs.String("create", "", "start a new site with this name when the key has none on its host (post --key)")
```

2. In the first `switch`, add before `case "service":`:

```go
	case "key":
		if len(rest) == 1 && rest[0] == "new" {
			return keyNew()
		}
```

3. In the last `post` case, add `Create: *create` to the `publish.NewPost` literal.
4. Add after `run`:

```go
// keyNew prints a new site key (PEM) on stdout and the site's address, its
// IPNS name, on stderr: all an agent needs to start a site of its own with
// post --create.
func keyNew() error {
	tmp, err := os.MkdirTemp("", "croptop-key-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	ks := &ipfs.Keystore{Dir: tmp}
	name, err := ks.Generate("new")
	if err != nil {
		return err
	}
	pem, err := ks.ExportPEM("new")
	if err != nil {
		return err
	}
	if _, err := os.Stdout.Write(pem); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "site address:", name)
	return nil
}
```

5. In `usage`, add after the `key import` line:

```
  croptop key new          print a new site key (PEM); its address goes to stderr
```

Then append to the `post --key` lines:

```
                           --create "<name>" starts a new site when the key has none
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal cmd && go vet ./... && go test -p 1 ./internal/publish/ ./cmd/croptop/ -count=1 -timeout 15m`
Expected: all pass.

**Mutation check:** change `println` back to `fmt.Println(s)`. `TestAgentCommands` must fail, with "stdout is not one JSON object".

- [ ] **Step 5: Commit**

```bash
git add internal/publish/post.go internal/publish/post_test.go cmd/croptop/main.go cmd/croptop/main_test.go
git commit -m "croptop key new; post --create starts an agent's own site

key new prints a fresh key; --create makes a site's first version, with
the post, when the host holds none and the network knows no site for the
key. The first command-line test runs an agent's commands end to end and
checks stdout holds only their results.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 7: The owner's console catches up from the network when the host is down

Task 8 lets an agent post over IPFS while its host cannot be reached. The agent waits for another peer to hold the version, and that peer is normally the owner's console. Today the console would take the version in only at its next 10-minute renewal, and would provide it only every 12 hours. So:
- **The minute's catch-up asks the network** when the host cannot be reached. It takes in a version published elsewhere, reading it over IPFS alone.
- **A take-in provides the version at once.**

Steps 1 and 2 are refactors that change no behavior. Each has its own commit:
- `publishedElsewhere` becomes Keepalive's take-in rule, so `CatchUp` can share it;
- `pull` and `takeIn` take the host to read from;
- the hung-host test server becomes a helper.

**Files:**
- Modify: `internal/publish/publish.go`:
  - `Keepalive` (about line 594);
  - `CatchUp` (about line 640);
  - `takeIn` (about line 688);
  - the take-in in `Publish` (about line 115);
  - add `publishedElsewhere`, `catchUpFromNetwork`, `catchUpNetworkTimeout`.
- Modify: `internal/publish/sync.go` (`Sync`'s `pull` call at about line 34, and `pull`)
- Modify/Test: `internal/publish/resilience_test.go`

**Interfaces:**
- Consumes: `unreachable` (Task 5).
- Produces:
  - `publishedElsewhere(site *store.Site, rec *ipfs.Record) bool`;
  - `takeIn(ctx, site, rec, host string) error`;
  - `pull(ctx, site, rec, host string)` (host "" reads over IPFS only);
  - `var catchUpNetworkTimeout = 10 * time.Second`.
- Produces, as test helpers:
  - `hungHost(t) (*httptest.Server, <-chan struct{})`;
  - `newLaptop(t, node *ipfs.Embedded, hostURL string, pem []byte) *Publisher`.

- [ ] **Step 1: Refactor, behavior unchanged: `publishedElsewhere`, and the host passed to `pull` and `takeIn`**

In `internal/publish/publish.go`, add after `hostAhead`:

```go
// publishedElsewhere says whether rec, a record from the network, is a
// version another machine published that this one has not taken in: none of
// this machine's own, and not below the sequence this machine announced.
func publishedElsewhere(site *store.Site, rec *ipfs.Record) bool {
	c := strings.TrimPrefix(rec.Value, "/ipfs/")
	return c != deref(site.LastPublishedCID) && !ownVersion(site, c) && rec.Sequence >= site.IPNSSequence
}
```

In `Keepalive`'s `switch`, the case `case rec.Sequence >= site.IPNSSequence:` becomes `case publishedElsewhere(site, rec):`. The cases before it already settle the rest. The case body becomes `return p.takeIn(ctx, site, rec, HostOf(site))`.

**`takeIn`:**
- its signature becomes `func (p *Publisher) takeIn(ctx context.Context, site *store.Site, rec *ipfs.Record, host string) error`;
- its `p.pull(ctx, site, rec)` becomes `p.pull(ctx, site, rec, host)`;
- add a sentence to its comment: `host is where to read the version first; "" reads over IPFS only.`

**The other two `takeIn` calls** add `, HostOf(site)`:
- in `Publish`: `p.takeIn(ctx, site, behind, HostOf(site))`;
- at the end of `CatchUp`: `p.takeIn(ctx, site, e.record(), HostOf(site))`.

**In `internal/publish/sync.go`:**
- `pull`'s signature becomes `func (p *Publisher) pull(ctx context.Context, site *store.Site, rec *ipfs.Record, host string) (added, updated int, err error)`;
- delete its line `host := HostOf(site)`;
- `Sync`'s call becomes `p.pull(ctx, site, rec, HostOf(site))`.

In `internal/publish/resilience_test.go`, add:

```go
// hungHost is a host that takes each connection and never answers. reached
// gets a value, without blocking, for each request it was sent.
func hungHost(t *testing.T) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	reached := make(chan struct{}, 16)
	release := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
		<-release
	}))
	t.Cleanup(hung.Close)
	t.Cleanup(func() { close(release) }) // cleanups run last first: this before Close, which waits for the handlers
	return hung, reached
}
```

In `TestPublishingNeedsNoHost`, replace the `reached` and `release` channels, the `hung` server, and its two `defer` lines with:

```go
	hung, reached := hungHost(t)
```

Run: `gofmt -l internal && go vet ./internal/... && go test -p 1 ./internal/publish/ -count=1 -timeout 15m`
Expected: all pass.

```bash
git add internal/publish/publish.go internal/publish/sync.go internal/publish/resilience_test.go
git commit -m "Refactor: the take-in rule in one place; pull takes the host to read from

Behavior unchanged: Keepalive's take-in condition becomes
publishedElsewhere, pull and takeIn are given the host, and the hung host
of the P2P gate becomes a helper.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 2: Write the failing test** (append to `internal/publish/resilience_test.go`)

```go
// newLaptop is a machine with the fixture site, pushing to hostURL. With pem
// it takes that key, as a second machine of the owner's does; without, it
// makes one.
func newLaptop(t *testing.T, node *ipfs.Embedded, hostURL string, pem []byte) *Publisher {
	t.Helper()
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	var err error
	if pem == nil {
		_, err = node.Keystore().Generate(fixtureID)
	} else {
		err = node.Keystore().ImportPEM(fixtureID, pem)
	}
	if err != nil {
		t.Fatal(err)
	}
	name, _ := node.Keystore().Name(fixtureID)
	site, _ := s.Site(fixtureID)
	site.IPNS = name
	SetHost(site, hostURL)
	if err := s.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	return &Publisher{Store: s, Node: node, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: node}, SkipPrewarm: true}
}

// When the host cannot be reached, the minute's catch-up asks the network: a
// version another machine published there is taken in, read over IPFS alone,
// and provided at once. An agent that posted while the host was down waits
// for exactly that before it exits (TestPostingNeedsNoHost).
func TestCatchUpTakesInFromTheNetworkWhenTheHostIsDown(t *testing.T) {
	ctx := context.Background()
	ht, nt, ct := hostTimeout, networkTimeout, catchUpNetworkTimeout
	hostTimeout, networkTimeout, catchUpNetworkTimeout = 300*time.Millisecond, 2*time.Second, 2*time.Second
	t.Cleanup(func() { hostTimeout, networkTimeout, catchUpNetworkTimeout = ht, nt, ct })
	hung, _ := hungHost(t)

	here, other := offlineNode(t), offlineNode(t)
	if err := other.Dial(ctx, here.Addrs()); err != nil {
		t.Fatal(err)
	}
	hp := newLaptop(t, here, hung.URL, nil)
	first, err := hp.Publish(ctx, fixtureID, false)
	if err != nil {
		t.Fatal(err)
	}
	// another machine of the owner's has that version and posts on it
	pem, _ := here.Keystore().ExportPEM(fixtureID)
	op := newLaptop(t, other, hung.URL, pem)
	osite, _ := op.Store.Site(fixtureID)
	osite.LastPublishedCID, osite.IPNSSequence = &first.CID, first.Sequence
	rememberVersion(osite, first.CID)
	if err := op.Store.SaveSite(osite); err != nil {
		t.Fatal(err)
	}
	if _, err := addPost(op.Store, fixtureID, NewPost{Title: "Posted on the other machine"}, store.NewID()); err != nil {
		t.Fatal(err)
	}
	theirs, err := op.Publish(ctx, fixtureID, false)
	if err != nil {
		t.Fatal(err)
	}

	if err := hp.CatchUp(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	site, _ := hp.Store.Site(fixtureID)
	if site.LastPublishedCID == nil || *site.LastPublishedCID != theirs.CID {
		t.Fatalf("not taken in: this machine has %v, the other published %s", site.LastPublishedCID, theirs.CID)
	}
	posts, _ := hp.Store.Posts(fixtureID)
	found := false
	for _, p := range posts {
		found = found || p.Title == "Posted on the other machine"
	}
	if !found {
		t.Fatal("the other machine's post was not taken in")
	}
	info, _ := here.Info(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if ids, _ := other.FindProviders(ctx, theirs.CID); slices.Contains(ids, info.PeerID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("this machine never said it holds the version it took in")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
```

Add `slices` and `github.com/mejango/croptop/internal/ipfs` to the imports if missing.

- [ ] **Step 3: Run it to verify it fails**

Run: `go test -p 1 ./internal/publish/ -run TestCatchUpTakesInFromTheNetworkWhenTheHostIsDown -count=1`
Expected: FAIL to compile (`catchUpNetworkTimeout` is undefined). Once that is declared: "not taken in".

- [ ] **Step 4: Implement**

In `internal/publish/publish.go`, next to the other timeouts:

```go
// catchUpNetworkTimeout bounds the network lookup a catch-up makes while the
// site's host cannot be reached. A variable so tests can shorten it.
var catchUpNetworkTimeout = 10 * time.Second
```

In `CatchUp`'s `switch`, add a case after the first one, the one that queues the upload:

```go
	case unreachable(err):
		// the host is down: an agent then posts over IPFS and waits for another
		// peer to hold its version, so look there now, not at the next renewal
		return p.catchUpFromNetwork(ctx, site)
```

Add after `CatchUp`:

```go
// catchUpFromNetwork takes in a version published elsewhere that the network
// has, reading it over IPFS alone: the site's host cannot be reached.
func (p *Publisher) catchUpFromNetwork(ctx context.Context, site *store.Site) error {
	nctx, cancel := context.WithTimeout(ctx, catchUpNetworkTimeout)
	rec, err := p.Node.NetworkRecord(nctx, site.IPNS)
	cancel()
	if err != nil || !publishedElsewhere(site, rec) {
		return nil
	}
	return p.takeIn(ctx, site, rec, "")
}
```

In `takeIn`, after the `p.log("%s: took in …")` line:

```go
	// an agent that posted over IPFS while its host was down waits for another
	// peer to hold its version before it exits: say at once that this one does
	go func(c string) {
		pctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := p.Node.Provide(pctx, c); err != nil {
			p.log("provide %s: %v", c, err)
		}
	}(strings.TrimPrefix(rec.Value, "/ipfs/"))
```

- [ ] **Step 5: Run the tests**

Run: `gofmt -l internal && go vet ./internal/... && go test -p 1 ./internal/publish/ -count=1 -timeout 15m`
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/publish/publish.go internal/publish/resilience_test.go
git commit -m "With the host down, the console catches up from the network

The minute's catch-up asks the network when the site's host cannot be
reached, and takes in a version published elsewhere over IPFS alone; a
take-in provides the version at once. An agent posting while the host is
down waits for exactly this.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: The host is down: post over IPFS, and the P2P gate for `post --key`

**When it runs:** a post's retries end with the host unreachable (no answer, or a 5xx).

**What happens:**
1. Turn P2P on.
2. Read the version the network has, over IPFS, and build the post on it.
3. Sign one above that version's sequence, and announce it.
4. Wait, at most `--linger` (default 10 minutes), until a provider other than this machine holds the new version. That is normally the owner's console after Task 7.
5. If none does, and the network still has this post's record, point the name back at the version the post was built on. Then fail.

**Files:**
- Modify: `internal/publish/post.go`:
  - `NewPost`, `postEngine` and `Post`;
  - add `toNetwork`, `linger`, `pointBack`, `DefaultLinger`, `lingerPoll`.
- Modify: `cmd/croptop/main.go`:
  - the `--linger` flag;
  - the `post` case;
  - the usage text.
- Test: `internal/publish/resilience_test.go`

**Interfaces:**
- Consumes:
  - `unreachable`, `retrying` (Task 5);
  - `onto`, `goOnline` (Task 3);
  - `hungHost`, `newLaptop` (Task 7).
- Produces:
  - `NewPost.Linger time.Duration`;
  - `const DefaultLinger = 10 * time.Minute`;
  - `var lingerPoll`;
  - `postEngine` gains `AnnounceRecord` and `RelayRecord`.

- [ ] **Step 1: Write the failing tests** (append to `internal/publish/resilience_test.go`)

```go
// Posting with a key never needs the host either. With the host hung, the
// post goes out over IPFS on top of the version the network has; the owner's
// console takes it in at its next catch-up and provides it; and the agent's
// command returns once that other peer holds the version. Every later
// sub-project keeps this green.
func TestPostingNeedsNoHost(t *testing.T) {
	ctx := context.Background()
	ht, nt, ct, waits, poll := hostTimeout, networkTimeout, catchUpNetworkTimeout, postRetryWaits, lingerPoll
	hostTimeout, networkTimeout, catchUpNetworkTimeout, lingerPoll = 300*time.Millisecond, 2*time.Second, 2*time.Second, 100*time.Millisecond
	postRetryWaits = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	t.Cleanup(func() { hostTimeout, networkTimeout, catchUpNetworkTimeout, postRetryWaits, lingerPoll = ht, nt, ct, waits, poll })
	hung, reached := hungHost(t)

	// the owner's laptop published the site; the host never got it
	laptop := &noPeers{Embedded: offlineNode(t)}
	lp := newLaptop(t, laptop.Embedded, hung.URL, nil)
	lp.Node = laptop
	first, err := lp.Publish(ctx, fixtureID, false)
	if err != nil {
		t.Fatal(err)
	}
	pem, _ := laptop.Keystore().ExportPEM(fixtureID)

	// the agent: P2P off until the host fails it
	agentNode := offlineNode(t)
	as := &store.Store{Root: t.TempDir()}
	ap := &Publisher{Store: as, Node: agentNode, Render: &render.Renderer{Store: as, Templates: templates.FS, CIDs: agentNode}}
	ap.Online = func(ctx context.Context) error { return agentNode.Dial(ctx, laptop.Addrs()) }
	type result struct {
		res Posted
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := ap.Post(ctx, hung.URL, pem, NewPost{Title: "Posted while the host was down", Linger: time.Minute})
		done <- result{res, err}
	}()

	// the owner's console catches up each minute; here, every 200 ms
	deadline := time.Now().Add(time.Minute)
	for {
		select {
		case r := <-done:
			if r.err != nil {
				t.Fatalf("the post with the host down: %v", r.err)
			}
			if r.res.OnHost || r.res.Sequence <= first.Sequence {
				t.Fatalf("posted %+v on top of %+v", r.res, first)
			}
			site, _ := lp.Store.Site(fixtureID)
			if site.LastPublishedCID == nil || *site.LastPublishedCID != r.res.CID {
				t.Fatalf("the owner's machine has %v; the agent posted %s", site.LastPublishedCID, r.res.CID)
			}
			select {
			case <-reached:
			default:
				t.Fatal("nobody asked the host, so this test proves nothing")
			}
			return
		default:
		}
		if err := lp.CatchUp(ctx, fixtureID); err != nil {
			t.Logf("catch-up: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent's post never reached the owner's machine")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// When no other peer takes the post in time, the agent's command fails, and
// the name points back at the version the post was built on, so the site
// stays readable for everyone.
func TestAPostNoPeerTookIsPointedBack(t *testing.T) {
	ctx := context.Background()
	ht, nt, waits, poll := hostTimeout, networkTimeout, postRetryWaits, lingerPoll
	hostTimeout, networkTimeout, lingerPoll = 300*time.Millisecond, 2*time.Second, 100*time.Millisecond
	postRetryWaits = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	t.Cleanup(func() { hostTimeout, networkTimeout, postRetryWaits, lingerPoll = ht, nt, waits, poll })
	hung, _ := hungHost(t)
	laptop := &noPeers{Embedded: offlineNode(t)}
	lp := newLaptop(t, laptop.Embedded, hung.URL, nil)
	lp.Node = laptop
	first, err := lp.Publish(ctx, fixtureID, false)
	if err != nil {
		t.Fatal(err)
	}
	pem, _ := laptop.Keystore().ExportPEM(fixtureID)
	agentNode := offlineNode(t)
	as := &store.Store{Root: t.TempDir()}
	ap := &Publisher{Store: as, Node: agentNode, Render: &render.Renderer{Store: as, Templates: templates.FS, CIDs: agentNode}}
	ap.Online = func(ctx context.Context) error { return agentNode.Dial(ctx, laptop.Addrs()) }

	_, err = ap.Post(ctx, hung.URL, pem, NewPost{Title: "Nobody took this", Linger: 500 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "no peer took the post") || !strings.Contains(err.Error(), "points at its previous version") {
		t.Fatalf("%v", err)
	}
	site, _ := lp.Store.Site(fixtureID)
	rec, err := laptop.NetworkRecord(ctx, site.IPNS)
	if err != nil || rec.Value != "/ipfs/"+first.CID || rec.Sequence != first.Sequence+2 {
		t.Fatalf("the network has %+v, %v; want %s at sequence %d", rec, err, first.CID, first.Sequence+2)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -p 1 ./internal/publish/ -run 'TestPostingNeedsNoHost|TestAPostNoPeerTookIsPointedBack' -count=1`
Expected: FAIL to compile (`lingerPoll` and `NewPost.Linger` are undefined).

- [ ] **Step 3: Implement**

In `NewPost`, add after `Create`:

```go
	// Linger bounds how long a post made while the host cannot be reached
	// waits for another peer to hold it; zero is DefaultLinger.
	Linger time.Duration
```

In `postEngine`, add:

```go
	AnnounceRecord(ctx context.Context, key, c string, rec []byte) error
	RelayRecord(ctx context.Context, ipnsName string, rec []byte) error
```

In `Post`, after the `errNeedsNetwork` block and before `return res, err`:

```go
	if unreachable(err) && ctx.Err() == nil {
		p.log("%s cannot be reached (%v); posting over IPFS", hostURL, err)
		if err = p.goOnline(ctx); err == nil {
			res, err = c.toNetwork(ctx)
		}
	}
```

Add to `Post`'s comment:

```go
// When the host cannot be reached even after retries, the post goes out over
// IPFS instead (toNetwork), and the result says it is not on the host.
```

Add after `retrying`:

```go
// DefaultLinger is how long, at most, a post made while the host cannot be
// reached waits for another peer to hold it (--linger).
const DefaultLinger = 10 * time.Minute

// lingerPoll is how often that wait looks for another peer. A variable so
// tests can shorten it.
var lingerPoll = 15 * time.Second

// toNetwork posts while the host cannot be reached: on top of the version the
// network has, announced over IPFS with the key. The owner's console takes it
// in from there, and its next upload brings the host up to date. This machine
// is about to exit with the only copy of the new blocks, so it waits, at most
// c.np.Linger, for another peer to hold the version; if none does, the name is
// pointed back at the version the post was built on, so the site stays
// readable, and the post fails.
func (c *postCall) toNetwork(ctx context.Context) (Posted, error) {
	nctx, cancel := context.WithTimeout(ctx, networkTimeout)
	rec, err := c.p.Node.NetworkRecord(nctx, c.ipns)
	cancel()
	if err != nil {
		return Posted{}, fmt.Errorf("%s cannot be reached, and the network has no version of this site: %w", c.host, err)
	}
	base := strings.TrimPrefix(rec.Value, "/ipfs/")
	tmp, err := os.MkdirTemp(c.p.Store.Root, "post-*")
	if err != nil {
		return Posted{}, err
	}
	defer os.RemoveAll(tmp)
	b, err := c.onto(ctx, "", base, "", tmp)
	if err != nil {
		return Posted{}, err
	}
	if b.existing {
		return c.posted(b, base, rec.Sequence, false), nil
	}
	seq := rec.Sequence + 1
	signed, err := c.eng.SignRecord(c.key, b.cid, seq)
	if err != nil {
		return Posted{}, err
	}
	if err := c.eng.AnnounceRecord(ctx, c.key, b.cid, signed); err != nil {
		return Posted{}, fmt.Errorf("announcing the post: %w", err)
	}
	if err := c.linger(ctx, b.cid); err != nil {
		if perr := c.pointBack(ctx, base, b.cid, seq); perr != nil {
			return Posted{}, fmt.Errorf("%w; pointing the site back at its previous version failed (%v), so its record names a version only this machine had until the owner publishes", err, perr)
		}
		return Posted{}, fmt.Errorf("%w; the site points at its previous version, so post again later", err)
	}
	return c.posted(b, b.cid, seq, false), nil
}

// linger waits until a peer other than this machine provides version cid, at
// most c.np.Linger.
func (c *postCall) linger(ctx context.Context, cid string) error {
	d := c.np.Linger
	if d <= 0 {
		d = DefaultLinger
	}
	self, err := c.p.Node.Info(ctx)
	if err != nil {
		return err
	}
	c.p.log("waiting, at most %s, for another peer to hold %s", d, cid)
	deadline := time.Now().Add(d)
	for {
		fctx, cancel := context.WithTimeout(ctx, lingerPoll)
		ids, _ := c.p.Node.FindProviders(fctx, cid)
		cancel()
		for _, id := range ids {
			if id != self.PeerID {
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("no peer took the post within %s, and %s cannot be reached", d, c.host)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lingerPoll):
		}
	}
}

// pointBack points the name back at base, the version the post was built on,
// if the network's newest record is still this post's (cid at seq): this
// machine is about to exit with the only copy of cid's new blocks.
func (c *postCall) pointBack(ctx context.Context, base, cid string, seq uint64) error {
	nctx, cancel := context.WithTimeout(ctx, networkTimeout)
	rec, err := c.p.Node.NetworkRecord(nctx, c.ipns)
	cancel()
	if err != nil {
		return err
	}
	if rec.Value != "/ipfs/"+cid || rec.Sequence != seq {
		return nil // published past since: no record names this version alone
	}
	back, err := c.eng.SignRecord(c.key, base, seq+1)
	if err != nil {
		return err
	}
	return c.eng.RelayRecord(ctx, c.ipns, back)
}
```

In `cmd/croptop/main.go`:
1. Add the flag next to `create`:

```go
	linger := fs.Duration("linger", publish.DefaultLinger, "with the host unreachable, how long to wait for another peer to hold the post (post --key)")
```

2. In the last `post` case, add `Linger: *linger` to the `publish.NewPost` literal, and after the `err` check:

```go
		if !res.OnHost {
			fmt.Fprintf(os.Stderr, "%s could not be reached: the post is on the IPFS network, held by another peer, but not on the host yet; the site owner's app takes it in and brings the host up to date\n", *postHost)
		}
```

3. In `usage`, append to the `post --key` lines:

```
                           with the host unreachable it posts over IPFS and waits, at most
                           --linger (10m), for another peer to hold the post
```

- [ ] **Step 4: Run the tests**

Run: `gofmt -l internal cmd && go vet ./... && go test -p 1 ./internal/publish/ ./cmd/croptop/ -count=1 -timeout 15m`
Expected: all pass. `TestPostRetriesWhatMayPassLater`'s "the host keeps failing" now also tries the network, finds no version, and still fails after four attempts.

**Mutation check:** comment out the provide in `takeIn` (Task 7). `TestPostingNeedsNoHost` must fail with the agent's "no peer took the post".

- [ ] **Step 5: Commit**

```bash
git add internal/publish/post.go internal/publish/resilience_test.go cmd/croptop/main.go
git commit -m "Gate: post --key needs no host either

With the host unreachable after retries, the post goes out over IPFS on
the network's version and waits, at most --linger, for another peer to
hold it; if none does, the name is pointed back at the version it was
built on and the command fails.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: `croptop mcp`

A stdio MCP server built on the official Go SDK, with two tools:
- **`post`:** the same as `post --key`.
- **`site`:** the site's address, its version, and its newest posts.

The server runs like `post --key`: a throwaway data directory, and an engine without P2P until it needs it. Files come as `{path}` or `{name, base64}`.

Only images, video and audio are attached. A path's target counts, after symlinks. The server may run outside the sandbox the agent is in, so a path could reach files the agent itself cannot read.

**Files:**
- Modify: `go.mod`, `go.sum` (`go get github.com/modelcontextprotocol/go-sdk@v1.8.0`)
- Modify: `internal/publish/post.go`:
  - add `SiteView`, `PostView`;
  - add `View` and `view`;
  - add `KeyName`.
- Create: `internal/agent/mcp.go`, `internal/agent/mcp_test.go`
- Modify: `cmd/croptop/main.go`:
  - the `mcp` command;
  - the usage text.
- Test: `cmd/croptop/main_test.go`

**Interfaces:**
- Consumes:
  - `Post`, `Posted`, `goOnline`, `siteOf`, `links`, `readFile` (Tasks 3–8);
  - `testHost`, `stdout` (Task 6).
- Produces:
  - `func (p *Publisher) View(ctx, hostURL, ipns string, n int) (SiteView, error)`;
  - `func (p *Publisher) KeyName(key []byte) (string, error)`;
  - `func agent.NewServer(p *publish.Publisher, hostURL string, key []byte, version string) (*mcp.Server, error)`;
  - `func agent.KeyFromEnv() ([]byte, error)`.

The SDK calls below were checked against v1.8.0 in a scratch module:
- `mcp.NewServer(&mcp.Implementation{Name, Version}, nil)`;
- `mcp.AddTool(s, &mcp.Tool{Name, Description}, func(ctx, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error))`;
- a handler error becomes a result with `IsError` and the message in `Content`;
- the input schema comes from `json` and `jsonschema` tags, with `additionalProperties: false`;
- `s.Run(ctx, &mcp.StdioTransport{})` returns nil when the client closes stdin;
- tests use `mcp.NewInMemoryTransports()` and `mcp.NewClient(...).Connect`;
- the SDK's default logger discards.

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/modelcontextprotocol/go-sdk@v1.8.0`
Expected: `go.mod` requires it, along with `github.com/google/jsonschema-go`, `github.com/segmentio/encoding`, `github.com/yosida95/uritemplate/v3` and `golang.org/x/oauth2`.

- [ ] **Step 2: Write the failing tests**

Create `internal/agent/mcp_test.go`:

```go
package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

// rig is a host on an engine without P2P, and a publisher like croptop mcp's.
func rig(t *testing.T) (*publish.Publisher, string) {
	t.Helper()
	start := func() *ipfs.Embedded {
		e := ipfs.NewEmbedded(t.TempDir())
		e.Offline = true
		if err := e.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { e.Stop() })
		return e
	}
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: start()}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	node := start()
	st := &store.Store{Root: t.TempDir()}
	return &publish.Publisher{Store: st, Node: node, Render: &render.Renderer{Store: st, Templates: templates.FS, CIDs: node}}, srv.URL
}

// The tools against a host: post starts a site and posts to it, a file given
// as base64 included; the same id posts once; site lists the posts; a path to
// something other than media, or a symlink to it, is refused; and no answer
// carries the key.
func TestTools(t *testing.T) {
	ctx := context.Background()
	p, hostURL := rig(t)
	keys := ipfs.NewEmbedded(t.TempDir())
	name, _ := keys.Keystore().Generate("bot")
	pem, _ := keys.Keystore().ExportPEM("bot")
	s, err := NewServer(p, hostURL, pem, "test")
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(tool string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "PRIVATE KEY") {
				t.Fatalf("%s answered with the key", tool)
			}
		}
		var out map[string]any
		b, _ := json.Marshal(res.StructuredContent)
		json.Unmarshal(b, &out)
		return res, out
	}
	png := base64.StdEncoding.EncodeToString([]byte("not really a png"))
	res, first := call("post", map[string]any{"title": "Hello", "create": "Bot", "id": "t1", "files": []map[string]any{{"name": "shot.png", "base64": png}}})
	if res.IsError || first["existing"] != false || first["site"] != name {
		t.Fatalf("post: %+v, %v", res, first)
	}
	resp, err := http.Get(hostURL + "/ipfs/" + first["cid"].(string) + "/" + first["postId"].(string) + "/shot.png")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "not really a png" {
		t.Fatalf("the attachment: %q", b)
	}
	if _, again := call("post", map[string]any{"title": "Hello", "id": "t1"}); again["existing"] != true || again["postId"] != first["postId"] {
		t.Fatalf("the same id again: %v", again)
	}
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret.txt")
	os.WriteFile(secret, []byte("secret"), 0o600)
	link := filepath.Join(dir, "pic.png")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{secret, link} {
		if res, _ := call("post", map[string]any{"title": "x", "files": []map[string]any{{"path": path}}}); !res.IsError {
			t.Fatalf("%s was attached", path)
		}
	}
	_, view := call("site", map[string]any{})
	posts, _ := view["posts"].([]any)
	if view["name"] != "Bot" || view["cid"] != first["cid"] || len(posts) != 1 || posts[0].(map[string]any)["title"] != "Hello" {
		t.Fatalf("site: %v", view)
	}
}

// The key comes from CROPTOP_KEY, the PEM itself, or else CROPTOP_KEY_FILE.
func TestKeyFromEnv(t *testing.T) {
	t.Setenv("CROPTOP_KEY", "")
	t.Setenv("CROPTOP_KEY_FILE", "")
	if _, err := KeyFromEnv(); err == nil || !strings.Contains(err.Error(), "CROPTOP_KEY_FILE") {
		t.Fatalf("no key: %v", err)
	}
	f := filepath.Join(t.TempDir(), "k.pem")
	os.WriteFile(f, []byte("from the file"), 0o600)
	t.Setenv("CROPTOP_KEY_FILE", f)
	if b, err := KeyFromEnv(); err != nil || string(b) != "from the file" {
		t.Fatalf("%q, %v", b, err)
	}
	t.Setenv("CROPTOP_KEY", "inline")
	if b, err := KeyFromEnv(); err != nil || string(b) != "inline" {
		t.Fatalf("%q, %v", b, err)
	}
}
```

Append to `cmd/croptop/main_test.go`, adding `bufio`, `fmt`, `sort`, `strings` and `time` to its imports:

```go
// croptop mcp prints nothing on stdout but the protocol: an MCP client reads
// each line there as a message.
func TestMCPPrintsOnlyProtocol(t *testing.T) {
	url := testHost(t)
	key := filepath.Join(t.TempDir(), "site.pem")
	if err := os.WriteFile(key, stdout(t, func() error { return run([]string{"key", "new"}) }), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CROPTOP_KEY_FILE", key)
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	realIn, realOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	t.Cleanup(func() { os.Stdin, os.Stdout = realIn, realOut })
	done := make(chan error, 1)
	go func() { done <- run([]string{"mcp", "--host", url}) }()
	for _, m := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	} {
		fmt.Fprintln(inW, m)
	}
	lines := bufio.NewScanner(outR)
	lines.Buffer(make([]byte, 1<<20), 1<<20)
	var tools []string
	for id := 1; id <= 2; id++ {
		if !lines.Scan() {
			t.Fatalf("stdout ended: %v", lines.Err())
		}
		var msg struct {
			ID     int `json:"id"`
			Result struct {
				Tools []struct{ Name string } `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal(lines.Bytes(), &msg); err != nil || msg.ID != id {
			t.Fatalf("stdout line %d is not the answer to request %d: %q", id, id, lines.Bytes())
		}
		for _, tl := range msg.Result.Tools {
			tools = append(tools, tl.Name)
		}
	}
	sort.Strings(tools)
	if strings.Join(tools, ",") != "post,site" {
		t.Fatalf("tools: %v", tools)
	}
	inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("croptop mcp: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("croptop mcp did not exit when its client left")
	}
	outW.Close()
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test -p 1 ./internal/agent/ ./cmd/croptop/ -count=1`
Expected: FAIL to compile (`NewServer` and `KeyFromEnv` are undefined).

- [ ] **Step 4: Implement `View` and `KeyName`**

In `internal/publish/post.go`, add at the end:

```go
// SiteView is what an agent sees of a site: croptop mcp's site tool.
type SiteView struct {
	Site     string     `json:"site"` // the site's IPNS name
	Name     string     `json:"name"`
	URL      string     `json:"url"`
	CID      string     `json:"cid"` // the version its host holds
	Sequence uint64     `json:"sequence"`
	Posts    []PostView `json:"posts"` // newest first
}

// PostView is one post in a SiteView.
type PostView struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Created string `json:"created"` // RFC 3339, UTC
}

// View is what an agent sees of the site at ipns: where it is, the version
// its host holds, and its n newest posts, read from the host and checked
// against that version's CIDs. A version the host cannot serve while P2P is
// off turns P2P on, as Post does.
func (p *Publisher) View(ctx context.Context, hostURL, ipns string, n int) (SiteView, error) {
	v, err := p.view(ctx, hostURL, ipns, n)
	if errors.Is(err, errNeedsNetwork) {
		p.log("%v", err)
		if err = p.goOnline(ctx); err == nil {
			v, err = p.view(ctx, hostURL, ipns, n)
		}
	}
	return v, err
}

func (p *Publisher) view(ctx context.Context, hostURL, ipns string, n int) (SiteView, error) {
	eng, ok := p.Node.(postEngine)
	if !ok {
		return SiteView{}, errors.New("reading a site needs the embedded engine (croptop --engine embedded)")
	}
	hctx, cancel := context.WithTimeout(ctx, hostTimeout)
	e, err := hostEntry(hctx, hostURL, ipns)
	cancel()
	if err != nil {
		return SiteView{}, err
	}
	links, err := p.links(ctx, eng, hostURL, e.CID)
	if err != nil {
		return SiteView{}, fmt.Errorf("read %s: %w", e.CID, err)
	}
	want, ok := links["planet.json"]
	if !ok {
		return SiteView{}, fmt.Errorf("%s is not a Croptop site (no planet.json)", e.CID)
	}
	tmp, err := os.MkdirTemp(p.Store.Root, "view-*")
	if err != nil {
		return SiteView{}, err
	}
	defer os.RemoveAll(tmp)
	pub := filepath.Join(tmp, "published")
	if err := p.readFile(ctx, eng, hostURL, e.CID, "planet.json", want, filepath.Join(pub, "planet.json")); err != nil {
		return SiteView{}, fmt.Errorf("planet.json: %w", err)
	}
	st := &store.Store{Root: filepath.Join(tmp, "store")}
	site, err := siteOf(st, pub, e.CID, ipns, e.Name, hostURL)
	if err != nil {
		return SiteView{}, err
	}
	posts, err := st.Posts(site.ID)
	if err != nil {
		return SiteView{}, err
	}
	sort.SliceStable(posts, func(i, j int) bool { return posts[i].Created > posts[j].Created })
	v := SiteView{Site: ipns, Name: site.Name, URL: render.SiteURL(site), CID: e.CID, Sequence: e.Sequence, Posts: []PostView{}}
	for _, post := range posts {
		if len(v.Posts) == n {
			break
		}
		if post.ArticleType == 1 { // a page, not a post
			continue
		}
		v.Posts = append(v.Posts, PostView{ID: post.ID, Title: post.Title, URL: render.BrowserURL(site, post), Created: post.Created.Time().UTC().Format(time.RFC3339)})
	}
	return v, nil
}

// KeyName is the IPNS name of a site key (PEM, or a Planet export's raw
// bytes): the site's address.
func (p *Publisher) KeyName(key []byte) (string, error) {
	ks := p.Node.Keystore()
	name := "name-" + store.NewID()
	if err := importKey(ks, name, key); err != nil {
		return "", fmt.Errorf("key: %w", err)
	}
	defer ks.Delete(name)
	return ks.Name(name)
}
```

- [ ] **Step 5: Implement the server**

Create `internal/agent/mcp.go`:

```go
// Package agent is croptop's MCP server: the tools an AI agent uses to post
// to one site and to read it, holding the site's key, which no tool returns.
package agent

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mejango/croptop/internal/publish"
)

// PostInput is what the post tool takes.
type PostInput struct {
	Title   string   `json:"title,omitempty" jsonschema:"the post's title"`
	Content string   `json:"content,omitempty" jsonschema:"the post's text, in markdown"`
	Tags    []string `json:"tags,omitempty" jsonschema:"tags; each gets a page listing its posts"`
	Files   []File   `json:"files,omitempty" jsonschema:"images, video or audio to attach: a path on this machine, or a name with the bytes in base64"`
	ID      string   `json:"id,omitempty" jsonschema:"a stable key for this post, such as a task ID: posting again with the same id returns the first post instead of adding another"`
	Create  string   `json:"create,omitempty" jsonschema:"if the key has no site yet, start one with this name"`
}

// File is one attachment: Path, or Name with Base64.
type File struct {
	Path   string `json:"path,omitempty" jsonschema:"a file on this machine"`
	Name   string `json:"name,omitempty" jsonschema:"the file's name, given with base64"`
	Base64 string `json:"base64,omitempty" jsonschema:"the file's bytes in base64, given with name"`
}

// SiteInput is what the site tool takes.
type SiteInput struct {
	Posts int `json:"posts,omitempty" jsonschema:"how many of the newest posts to list (default 10)"`
}

// media is what the post tool attaches: images, video and audio. This server
// may read files the agent itself is not allowed to (it runs outside the
// agent's sandbox), so a path to anything else is refused, through a symlink
// too.
var media = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true, ".heic": true,
	".mp4": true, ".mov": true, ".m4v": true, ".webm": true,
	".mp3": true, ".m4a": true, ".aac": true, ".wav": true, ".ogg": true, ".oga": true, ".opus": true, ".flac": true,
}

// NewServer is croptop's MCP server for the site whose key is key, posting
// through hostURL.
func NewServer(p *publish.Publisher, hostURL string, key []byte, version string) (*mcp.Server, error) {
	site, err := p.KeyName(key)
	if err != nil {
		return nil, err
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "croptop", Version: version}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "post",
		Description: "Post to the croptop site " + site + ". Give a title, markdown content, or files (images, video, audio). Pass id, any stable string such as a task ID, to make retries safe: the same id returns the first post instead of posting again. Returns the post's URL.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PostInput) (*mcp.CallToolResult, publish.Posted, error) {
		dir, err := os.MkdirTemp(p.Store.Root, "files-*")
		if err != nil {
			return nil, publish.Posted{}, err
		}
		defer os.RemoveAll(dir)
		files, err := in.paths(dir)
		if err != nil {
			return nil, publish.Posted{}, err
		}
		res, err := p.Post(ctx, hostURL, key, publish.NewPost{Title: in.Title, Content: in.Content, Tags: strings.Join(in.Tags, ","), Files: files, ID: in.ID, Create: in.Create})
		return nil, res, err
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "site",
		Description: "Read the croptop site " + site + ": its address, the version its host serves, and its newest posts.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SiteInput) (*mcp.CallToolResult, publish.SiteView, error) {
		n := in.Posts
		if n <= 0 {
			n = 10
		}
		v, err := p.View(ctx, hostURL, site, n)
		return nil, v, err
	})
	return s, nil
}

// paths gives each attachment as a path: files given by path as they are,
// files given as base64 written into dir under their names. Media only.
func (in PostInput) paths(dir string) ([]string, error) {
	var out []string
	for _, f := range in.Files {
		switch {
		case f.Path != "" && f.Name == "" && f.Base64 == "":
			real, err := filepath.EvalSymlinks(f.Path)
			if err != nil {
				return nil, err
			}
			if !media[strings.ToLower(filepath.Ext(f.Path))] || !media[strings.ToLower(filepath.Ext(real))] {
				return nil, fmt.Errorf("%s: only images, video and audio can be attached", f.Path)
			}
			out = append(out, f.Path)
		case f.Path == "" && f.Name != "" && f.Base64 != "":
			if filepath.Base(f.Name) != f.Name || !media[strings.ToLower(filepath.Ext(f.Name))] {
				return nil, fmt.Errorf("%s: give the file name (no folders) of an image, video or audio", f.Name)
			}
			b, err := base64.StdEncoding.DecodeString(f.Base64)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f.Name, err)
			}
			path := filepath.Join(dir, f.Name)
			if err := os.WriteFile(path, b, 0o644); err != nil {
				return nil, err
			}
			out = append(out, path)
		default:
			return nil, errors.New("give each file as {path}, or as {name, base64}")
		}
	}
	return out, nil
}

// KeyFromEnv is the site key croptop mcp serves: CROPTOP_KEY holds the PEM
// itself, CROPTOP_KEY_FILE the path to it.
func KeyFromEnv() ([]byte, error) {
	if k := os.Getenv("CROPTOP_KEY"); k != "" {
		return []byte(k), nil
	}
	if f := os.Getenv("CROPTOP_KEY_FILE"); f != "" {
		return os.ReadFile(f)
	}
	return nil, errors.New("croptop mcp needs the site's key: set CROPTOP_KEY to the PEM, or CROPTOP_KEY_FILE to its path")
}
```

- [ ] **Step 6: Wire `croptop mcp`**

In `cmd/croptop/main.go`:
1. Add the imports `"github.com/mejango/croptop/internal/agent"` and `"github.com/modelcontextprotocol/go-sdk/mcp"`.
2. Next to `var postKey []byte`, add `var mcpKey []byte`.
3. In the first `switch`, add:

```go
	case "mcp":
		// like post --key: a throwaway data directory, and an engine without P2P
		var err error
		if mcpKey, err = agent.KeyFromEnv(); err != nil {
			return err
		}
		tmp, err := os.MkdirTemp("", "croptop-mcp-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		a.dataDir, a.offline, logs = tmp, true, os.Stderr
```

4. In the last `switch`, add:

```go
	case "mcp":
		s, err := agent.NewServer(a.pub, strings.TrimSuffix(*postHost, "/"), mcpKey, version)
		if err != nil {
			return err
		}
		if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
			return err
		}
		return nil
```

5. In `usage`, add after the `post --key` lines:

```
  croptop mcp [--host url] an MCP server (stdio) with the tools post and site, for the site
                           whose key is in CROPTOP_KEY_FILE (a path) or CROPTOP_KEY (the PEM)
```

- [ ] **Step 7: Run the tests**

Run: `gofmt -l internal cmd && go vet ./... && go test -p 1 ./internal/agent/ ./internal/publish/ ./cmd/croptop/ -count=1 -timeout 15m`
Expected: all pass.

**Mutation check:** in `run`'s `mcp` case, leave out `logs = os.Stderr` (keep the rest of that line). `TestMCPPrintsOnlyProtocol` must fail with "stdout line 1 is not the answer to request 1": the line "starting ipfs (embedded)" comes first.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/publish/post.go internal/agent/mcp.go internal/agent/mcp_test.go cmd/croptop/main.go cmd/croptop/main_test.go
git commit -m "croptop mcp: post and site tools for any MCP client

A stdio MCP server on the official Go SDK (v1.8.0) for the site whose key
is in CROPTOP_KEY_FILE or CROPTOP_KEY. post is post --key; site reads the
site's address, version and newest posts. Only media is attached, never
through a symlink to something else, and no tool returns the key.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Docs

**Files:**
- Modify: `docs/agents.md`. crop.top serves it at `/agents.md`, so it ships with the Worker deploy in Task 11.
- Modify: `README.md` ("Posting from an agent" and "Commands")
- Modify: `docs/host.md` (the routing endpoint)

- [ ] **Step 1: Rewrite `docs/agents.md`** with exactly this:

````markdown
# Posting to a croptop site from an agent

An agent, bot or CI job can post to a croptop site with nothing but the site's
key. It needs no copy of the site, no gateway, no open ports, and nothing kept
between runs.

## Setup

Use a site you own, or let the agent start its own.

**A site you own.** Publish it once from the Croptop app or `croptop serve`.
This puts it on its host, crop.top unless you set another. Then export its key
and give it to the agent as a secret:

```
croptop key export <site> > site.pem
```

**The agent's own site.** Make a key, and start the site with its first post:

```
croptop key new > site.pem
croptop post --key site.pem --create "Name of the site" --title "Hello"
```

The key controls the site. Keep it in the agent's secret store, never in a
repository.

## Each run

1. Install the latest croptop. Repeating this is safe.

   ```
   curl -fsSL https://crop.top/install.sh | sh     # macOS and Linux
   irm https://crop.top/install.ps1 | iex          # Windows
   ```

2. Post:

   ```
   croptop post --key site.pem --title "Title" --content "Body in **markdown**" [--tags a,b] [--id <task id>] [--json] [files…]
   ```

   - Files are images, videos or audio to attach.
   - `--id` takes any stable string, such as a task ID. Running the command
     again with it returns the first post instead of posting twice.
   - `--json` prints one JSON object: `url`, `cid`, `sequence`, `postId`,
     `site` (the site's address, an IPNS name), `existing` (true when `--id`
     found the post already there) and `onHost`.
   - The run can end right after: the host keeps the post online.

## MCP

`croptop mcp` serves two tools to any MCP client over stdio:
- `post` does what `post --key` does. Files are given as a path, or as a name
  with the bytes in base64.
- `site` reads the site's address, its version, and its newest posts.

The key comes from `CROPTOP_KEY_FILE` (a path) or `CROPTOP_KEY` (the PEM
itself). No tool returns it. With Claude Code:

```
claude mcp add croptop -e CROPTOP_KEY_FILE=/path/to/site.pem -- croptop mcp
```

Other clients take the same command:

```json
{ "mcpServers": { "croptop": { "command": "croptop", "args": ["mcp"], "env": { "CROPTOP_KEY_FILE": "/path/to/site.pem" } } } }
```

## Rules

- Never print, log or echo the key.
- **Retries happen on their own.** A busy or failing host, and a site that
  changed while posting, are tried again three times.
- **"host already has sequence" or "host holds …, not …":** the retries did
  not settle it. Run the command again. With `--id` it never posts twice.
- **"the network has a newer version of this site":** the owner's latest
  publish has not reached the host yet; a big one uploads in the background.
  Wait a few minutes and run it again. If it keeps failing, tell the owner.
- **The host cannot be reached at all:**
  - the command posts over IPFS, and waits at most 10 minutes (`--linger`) for
    the owner's app to take the post;
  - it exits 0 once another peer holds it, with `onHost` false;
  - if no peer took it, it exits non-zero and leaves the site as it was. Run
    it again later.
- **Sites on their own host.** Some owners publish to their own host instead of
  crop.top. For those sites, add `--host https://<their host>`, to
  `croptop mcp` too. The agent and the owner's app must use the same host.
- Use croptop 0.13.20 or newer. The install line always gets the latest.
````

- [ ] **Step 2: README.** In "Posting from an agent", after the paragraph that ends "A console running on your machine takes the agent's posts in within a minute.", add:

````markdown
`--id <task id>` makes a retry return the first post instead of posting
twice, and `--json` prints the result for a script. Busy or failing hosts are
retried on their own. An agent can also start a site of its own:
`croptop key new > site.pem`, then `post --key site.pem --create "<name>" …`.
If the host cannot be reached at all, the post goes out over IPFS, and the
command waits up to 10 minutes for your app to take it in.

MCP clients get the same through `croptop mcp`, with the tools `post` and
`site`. With Claude Code:

```
claude mcp add croptop -e CROPTOP_KEY_FILE=/path/to/site.pem -- croptop mcp
```
````

In "Commands", replace the two `croptop post --key …` lines with:

```
croptop key new                print a new site key; its address goes to stderr
croptop post --key f --title t [--content c] [--tags a,b] [--host url] [--id key] [--json] [--create name] [files]
                               post with only the key; see Posting from an agent
croptop mcp [--host url]       MCP server (stdio) for the site whose key is in CROPTOP_KEY_FILE
```

- [ ] **Step 3: `docs/host.md`.** In the `GET|PUT /routing/v1/ipns/<name>` entry, change "GET answers pushed sites from the registry and others from the node's DHT;" to:

```
GET answers a pushed site with the newer of its pushed record and the node's
DHT copy (the owner's app renews its record every 10 minutes without pushing,
and sends each renewal here), and other sites from the node's DHT;
```

- [ ] **Step 4: Commit**

```bash
git add docs/agents.md README.md docs/host.md
git commit -m "Docs: --id, --json, --create, key new, croptop mcp, posting while the host is down

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Ship D

Run by the main session. Before any deploy, tag or release, confirm with the user.

- [ ] **Step 1: Full test run.**

`go vet ./... && go test -p 1 ./... -count=1 -timeout 20m && (cd worker && node --experimental-loader ./text-loader.mjs --test test/) && (cd apps/macos && swift test)`

Expect every Go package `ok`, Worker `# fail 0`, and Swift `0 failures`.

- [ ] **Step 2: Merge.** Bump `installer/macos-build-number` to 1159 and commit "Agent client and MCP (build 1159)". Fast-forward main and push.

- [ ] **Step 3: Deploy the node,** for the routing answer.
  1. Build a clean `git archive` folder, plus the `templates/croptop` submodule. Use `mktemp -d`, not `rm -rf`.
  2. Run `railway up <dir> --path-as-root --service croptop-host --environment production --ci`.
  3. Verify that `GET /routing/v1/ipns/<jango ipns>` on the Railway URL answers 200, and that its record's sequence is at least the sequence `GET /v0/host/keys/<jango ipns>` shows.

- [ ] **Step 4: Deploy the Worker,** for the routing answer and agents.md.
  1. Run `cd worker && npx -y -p node@22 -p wrangler@4.145.0 wrangler deploy -c wrangler.toml`.
  2. Verify:
     - `https://crop.top/agents.md` shows the "## MCP" section;
     - `https://crop.top/routing/v1/ipns/<jango ipns>` answers a record whose sequence is at least the keys entry's;
     - `/`, `/jango/` and `/directory` answer 200.

- [ ] **Step 5: Release.**
  1. Tag `v0.13.20`, push, and wait for goreleaser.
  2. Upload v0.13.19's `appcast.xml` to the new release (`gh release upload v0.13.20 appcast.xml --clobber`), so update checks never 404 meanwhile.
  3. Run `installer/release-macos.sh 0.13.20` from a `git archive v0.13.20 installer apps` export, with the user's notary key.
  4. Check that the appcast shows `<sparkle:version>1159`.
  5. Release note: agents post in seconds over HTTP (`--id`, `--json`, `--create`, `croptop key new`); `croptop mcp`; posts still go out when the host is down; background uploads no longer retry versions taken in from another machine.

- [ ] **Step 6: Live checks,** with the released `croptop`. They use a throwaway key, never the owner's sites.
  1. `croptop key new > /tmp/d-smoke.pem`. Then `time croptop post --key /tmp/d-smoke.pem --create "D smoke" --title "Hello from D" --id smoke-1 --json`. Expect one JSON object on stdout with `"existing":false`, in about 3 s.
  2. The same command again, without `--create`. Expect `"existing":true` and the same `postId` and `cid`.
  3. `printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"site","arguments":{}}}' | CROPTOP_KEY_FILE=/tmp/d-smoke.pem croptop mcp`. Expect exactly two JSON lines on stdout; the second lists the post "Hello from D".
  4. The host-down path, on the real network: `croptop post --key /tmp/d-smoke.pem --host http://127.0.0.1:9 --title "while the host was down" --linger 2m`. No machine holds the smoke site, so expect a non-zero exit after about 2 minutes, with "no peer took the post" and "the site points at its previous version".
  5. Note the smoke site's IPNS name in the ledger, as junk to clean up with the other throwaway sites. Kill any test processes started.
- [ ] **Step 7: Owner's follow-ups to report.**
  - The four sites whose versions were taken in (Banny Futebol Club, CocoPay, Sana Floresta, ETH.SHOP) stop retrying after this release. Each reaches crop.top when the machine that published it uploads it, or when it is next published from this one.
  - kmac can update.
