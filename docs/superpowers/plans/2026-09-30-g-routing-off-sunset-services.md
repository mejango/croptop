# G: Routing and bootstrap off sunset services — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** crop.top and croptop keep resolving, publishing and joining the IPFS network after delegated-ipfs.dev and the IPFS bootstrap nodes stop (2026-09-30).

**Architecture:** crop.top's node serves the IPNS part of the Delegated Routing V1 API and a peers list; the Worker proxies both and uses the node instead of delegated-ipfs.dev. Clients send records to crop.top's routing endpoint in the background, peer with crop.top's node, bootstrap through it, and remember the peers they were connected to.

**Tech Stack:** Go 1.27 (boxo v0.42.2, go-libp2p, go-libp2p-kad-dht v0.42.1), Cloudflare Worker (JS, R2, KV), wrangler 4.145.0, Railway.

**Spec:** `docs/superpowers/specs/2026-09-30-scaling-and-agent-economy-design.md` (section G). This is the first of seven plans; each later plan is written after the previous sub-project ships.

## Global Constraints
- delegated-ipfs.dev is best effort only: no publish, lookup or request may wait on it.
- A host never sends records to routing endpoints (it is the endpoint; the Worker proxies back to it).
- Every client start must work with crop.top unreachable: no step of `Start` may block on crop.top.
- crop.top's node: peer ID `12D3KooWDSjjQ4GuTGwEbxw6QnfRu3GLa45GzN5s7vAgKTo6wLqY`, address `/dns4/altaria.proxy.rlwy.net/tcp/35880`.
- Go tests run serially: `go test -p 1 ./...` (packages share a kubo repo lock).
- Worker tests: `cd worker && node --experimental-loader ./text-loader.mjs --test test/`.
- wrangler: `npx -y -p node@22 -p wrangler@4.145.0 wrangler …`.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Each release bumps `installer/macos-build-number`; nothing is committed in `app/` while `installer/release-macos.sh` runs.

## Review Focus
1. crop.top is down when a client starts: the node must still start at once and join through the default and remembered peers. (Task 3: `hostPeers` returns within its 5 s timeout and runs in a goroutine.)
2. A routing PUT loop between the node and the Worker: the host must never forward records. (Task 4: `Host.Start` clears the engine's routing and peers settings; tested.)
3. A flood of routing PUTs to the node: excess requests get `429` instead of spawning unbounded DHT puts. (Task 4: zero free slots gives `429`.)
4. A damaged or foreign `peers.json`: read as no peers, never a failed start. (Task 3.)
5. crop.top's node is down while the Worker serves a routing request: `502` within the timeout, not a hung request. (Task 5.)

---

### Task 1: Raw IPNS records from the engine

**Files:**
- Modify: `internal/ipfs/embedded_ipns.go` (`NetworkRecord` around lines 97-155)
- Test: `internal/ipfs/embedded_test.go`

**Interfaces:**
- Produces: `func (e *Embedded) GetRecord(ctx context.Context, name string) ([]byte, error)` — the newest valid signed record for `name` as raw bytes, `ErrNoRecord` or another error when none. `NetworkRecord` keeps its signature and is built on it.

- [ ] **Step 1: Write the failing test** (append to `internal/ipfs/embedded_test.go`; add `"bytes"` to its imports)

```go
// GetRecord hands back the exact signed bytes, which the routing endpoint serves.
func TestGetRecordReturnsTheStoredRecord(t *testing.T) {
	ctx := context.Background()
	e := NewEmbedded(t.TempDir())
	e.Offline = true
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	name, _ := e.Keystore().Generate("s")
	if _, err := e.GetRecord(ctx, name); err == nil {
		t.Fatal("found a record before any put")
	}
	rec, err := e.SignRecord("s", "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4", 3)
	if err != nil {
		t.Fatal(err)
	}
	e.PutRecord(ctx, name, rec) // stored locally first; offline there is no peer to send it to
	got, err := e.GetRecord(ctx, name)
	if err != nil || !bytes.Equal(got, rec) {
		t.Fatalf("GetRecord: %v", err)
	}
	r, err := e.NetworkRecord(ctx, name)
	if err != nil || r.Sequence != 3 || r.Value != "/ipfs/bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4" {
		t.Fatalf("NetworkRecord: %+v %v", r, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/ipfs/ -run TestGetRecordReturnsTheStoredRecord -count=1`
Expected: FAIL to compile, `e.GetRecord undefined`.

- [ ] **Step 3: Replace `NetworkRecord` with `GetRecord` plus a thin `NetworkRecord`**

Replace the whole `NetworkRecord` function in `internal/ipfs/embedded_ipns.go` with:

```go
// GetRecord returns the newest valid signed IPNS record for name the DHT has,
// this node's own copy included, as raw bytes.
func (e *Embedded) GetRecord(ctx context.Context, nameStr string) ([]byte, error) {
	if e.dht == nil {
		return nil, fmt.Errorf("node not started")
	}
	name, err := ipns.NameFromString(strings.TrimPrefix(nameStr, "/ipns/"))
	if err != nil {
		return nil, err
	}
	if !e.Offline {
		e.waitForRoutingTable(ctx, 4, 20*time.Second)
	}
	sctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	ch, err := e.dht.SearchValue(sctx, string(name.RoutingKey()))
	if err != nil {
		if errors.Is(err, routing.ErrNotFound) {
			return nil, ErrNoRecord
		}
		return nil, err
	}
	var best []byte
	var bestSeq uint64
	for b := range ch {
		rec, err := ipns.UnmarshalRecord(b)
		if err != nil || ipns.ValidateWithName(rec, name) != nil {
			continue
		}
		seq, err := rec.Sequence()
		if err != nil {
			continue
		}
		if best == nil || seq > bestSeq {
			best, bestSeq = b, seq
		}
	}
	if best == nil {
		return nil, ErrNoRecord
	}
	return best, nil
}

// NetworkRecord is the newest record for name, parsed.
func (e *Embedded) NetworkRecord(ctx context.Context, nameStr string) (*Record, error) {
	b, err := e.GetRecord(ctx, nameStr)
	if err != nil {
		return nil, err
	}
	rec, err := ipns.UnmarshalRecord(b)
	if err != nil {
		return nil, err
	}
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

- [ ] **Step 4: Run the engine tests**

Run: `go test ./internal/ipfs/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ipfs/embedded_ipns.go internal/ipfs/embedded_test.go
git commit -m "Engine hands back raw IPNS records

GetRecord returns the newest valid signed record as bytes; NetworkRecord
parses it. The routing endpoint serves these bytes.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Records go to routing endpoints in the background

**Files:**
- Modify: `internal/ipfs/embedded_ipns.go` (`putRecord`, `putDelegated` around lines 198-300; constants at 22-29)
- Modify: `internal/ipfs/embedded.go` (the `Embedded` struct, lines 44-77)
- Modify: `cmd/croptop/main.go` (`open`, around line 430)
- Test: `internal/ipfs/embedded_test.go`

**Interfaces:**
- Produces: field `Embedded.RoutingPuts []string` (base URLs ending in `/routing/v1/ipns/`; empty by default), `var ipfs.DefaultRoutingPuts = []string{"https://crop.top/routing/v1/ipns/", "https://delegated-ipfs.dev/routing/v1/ipns/"}`. `putDelegated` is removed.

- [ ] **Step 1: Write the failing test** (append; add `"io"`, `"net/http"`, `"net/http/httptest"` to imports if missing)

```go
// A routing endpoint gets each record in the background; one that is gone
// never holds up a publish.
func TestPutRecordSendsToRoutingEndpointsInTheBackground(t *testing.T) {
	got := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Method == http.MethodPut && r.Header.Get("Content-Type") == "application/vnd.ipfs.ipns-record" && strings.HasPrefix(r.URL.Path, "/routing/v1/ipns/k") {
			got <- b
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	e := NewEmbedded(t.TempDir())
	e.Offline = true
	e.RoutingPuts = []string{"http://127.0.0.1:1/routing/v1/ipns/", srv.URL + "/routing/v1/ipns/"} // the first is gone
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	name, _ := e.Keystore().Generate("s")
	rec, _ := e.SignRecord("s", "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4", 1)
	start := time.Now()
	e.PutRecord(ctx, name, rec)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("a routing endpoint held up the put for %s", d)
	}
	select {
	case b := <-got:
		if !bytes.Equal(b, rec) {
			t.Fatal("the endpoint got other bytes")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the endpoint never got the record")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/ipfs/ -run TestPutRecordSendsToRoutingEndpointsInTheBackground -count=1`
Expected: FAIL to compile, `e.RoutingPuts undefined`.

- [ ] **Step 3: Add the field, the default, and the background puts**

In `internal/ipfs/embedded.go`, inside `type Embedded struct` after the `Announce []string` field:

```go
	// RoutingPuts are Delegated Routing endpoints (…/routing/v1/ipns/) each
	// published record is also sent to, in the background. Empty for a host:
	// it is the endpoint.
	RoutingPuts []string
```

In `internal/ipfs/embedded_ipns.go`, below the `const (…)` block:

```go
// DefaultRoutingPuts: crop.top's node, and delegated-ipfs.dev while it lasts.
var DefaultRoutingPuts = []string{"https://crop.top/routing/v1/ipns/", delegatedIPNS}
```

Replace `putDelegated` with:

```go
// putRouting sends a signed record to one Delegated Routing endpoint. Best effort.
func (e *Embedded) putRouting(base string, name ipns.Name, rec []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+name.String(), bytes.NewReader(rec))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/vnd.ipfs.ipns-record")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.Log("ipns routing put " + base + ": " + err.Error())
		return
	}
	resp.Body.Close()
	e.Log(fmt.Sprintf("ipns routing put %s: %s", base, resp.Status))
}
```

Replace `putRecord` with (the extra `GetClosestPeers` only fed a log line, and `PutValue` repeats that lookup):

```go
// putRecord puts a marshalled record in the DHT and pubsub, and sends it to
// the routing endpoints in the background.
func (e *Embedded) putRecord(ctx context.Context, name ipns.Name, b []byte) error {
	for _, base := range e.RoutingPuts {
		go e.putRouting(base, name, b) // an endpoint that is slow or gone never holds up a publish
	}
	if !e.Offline {
		e.waitForRoutingTable(ctx, 20, 60*time.Second)
	}
	start := time.Now()
	if err := e.dht.PutValue(ctx, string(name.RoutingKey()), b); err != nil {
		return fmt.Errorf("ipns put: %w", err)
	}
	e.Log(fmt.Sprintf("ipns put done in %s", time.Since(start).Round(time.Millisecond)))
	if !e.Offline {
		go e.putPubsub(context.Background(), name, b)
	}
	return nil
}
```

In `cmd/croptop/main.go`, in `open`, inside `if a.cfg.EngineName() == "embedded" {` after `e.Log = logf`:

```go
		e.RoutingPuts = ipfs.DefaultRoutingPuts
```

- [ ] **Step 4: Run the tests**

Run: `go vet ./... && go test ./internal/ipfs/ ./internal/publish/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ipfs/embedded.go internal/ipfs/embedded_ipns.go internal/ipfs/embedded_test.go cmd/croptop/main.go
git commit -m "Send records to crop.top's routing endpoint in the background

delegated-ipfs.dev shuts down; records now also go to crop.top, and no
endpoint can hold up a publish.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Peer with crop.top's node, bootstrap through it, remember peers

**Files:**
- Modify: `internal/ipfs/repo.go` (`peers`, lines 21-28)
- Modify: `internal/ipfs/embedded.go` (`Start` lines 107-257, `peer` lines 305-338, `Stop` lines 384-410, struct)
- Create: `internal/ipfs/peers.go`
- Modify: `cmd/croptop/main.go` (`open`)
- Test: `internal/ipfs/embedded_test.go`

**Interfaces:**
- Produces: field `Embedded.PeersURL string` (empty by default), `const ipfs.DefaultPeersURL = "https://crop.top/v0/host/peers"`, `func (e *Embedded) PeerAddrs() []string` (used by Task 4), unexported `hostPeers`, `savePeers`, `loadPeers`, `addrInfos`, `peeringInfos`.

- [ ] **Step 1: Write the failing tests** (append)

```go
// Remembered peers survive a damaged file as "no peers", never a failed start.
func TestSavedPeersReadBackAndDamagedFilesAreIgnored(t *testing.T) {
	e := NewEmbedded(t.TempDir())
	os.MkdirAll(e.nodeDir(), 0o755)
	os.WriteFile(e.peersFile(), []byte(`[{"id":"12D3KooWDSjjQ4GuTGwEbxw6QnfRu3GLa45GzN5s7vAgKTo6wLqY","addrs":["/dns4/altaria.proxy.rlwy.net/tcp/35880","not an address"]},{"id":"not a peer","addrs":["/ip4/1.2.3.4/tcp/1"]}]`), 0o644)
	got := e.loadPeers()
	if len(got) != 1 || got[0].ID.String() != "12D3KooWDSjjQ4GuTGwEbxw6QnfRu3GLa45GzN5s7vAgKTo6wLqY" || len(got[0].Addrs) != 1 {
		t.Fatalf("loadPeers: %v", got)
	}
	os.WriteFile(e.peersFile(), []byte("{damaged"), 0o644)
	if got := e.loadPeers(); got != nil {
		t.Fatalf("a damaged file must read as no peers: %v", got)
	}
}

// The peers list comes from crop.top; an unreachable crop.top costs at most
// the timeout, and Start calls this in a goroutine.
func TestHostPeersReadsTheListAndGivesUpQuickly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"12D3KooWDSjjQ4GuTGwEbxw6QnfRu3GLa45GzN5s7vAgKTo6wLqY","addrs":["/dns4/altaria.proxy.rlwy.net/tcp/35880"]}`))
	}))
	defer srv.Close()
	e := NewEmbedded(t.TempDir())
	e.PeersURL = srv.URL
	if got := e.hostPeers(context.Background()); len(got) != 1 || len(got[0].Addrs) != 1 {
		t.Fatalf("hostPeers: %v", got)
	}
	e.PeersURL = "http://127.0.0.1:1/"
	start := time.Now()
	if got := e.hostPeers(context.Background()); got != nil || time.Since(start) > 6*time.Second {
		t.Fatalf("an unreachable host: %v after %s", got, time.Since(start))
	}
}

func TestPeeringIncludesCropTop(t *testing.T) {
	for _, ai := range peeringInfos() {
		if ai.ID.String() == "12D3KooWDSjjQ4GuTGwEbxw6QnfRu3GLa45GzN5s7vAgKTo6wLqY" {
			return
		}
	}
	t.Fatal("crop.top's node is not in the peering list")
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/ipfs/ -run 'TestSavedPeers|TestHostPeers|TestPeeringIncludesCropTop' -count=1`
Expected: FAIL to compile (`e.peersFile`, `e.loadPeers`, `e.hostPeers`, `peeringInfos` undefined).

- [ ] **Step 3: Add crop.top's node to the peering list**

In `internal/ipfs/repo.go`, add as the first entry of `var peers = []map[string]any{`:

```go
	{"ID": "12D3KooWDSjjQ4GuTGwEbxw6QnfRu3GLa45GzN5s7vAgKTo6wLqY", "Addrs": []string{"/dns4/altaria.proxy.rlwy.net/tcp/35880"}}, // crop.top's node
```

- [ ] **Step 4: Create `internal/ipfs/peers.go`**

```go
package ipfs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// DefaultPeersURL lists the peers crop.top runs, for joining the network now
// that the public bootstrap nodes are gone.
const DefaultPeersURL = "https://crop.top/v0/host/peers"

type savedPeer struct {
	ID    string   `json:"id"`
	Addrs []string `json:"addrs"`
}

func (e *Embedded) peersFile() string { return filepath.Join(e.nodeDir(), "peers.json") }

func addrInfos(in []savedPeer) []peer.AddrInfo {
	var out []peer.AddrInfo
	for _, sp := range in {
		id, err := peer.Decode(sp.ID)
		if err != nil {
			continue
		}
		ai := peer.AddrInfo{ID: id}
		for _, a := range sp.Addrs {
			if m, err := ma.NewMultiaddr(a); err == nil {
				ai.Addrs = append(ai.Addrs, m)
			}
		}
		if len(ai.Addrs) > 0 {
			out = append(out, ai)
		}
	}
	return out
}

// peeringInfos is the fixed peering list (repo.go) as dialable peers.
func peeringInfos() []peer.AddrInfo {
	var in []savedPeer
	for _, p := range peers {
		in = append(in, savedPeer{ID: p["ID"].(string), Addrs: p["Addrs"].([]string)})
	}
	return addrInfos(in)
}

// loadPeers reads the peers savePeers remembered. A missing or damaged file
// is no peers.
func (e *Embedded) loadPeers() []peer.AddrInfo {
	b, err := os.ReadFile(e.peersFile())
	if err != nil {
		return nil
	}
	var in []savedPeer
	if json.Unmarshal(b, &in) != nil {
		return nil
	}
	return addrInfos(in)
}

// savePeers remembers up to 64 connected peers with public addresses, so the
// next start can rejoin the network without any bootstrap peer. An empty list
// keeps the last good one.
func (e *Embedded) savePeers() {
	if e.host == nil {
		return
	}
	var out []savedPeer
	for _, p := range e.host.Network().Peers() {
		sp := savedPeer{ID: p.String()}
		for _, a := range e.host.Peerstore().Addrs(p) {
			if manet.IsPublicAddr(a) {
				sp.Addrs = append(sp.Addrs, a.String())
			}
		}
		if len(sp.Addrs) > 0 {
			out = append(out, sp)
		}
		if len(out) == 64 {
			break
		}
	}
	if len(out) == 0 {
		return
	}
	b, _ := json.Marshal(out)
	// a temp file of its own: Stop and the ten-minute save can overlap
	f, err := os.CreateTemp(filepath.Dir(e.peersFile()), "peers-*.json")
	if err != nil {
		return
	}
	_, err = f.Write(b)
	f.Close()
	if err != nil {
		os.Remove(f.Name())
		return
	}
	os.Rename(f.Name(), e.peersFile())
}

// hostPeers asks PeersURL for the peers a host runs. It gives up after 5 s;
// Start calls it in a goroutine so an unreachable host never delays a start.
func (e *Embedded) hostPeers(ctx context.Context) []peer.AddrInfo {
	if e.PeersURL == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.PeersURL, nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.Log("host peers: " + err.Error())
		return nil
	}
	defer resp.Body.Close()
	var sp savedPeer
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&sp) != nil {
		return nil
	}
	return addrInfos([]savedPeer{sp})
}

// PeerAddrs are the addresses other nodes should dial to reach this one: the
// announced ones when set, else the public ones libp2p knows.
func (e *Embedded) PeerAddrs() []string {
	if e.host == nil {
		return nil
	}
	var out []string
	for _, a := range e.host.Addrs() {
		if len(e.Announce) > 0 || manet.IsPublicAddr(a) {
			out = append(out, a.String())
		}
	}
	return out
}
```

- [ ] **Step 5: Wire it into the struct, `Start`, `peer` and `Stop`**

In `type Embedded struct`, after `RoutingPuts`:

```go
	// PeersURL lists a host's peers to dial at start (crop.top's node); empty to skip.
	PeersURL string
```

In `Start`, replace

```go
		dopts = append(dopts, dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...))
```

with

```go
		// the public bootstrap nodes may be gone: crop.top's node and the peers
		// this node was connected to last time also bootstrap the table
		boot := append(dht.GetDefaultBootstrapPeerAddrInfos(), peeringInfos()...)
		boot = append(boot, e.loadPeers()...)
		dopts = append(dopts, dht.BootstrapPeers(boot...))
```

In `Start`, replace the connect loop

```go
		for _, ai := range dht.GetDefaultBootstrapPeerAddrInfos() {
			go func(ai peer.AddrInfo) {
				c, cancel := context.WithTimeout(runCtx, 30*time.Second)
				defer cancel()
				h.Connect(c, ai)
			}(ai)
		}
```

with

```go
		dial := func(ai peer.AddrInfo) {
			c, cancel := context.WithTimeout(runCtx, 30*time.Second)
			defer cancel()
			h.Connect(c, ai)
		}
		for _, ai := range append(dht.GetDefaultBootstrapPeerAddrInfos(), e.loadPeers()...) {
			go dial(ai)
		}
		go func() {
			for _, ai := range e.hostPeers(runCtx) {
				go dial(ai)
			}
		}()
		go func() { // remember peers, so a crash does not lose them
			t := time.NewTicker(10 * time.Minute)
			defer t.Stop()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-t.C:
					e.savePeers()
				}
			}
		}()
```

In `peer`, replace the block that builds `infos` from `peers` with:

```go
	infos := peeringInfos()
```

In `Stop`, before `if e.cancel != nil {`:

```go
	e.savePeers()
```

In `cmd/croptop/main.go` `open`, after `e.RoutingPuts = ipfs.DefaultRoutingPuts`:

```go
		e.PeersURL = ipfs.DefaultPeersURL
```

- [ ] **Step 6: Run the tests**

Run: `go vet ./... && go test ./internal/ipfs/ -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/ipfs/peers.go internal/ipfs/repo.go internal/ipfs/embedded.go internal/ipfs/embedded_test.go cmd/croptop/main.go
git commit -m "Join the network through crop.top and remembered peers

The public bootstrap nodes shut down. Nodes now peer with crop.top's node,
bootstrap through it and through the peers they were connected to last
time (node/peers.json), and fetch crop.top's current peers at start
without waiting on it.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Routing endpoint and peers list on the node

**Files:**
- Modify: `internal/host/host.go` (`Host` struct lines 48-67, `Start` 83-104, `serveBare` 188, `serveAPI` 392)
- Modify: `internal/ipfs/embedded_ipns.go` (export a validator)
- Test: `internal/host/host_test.go`

**Interfaces:**
- Consumes: `GetRecord` (Task 1), `PeerAddrs` (Task 3), `Embedded.Info`, `Embedded.PutRecord`.
- Produces: `GET|HEAD|PUT /routing/v1/ipns/{name}`, `GET /v0/host/peers` → `{"id": "<peer id>", "addrs": ["<multiaddr>", …]}`; `func ipfs.ValidateRecord(name string, rec []byte) error`.

- [ ] **Step 1: Write the failing test** (append to `internal/host/host_test.go`)

```go
// The node answers the IPNS part of the Delegated Routing API that
// delegated-ipfs.dev used to: pushed sites from the registry, others from the
// DHT; PUT validates and stores. It lists its peers, never forwards records,
// and refuses PUTs when all its slots are busy.
func TestRoutingEndpointAndPeers(t *testing.T) {
	site := offline(t)
	eng := offline(t)
	eng.RoutingPuts = []string{"https://crop.top/routing/v1/ipns/"}
	eng.PeersURL = "https://crop.top/v0/host/peers"
	h := &Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: eng}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if len(eng.RoutingPuts) != 0 || eng.PeersURL != "" {
		t.Fatal("a host must not forward records or bootstrap from itself")
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	name, _ := site.Keystore().Generate("s")
	rec, _ := site.SignRecord("s", "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4", 7)
	get := func(n string) (int, []byte) {
		resp, err := http.Get(srv.URL + "/routing/v1/ipns/" + n)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	put := func(n string, b []byte) int {
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/routing/v1/ipns/"+n, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/vnd.ipfs.ipns-record")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code, _ := get(name); code != 404 {
		t.Fatalf("unknown name: %d", code)
	}
	if code := put(name, []byte("not a record")); code != 400 {
		t.Fatalf("garbage: %d", code)
	}
	site.Keystore().Generate("o")
	wrong, _ := site.SignRecord("o", "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4", 1)
	if code := put(name, wrong); code != 400 {
		t.Fatalf("another name's record: %d", code)
	}
	if code := put(name, rec); code != 200 {
		t.Fatalf("valid record: %d", code)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, b := get(name)
		if code == 200 && bytes.Equal(b, rec) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET after PUT: %d", code)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// a pushed site answers from the registry at once
	pushed, _ := site.Keystore().Generate("p")
	pushedRec, _ := site.SignRecord("p", "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4", 9)
	h.mu.Lock()
	h.reg.Keys[pushed] = &Entry{IPNS: pushed, Record: pushedRec}
	h.mu.Unlock()
	if code, b := get(pushed); code != 200 || !bytes.Equal(b, pushedRec) {
		t.Fatalf("pushed site: %d", code)
	}
	// no free slot: 429, not an unbounded pile of DHT puts
	h.routingSlots = make(chan struct{})
	if code := put(name, rec); code != 429 {
		t.Fatalf("all slots busy: %d", code)
	}
	resp, err := http.Get(srv.URL + "/v0/host/peers")
	if err != nil {
		t.Fatal(err)
	}
	var peers struct {
		ID    string   `json:"id"`
		Addrs []string `json:"addrs"`
	}
	json.NewDecoder(resp.Body).Decode(&peers)
	resp.Body.Close()
	if peers.ID == "" {
		t.Fatal("peers: no id")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/host/ -run TestRoutingEndpointAndPeers -count=1`
Expected: FAIL to compile (`h.routingSlots` undefined).

- [ ] **Step 3: Export the record check**

In `internal/ipfs/embedded_ipns.go`, add and use it in `PutRecord`:

```go
// ValidateRecord checks that rec is a valid record signed by the key inside name.
func ValidateRecord(nameStr string, rec []byte) error {
	name, err := ipns.NameFromString(strings.TrimPrefix(nameStr, "/ipns/"))
	if err != nil {
		return err
	}
	r, err := ipns.UnmarshalRecord(rec)
	if err != nil {
		return err
	}
	return ipns.ValidateWithName(r, name)
}
```

and make `PutRecord` start with:

```go
	if e.dht == nil {
		return fmt.Errorf("node not started")
	}
	if err := ValidateRecord(ipnsName, rec); err != nil {
		return err
	}
	name, _ := ipns.NameFromString(strings.TrimPrefix(ipnsName, "/ipns/"))
	return e.putRecord(ctx, name, rec)
```

(replacing its previous unmarshal and validate lines).

- [ ] **Step 4: Add the endpoint, the peers list and the host's own settings**

In `internal/host/host.go`, add to `type Host struct` (unexported, after `cache`):

```go
	routingSlots chan struct{} // routing PUTs in flight; full means 429
```

In `Start`, before `return nil`:

```go
	// a host is the routing endpoint and a bootstrap peer: it must not send
	// records to routing endpoints (the Worker proxies them back here) or
	// fetch its own peers list
	h.Engine.RoutingPuts, h.Engine.PeersURL = nil, ""
	h.routingSlots = make(chan struct{}, 32)
```

In `serveBare`, as the first case of the `switch`:

```go
	case strings.HasPrefix(p, "/routing/v1/ipns/"):
		h.serveRouting(w, r, strings.TrimSuffix(strings.TrimPrefix(p, "/routing/v1/ipns/"), "/"))
```

In `serveAPI`, after the `health` case:

```go
	case p == "peers" && r.Method == "GET":
		// what a new node dials to join the network through this host
		info, err := h.Engine.Info(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, map[string]any{"id": info.PeerID, "addrs": h.Engine.PeerAddrs()})
```

Add the handler:

```go
// serveRouting is the IPNS part of the Delegated Routing V1 HTTP API, which
// delegated-ipfs.dev served until 2026-09-30: GET the newest record for a name,
// PUT a signed record into the DHT. Pushed sites answer from the registry.
func (h *Host) serveRouting(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.mu.Lock()
		var rec []byte
		if e := h.reg.Keys[name]; e != nil {
			rec = e.Record
		}
		h.mu.Unlock()
		if len(rec) == 0 {
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			var err error
			if rec, err = h.Engine.GetRecord(ctx, name); err != nil {
				http.Error(w, "not found", 404)
				return
			}
		}
		w.Header().Set("Content-Type", "application/vnd.ipfs.ipns-record")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Write(rec)
	case http.MethodPut:
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<10))
		if err != nil {
			http.Error(w, "record too large", 413)
			return
		}
		if err := ipfs.ValidateRecord(name, b); err != nil {
			http.Error(w, "invalid record: "+err.Error(), 400)
			return
		}
		select {
		case h.routingSlots <- struct{}{}:
		default:
			w.Header().Set("Retry-After", "10")
			http.Error(w, "busy", 429)
			return
		}
		go func() { // a DHT put takes seconds; the caller does not wait for it
			defer func() { <-h.routingSlots }()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := h.Engine.PutRecord(ctx, name, b); err != nil {
				h.log("routing put %s: %v", name, err)
			}
		}()
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", 405)
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `go vet ./... && go test -p 1 ./internal/host/ ./internal/ipfs/ ./internal/publish/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/host/host.go internal/host/host_test.go internal/ipfs/embedded_ipns.go
git commit -m "Node serves IPNS routing and its peers list

GET/PUT /routing/v1/ipns/{name} replaces delegated-ipfs.dev for crop.top;
GET /v0/host/peers lets new nodes join through crop.top. A host never
forwards records, and busy PUT slots answer 429.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The Worker routes names through the node

**Files:**
- Modify: `worker/src/index.js` (`serveBare` line ~46, `api`, `resolveKey` lines 85-107, `republish` 546-549 and its callers, the export line at the end)
- Test: `worker/test/push.test.mjs`

**Interfaces:**
- Consumes: the node's `/routing/v1/ipns/{name}` and `/v0/host/peers` (Task 4).
- Produces: `crop.top/routing/v1/ipns/{name}` (GET/HEAD/PUT), `crop.top/v0/host/peers`; `republish(env, ipns, recordB64)` (exported for tests).

- [ ] **Step 1: Write the failing tests** (append to `worker/test/push.test.mjs`; change the import to `import worker, { republish } from "../src/index.js";`)

```js
// protobuf of an IPNS record with only value (field 1) and sequence (field 5), enough for parseRecord
const recordBytes = (value, seq) => Uint8Array.from([0x0a, value.length, ...new TextEncoder().encode(value), 0x28, seq]);

test("names resolve and route through the node, not delegated-ipfs.dev", async () => {
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", UPSTREAMS: "https://up.test", SITES: r2(), REGISTRY: kv() };
  const calls = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init = {}) => {
    u = String(u);
    calls.push({ u, method: init.method || "GET" });
    if (u.startsWith("https://node.test/routing/v1/ipns/")) return new Response(init.method === "PUT" ? null : recordBytes("/ipfs/bafyfromnode", 3), { headers: { "content-type": "application/vnd.ipfs.ipns-record" } });
    if (u === "https://node.test/v0/host/peers") return new Response('{"id":"12D3KooWnode","addrs":["/dns4/x/tcp/1"]}', { headers: { "content-type": "application/json" } });
    if (u.startsWith("https://up.test/ipfs/bafyfromnode/")) return new Response("from upstream");
    return new Response("nope", { status: 404 });
  };
  const call = (path, init, host = "crop.test") => worker.fetch(new Request(`https://${host}${path}`, init), env, { waitUntil() {} });
  const pushed = "k51qzi5uqu5dlgq33myrm8ik5j5m87add6c1vwaz2ubxhobjtibprs7nc9bnto";
  const other = "k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb";
  try {
    await env.REGISTRY.put("key:" + pushed, JSON.stringify({ ipns: pushed, cid: "bafyx", sequence: 1, record: btoa("signed") }));
    const own = await call("/routing/v1/ipns/" + pushed);
    assert.equal(new TextDecoder().decode(await own.arrayBuffer()), "signed");
    assert.equal(calls.length, 0, "a pushed name is answered from the registry");
    const r = await call("/routing/v1/ipns/" + other);
    assert.equal(r.status, 200);
    assert.equal(r.headers.get("content-type"), "application/vnd.ipfs.ipns-record");
    assert.equal((await call("/routing/v1/ipns/" + other, { method: "PUT", body: new Uint8Array([1]) })).status, 200);
    assert.equal(calls.at(-1).method, "PUT");
    assert.equal((await call("/routing/v1/ipns/not-a-name")).status, 400);
    assert.equal((await (await call("/v0/host/peers")).json()).id, "12D3KooWnode");
    // a name crop.top does not host resolves through the node
    const site = await call("/planet.json", undefined, `${other}.crop.test`);
    assert.equal(await site.text(), "from upstream");
    assert.ok(!calls.some((c) => c.u.includes("delegated-ipfs.dev")), "the node answered first");
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("the node being down gives 502, not a hung request", async () => {
  const env = { DOMAIN: "crop.test", NODE: "https://node.test", SITES: r2(), REGISTRY: kv() };
  const realFetch = globalThis.fetch;
  globalThis.fetch = async () => { throw new Error("connection refused"); };
  try {
    const r = await worker.fetch(new Request("https://crop.test/routing/v1/ipns/k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb"), env, { waitUntil() {} });
    assert.equal(r.status, 502);
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("republishing sends a record to the node and to delegated routing", async () => {
  const puts = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = async (u, init = {}) => { puts.push({ u: String(u), method: init.method }); return new Response(null); };
  try {
    await republish({ NODE: "https://node.test" }, "k51abc", btoa("signed"));
    assert.deepEqual(puts.map((p) => p.u).sort(), ["https://delegated-ipfs.dev/routing/v1/ipns/k51abc", "https://node.test/routing/v1/ipns/k51abc"]);
    assert.ok(puts.every((p) => p.method === "PUT"));
  } finally {
    globalThis.fetch = realFetch;
  }
});
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd worker && node --experimental-loader ./text-loader.mjs --test test/`
Expected: FAIL (`republish` is not exported; `/routing/v1/ipns/…` returns the root site).

- [ ] **Step 3: Add the proxies**

In `serveBare`, before `if (p.startsWith("/v0/host/")) return api(request, url, env, ctx);`:

```js
  if (p.startsWith("/routing/v1/ipns/")) return routing(request, env, p.slice("/routing/v1/ipns/".length).replace(/\/$/, ""));
```

In `api`, after the `blocks/` line:

```js
  if (p === "peers" && request.method === "GET") return peers(env);
```

Add these functions after `block`:

```js
// routing is the IPNS part of the Delegated Routing V1 HTTP API, which
// delegated-ipfs.dev served until 2026-09-30. Pushed sites answer from the
// registry; everything else, and every PUT, goes to the node, which keeps
// records in the DHT.
async function routing(request, env, name) {
  if (!/^k[a-z0-9]{40,80}$/.test(name)) return text("bad name", 400);
  const raw = { "content-type": "application/vnd.ipfs.ipns-record", "cache-control": "public, max-age=60" };
  const get = request.method === "GET" || request.method === "HEAD";
  if (!get && request.method !== "PUT") return text("method not allowed", 405);
  if (get) {
    const e = await entryByKey(env, name);
    if (e && e.record) return new Response(request.method === "HEAD" ? null : Uint8Array.from(atob(e.record), (c) => c.charCodeAt(0)), { headers: raw });
  }
  if (!env.NODE) return text("not found", 404);
  const r = await fetch(`${env.NODE}/routing/v1/ipns/${name}`, { method: request.method, headers: { ...UA, "content-type": raw["content-type"] }, body: get ? undefined : await request.arrayBuffer(), signal: AbortSignal.timeout(35000) }).catch(() => null);
  if (!r) return text("routing unavailable", 502);
  return new Response(r.body, { status: r.status, headers: r.ok && get ? raw : { "content-type": r.headers.get("content-type") || "text/plain" } });
}

// peers lists the node's addresses, for new nodes to join the network through
// now that the public bootstrap nodes are gone.
async function peers(env) {
  if (!env.NODE) return text("not found", 404);
  const r = await fetch(`${env.NODE}/v0/host/peers`, { headers: UA, cf: { cacheTtl: 3600, cacheEverything: true }, signal: AbortSignal.timeout(10000) }).catch(() => null);
  if (!r || !r.ok) return text("peers unavailable", 502);
  return new Response(r.body, { headers: { "content-type": "application/json", "cache-control": "public, max-age=3600" } });
}
```

- [ ] **Step 4: Resolve and republish through the node**

In `resolveKey`, replace

```js
    // the delegated endpoint is fast but only knows records published to it;
    // the upstream gateways resolve the rest through their own nodes
    for (const src of [`https://delegated-ipfs.dev/routing/v1/ipns/${ipns}`]) {
      try {
        const r = await fetch(src, { headers: { Accept: "application/vnd.ipfs.ipns-record" }, redirect: "follow" });
```

with

```js
    // the node answers from the DHT; delegated-ipfs.dev is a fallback while it
    // lasts; the upstream gateways resolve the rest through their own nodes
    const sources = [env.NODE && `${env.NODE}/routing/v1/ipns/${ipns}`, `https://delegated-ipfs.dev/routing/v1/ipns/${ipns}`].filter(Boolean);
    for (const src of sources) {
      try {
        const r = await fetch(src, { headers: { Accept: "application/vnd.ipfs.ipns-record" }, redirect: "follow", signal: AbortSignal.timeout(20000) });
```

Replace `republish` with:

```js
async function republish(env, ipns, recordB64) {
  const body = Uint8Array.from(atob(recordB64), (c) => c.charCodeAt(0));
  const put = (u) => fetch(u, { method: "PUT", headers: { "content-type": "application/vnd.ipfs.ipns-record" }, body, signal: AbortSignal.timeout(20000) }).catch(() => {});
  await Promise.all([env.NODE && put(`${env.NODE}/routing/v1/ipns/${ipns}`), put(`https://delegated-ipfs.dev/routing/v1/ipns/${ipns}`)]);
}
```

Update its two callers: in `push`, `ctx.waitUntil(republish(ipns, e.record))` becomes `ctx.waitUntil(republish(env, ipns, e.record))`; in `republishAll`, `await republish(e.ipns, e.record)` becomes `await republish(env, e.ipns, e.record)`.

Change the last line to:

```js
export { publicKeyOf, verify, parseRecord, base36Decode, republish };
```

- [ ] **Step 5: Run the tests**

Run: `node --check worker/src/index.js && cd worker && node --experimental-loader ./text-loader.mjs --test test/`
Expected: all pass (8 tests).

- [ ] **Step 6: Commit**

```bash
git add worker/src/index.js worker/test/push.test.mjs
git commit -m "Worker routes names through crop.top's node

crop.top/routing/v1/ipns/ and /v0/host/peers proxy to the node; names
resolve and republish through it first, with delegated-ipfs.dev as a
fallback while it lasts.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: No sunset gateways in fetch lists or warm-ups

**Files:**
- Modify: `internal/gateway/gateway.go` (`FetchURLs`, around lines 205-220)
- Modify: `internal/publish/fetch.go` (`prewarm`, `prewarmAll`)
- Test: `internal/gateway/gateway_test.go` (lines ~120-123)

**Interfaces:**
- Produces: `FetchURLs` returns only name-resolving gateway URLs from `Table` (CID first, then IPNS name).

- [ ] **Step 1: Change the test to expect no dweb.link**

Replace

```go
	urls := FetchURLs("k51abc", "bafyX")
	if urls[0] != "https://bafyX.eth.sucks/" || urls[len(urls)-1] != "https://dweb.link/ipns/k51abc/" {
		t.Fatalf("fetch urls: %v", urls)
	}
```

with

```go
	urls := FetchURLs("k51abc", "bafyX")
	if urls[0] != "https://bafyX.eth.sucks/" || urls[len(urls)-1] != "https://k51abc.crop.top/" {
		t.Fatalf("fetch urls: %v", urls)
	}
	for _, u := range urls {
		if strings.Contains(u, "dweb.link") || strings.Contains(u, "ipfs.io") {
			t.Fatalf("a sunset gateway is still listed: %s", u) // they refuse programmatic fetches since 2026-09-21
		}
	}
```

(add `"strings"` to the test's imports if missing)

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/gateway/ -count=1`
Expected: FAIL, `fetch urls: [… https://dweb.link/ipns/k51abc/]`.

- [ ] **Step 3: Remove the two dweb.link lines**

In `FetchURLs`, delete `out = append(out, "https://dweb.link/ipfs/"+cid+"/")` and change the final `return append(out, "https://dweb.link/ipns/"+ipns+"/")` to `return out`.

In `internal/publish/fetch.go`, warm-ups reach dweb.link only through `gateway.CIDURL` for CIDv0 roots (Planet-era sites); skip it there. In `prewarm`, make the first line of the `for _, u := range urls {` loop body:

```go
		if strings.Contains(u, "dweb.link") {
			continue // refuses programmatic fetches since 2026-09-21
		}
```

and in `prewarmAll`, right after `base := gateway.CIDURL(site, cid)`:

```go
	if strings.Contains(base, "dweb.link") {
		return // a CIDv0 root: its only CID gateway refuses programmatic fetches
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/gateway/ ./internal/publish/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/gateway.go internal/gateway/gateway_test.go internal/publish/fetch.go
git commit -m "Drop dweb.link from fetch lists and warm-ups

It refuses programmatic fetches since 2026-09-21.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Docs

**Files:**
- Modify: `docs/host.md` (API section), `README.md` (IPFS engine section), `docs/superpowers/specs/2026-09-30-scaling-and-agent-economy-design.md` (G, Bootstrap paragraph)

- [ ] **Step 1: Document the endpoints** — in `docs/host.md`, add to the API list:

```markdown
- `GET|PUT /routing/v1/ipns/<name>`: the IPNS part of the Delegated Routing V1
  HTTP API, which delegated-ipfs.dev served until 2026-09-30. GET answers pushed
  sites from the registry and others from the node's DHT; PUT takes a signed
  record (checked against the name) and puts it in the DHT. The node takes up to
  32 PUTs at a time and answers `429` beyond that.
- `GET /v0/host/peers`: `{"id", "addrs"}`, the node's peer ID and announced
  addresses. croptop dials them at start to join the network.
```

- [ ] **Step 2: Update the README** — in "IPFS engine", replace "A publish writes the IPNS record to the DHT, to the public delegated routing endpoint, and to the IPNS pubsub topic." with:

```markdown
A publish writes the IPNS record to the DHT and the IPNS pubsub topic, and
sends it to crop.top's routing endpoint (and delegated-ipfs.dev while it
lasts). The node joins the network through the default bootstrap peers,
crop.top's node, and the peers it was connected to last time.
```

- [ ] **Step 3: Correct the spec** — in the spec's G "Bootstrap" paragraph, replace "The embedded engine keeps its peerstore across restarts, so a node that has joined once does not need a bootstrap peer to rejoin." with "The embedded engine remembers up to 64 peers it was connected to (`node/peers.json`) and dials them at start, so a node that has joined once does not need a bootstrap peer to rejoin."

- [ ] **Step 4: Commit**

```bash
git add docs/host.md README.md docs/superpowers/specs/2026-09-30-scaling-and-agent-economy-design.md
git commit -m "Document routing through crop.top and remembered peers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Ship G

- [ ] **Step 1: Full test run**

Run: `go vet ./... && go test -p 1 ./... -count=1 && (cd worker && node --experimental-loader ./text-loader.mjs --test test/)`
Expected: every Go package `ok`, Worker `# pass 8`, `# fail 0`.

- [ ] **Step 2: Bump the build and push**

```bash
echo $(( $(cat installer/macos-build-number) + 1 )) > installer/macos-build-number
git add installer/macos-build-number
git commit -m "Routing and bootstrap off sunset services (build $(cat installer/macos-build-number))

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push origin main
```

- [ ] **Step 3: Deploy the node first** (it must serve the new paths before the Worker proxies to them)

```bash
U=$(mktemp -d)
git archive HEAD go.mod go.sum cmd internal web templates Dockerfile railway.toml .dockerignore | tar -x -C "$U"
(cd templates/croptop && git archive HEAD) | tar -x -C "$U/templates/croptop"
(cd "$U" && CGO_ENABLED=0 go build -o /dev/null ./cmd/croptop)
railway up "$U" --path-as-root --service croptop-host --environment production --ci
```

Verify (wait until `railway deployment list` shows the new deployment `SUCCESS`):
`curl -s https://croptop-host-production.up.railway.app/v0/host/peers` → JSON with `"id":"12D3KooWDSjj…"` and `"/dns4/altaria.proxy.rlwy.net/tcp/35880"`.
`curl -s -o /dev/null -w '%{http_code} %{content_type}\n' https://croptop-host-production.up.railway.app/routing/v1/ipns/k51qzi5uqu5dlgq33myrm8ik5j5m87add6c1vwaz2ubxhobjtibprs7nc9bnto` → `200 application/vnd.ipfs.ipns-record`.

- [ ] **Step 4: Deploy the Worker**

Run: `cd worker && npx -y -p node@22 -p wrangler@4.145.0 wrangler deploy -c wrangler.toml`
Verify (repeat for up to a minute while the version propagates): `curl -s -o /dev/null -w '%{http_code}\n' https://crop.top/routing/v1/ipns/k51qzi5uqu5dhxiwvl4xx3sco13y50yoo28rbhe3sneirnj2qatinx75qpsqtb` → `200` or `404` (never the root site's HTML), and `curl -s https://crop.top/v0/host/peers` → the node's JSON. Existing pages: `https://crop.top/`, `https://crop.top/follo/` → `200`.

- [ ] **Step 5: Release the client**

```bash
VER=0.13.17
git tag -a v$VER -m "v$VER: routing and bootstrap off sunset services"
git push origin v$VER
```

Wait for the release workflow's `goreleaser` job to succeed, then ask the user for the notary key path and issuer ID and run
`SPARKLE_KEY_FILE=~/Documents/croptop-signing/sparkle-ed25519.key AC_API_KEY_PATH=<p8> AC_API_KEY_ID=<id> AC_API_ISSUER_ID=<issuer> installer/release-macos.sh $VER`.
Verify: `curl -sL https://github.com/mejango/croptop/releases/latest/download/appcast.xml | grep -o '<sparkle:version>[0-9]*'` shows the new build.

- [ ] **Step 6: Live check**

With the released binary: `CROPTOP_DEBUG=1 croptop post --key <throwaway site.pem> --title "G check"` and confirm the log shows a routing put to `https://crop.top/routing/v1/ipns/` answered `200 OK` and the post is served. Then run `croptop serve --data <scratch dir> --listen 127.0.0.1:8098 --no-open` for 3 minutes, stop it, and confirm `<scratch dir>/node/peers.json` lists peers. Stop every test process started here.
