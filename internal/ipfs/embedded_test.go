package ipfs

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ipfs/boxo/ipld/merkledag"
	ft "github.com/ipfs/boxo/ipld/unixfs"
	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// kuboNode returns the downloaded kubo when present, for parity checks.
func kuboNode(t *testing.T) *Node {
	bin := filepath.Join("..", "..", ".data", "kubo", "ipfs")
	repo := filepath.Join("..", "..", ".data", "ipfs")
	if _, err := os.Stat(bin); err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(repo, "config")); err != nil {
		return nil
	}
	return NewNode(bin, repo)
}

func TestFileCIDv0MatchesPlanet(t *testing.T) {
	got, err := fileCIDv0(context.Background(), "../render/testdata/public-post/nft.json")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile("../render/testdata/public-post/nft.json.cid.txt")
	if got != strings.TrimSpace(string(want)) {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestFileCIDv0LargeFileMatchesKubo(t *testing.T) {
	k := kuboNode(t)
	if k == nil {
		t.Skip("kubo not downloaded")
	}
	p := filepath.Join(t.TempDir(), "big.bin")
	b := make([]byte, 3*chunkSize+12345) // several chunks, exercises the balanced layout
	rand.Read(b)
	os.WriteFile(p, b, 0o644)
	ours, err := fileCIDv0(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := k.FileCIDv0(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if ours != theirs {
		t.Fatalf("ours %s kubo %s", ours, theirs)
	}
}

func TestAddDirMatchesKubo(t *testing.T) {
	k := kuboNode(t)
	if k == nil {
		t.Skip("kubo not downloaded")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "post", "nested"), 0o755)
	os.WriteFile(filepath.Join(dir, "planet.json"), []byte(`{"a":1}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("h"), 0o644)
	os.WriteFile(filepath.Join(dir, "post", "article.json"), []byte(`{"b":2}`), 0o644)
	big := make([]byte, 2*chunkSize+777)
	rand.Read(big)
	os.WriteFile(filepath.Join(dir, "post", "photo.png"), big, 0o644)
	os.WriteFile(filepath.Join(dir, "post", "nested", "empty"), nil, 0o644)
	// many entries push the directory past the HAMT threshold
	wide := filepath.Join(dir, "wide")
	os.MkdirAll(wide, 0o755)
	for i := 0; i < 5000; i++ {
		os.WriteFile(filepath.Join(wide, "file-"+strings.Repeat("x", 40)+"-"+itoa(i)), []byte{byte(i)}, 0o644)
	}
	nd, err := addDir(context.Background(), memoryDAG(), dir)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := k.AddDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if nd.Cid().String() != theirs {
		t.Fatalf("ours %s kubo %s", nd.Cid(), theirs)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var s []byte
	for n > 0 {
		s = append([]byte{byte('0' + n%10)}, s...)
		n /= 10
	}
	return string(s)
}

// AddOver must give the root an add of the whole merged tree gives, or a host
// rebuilding a pushed post from the version it holds would not match it.
func TestAddOverMatchesAddDirOfMergedTree(t *testing.T) {
	e := NewEmbedded(t.TempDir())
	e.Offline = true
	ctx := context.Background()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	for _, width := range []int{3, 5000} { // 5000 entries shard the root (HAMT)
		base, over, merged := t.TempDir(), t.TempDir(), t.TempDir()
		for _, d := range []string{base, merged} {
			os.MkdirAll(filepath.Join(d, "old-post"), 0o755)
			os.WriteFile(filepath.Join(d, "old-post", "photo.png"), []byte("old photo"), 0o644)
			os.WriteFile(filepath.Join(d, "index.html"), []byte("<html>"), 0o644)
			for i := 0; i < width; i++ {
				os.WriteFile(filepath.Join(d, "tag-"+strings.Repeat("x", 40)+"-"+itoa(i)+".html"), []byte{byte(i)}, 0o644)
			}
		}
		os.WriteFile(filepath.Join(base, "planet.json"), []byte(`{"articles":[1]}`), 0o644)
		for _, d := range []string{over, merged} {
			os.WriteFile(filepath.Join(d, "planet.json"), []byte(`{"articles":[2,1]}`), 0o644)
			os.MkdirAll(filepath.Join(d, "new-post"), 0o755)
			os.WriteFile(filepath.Join(d, "new-post", "article.json"), []byte(`{"id":2}`), 0o644)
		}
		baseCID, err := e.AddDir(ctx, base)
		if err != nil {
			t.Fatal(err)
		}
		got, err := e.AddOver(ctx, baseCID, over)
		if err != nil {
			t.Fatal(err)
		}
		want, err := e.AddDir(ctx, merged)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("width %d: AddOver %s, AddDir of merged tree %s", width, got, want)
		}
		names, err := e.Links(ctx, baseCID)
		if err != nil || len(names) != width+3 || names["index.html"] == "" {
			t.Fatalf("width %d: Links = %d names, %v", width, len(names), err)
		}
	}
}

// A version's folder blocks are enough for another node to list it, without
// its file data and without the network.
func TestDirBlocksListAVersionElsewhere(t *testing.T) {
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
	a, b := start(), start()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "post", "nested"), 0o755)
	os.WriteFile(filepath.Join(dir, "planet.json"), []byte("{}"), 0o644)
	big := make([]byte, 3*chunkSize) // a chunked file: its root is dag-pb but not a folder
	rand.Read(big)
	os.WriteFile(filepath.Join(dir, "post", "photo.png"), big, 0o644)
	os.WriteFile(filepath.Join(dir, "post", "nested", "a.txt"), []byte("a"), 0o644)
	root, err := a.AddDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := a.DirBlocks(ctx, root)
	if err != nil || len(blocks) != 3 {
		t.Fatalf("want the root, post and nested folders, got %d: %v", len(blocks), err)
	}
	for c, data := range blocks {
		if err := b.PutBlock(ctx, c, data); err != nil {
			t.Fatal(err)
		}
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	top, err := b.Links(lctx, root)
	if err != nil {
		t.Fatal(err)
	}
	post, err := b.Links(lctx, top["post"])
	if err != nil || post["photo.png"] == "" || post["nested"] == "" {
		t.Fatalf("post folder: %v %v", post, err)
	}
	if err := b.PutBlock(ctx, root, []byte("not the block")); err == nil {
		t.Error("PutBlock took a block that does not hash to its CID")
	}
}

func TestEmbeddedOfflineAddGetRoundTrip(t *testing.T) {
	e := NewEmbedded(t.TempDir())
	e.Offline = true
	ctx := context.Background()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	src := filepath.Join(t.TempDir(), "site")
	os.MkdirAll(filepath.Join(src, "p"), 0o755)
	os.WriteFile(filepath.Join(src, "planet.json"), []byte(`{"id":"x"}`), 0o644)
	os.WriteFile(filepath.Join(src, "p", "a.txt"), []byte("hello"), 0o644)
	root, err := e.AddDir(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(root, "bafy") {
		t.Fatalf("root %s", root)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := e.Get(ctx, "/ipfs/"+root, dest); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dest, "p", "a.txt"))
	if string(b) != "hello" {
		t.Fatalf("round trip: %q", b)
	}
	if err := e.Get(ctx, "/ipfs/"+root+"/planet.json", filepath.Join(dest, "pj")); err != nil {
		t.Fatal(err)
	}
	info, err := e.Info(ctx)
	if err != nil || !strings.HasPrefix(info.PeerID, "12D3KooW") {
		t.Fatalf("info %+v %v", info, err)
	}
}

func TestSiteKeyNameMatchesKeystore(t *testing.T) {
	e := NewEmbedded(t.TempDir())
	ks := e.Keystore()
	want, err := ks.Generate("site")
	if err != nil {
		t.Fatal(err)
	}
	_, name, err := e.siteKey("site")
	if err != nil {
		t.Fatal(err)
	}
	if name.String() != want {
		t.Fatalf("libp2p name %s keystore name %s", name, want)
	}
}

func TestIPNSRecordRoundTrip(t *testing.T) {
	sk, _, _ := crypto.GenerateEd25519Key(nil)
	pid, _ := peer.IDFromPrivateKey(sk)
	c, _ := cid.Decode("bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4")
	rec, err := ipns.NewRecord(sk, path.FromCid(c), 42, time.Now().Add(time.Hour), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ipns.MarshalRecord(rec)
	back, err := ipns.UnmarshalRecord(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := ipns.ValidateWithName(back, ipns.NameFromPeer(pid)); err != nil {
		t.Fatal(err)
	}
	seq, _ := back.Sequence()
	v, _ := back.Value()
	if seq != 42 || v.String() != "/ipfs/"+c.String() {
		t.Fatalf("seq %d value %s", seq, v)
	}
}

func TestParseDNSLink(t *testing.T) {
	v, ok := parseDNSLink([]string{`"v=spf1"`, `dnslink=/ipns/k51abc`})
	if !ok || v != "/ipns/k51abc" {
		t.Fatalf("%q %v", v, ok)
	}
	if _, ok := parseDNSLink([]string{"dnslink=garbage"}); ok {
		t.Fatal("should reject")
	}
}

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

// A routing endpoint gets each record in the background; one that is gone
// never holds up a publish.
func TestPutRecordSendsToRoutingEndpointsInTheBackground(t *testing.T) {
	got := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Method == http.MethodPut && r.Header.Get("Content-Type") == "application/vnd.ipfs.ipns-record" && strings.HasPrefix(r.URL.Path, "/routing/v1/ipns/k") {
			select {
			case got <- b:
			default:
			}
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
	name, err := e.Keystore().Generate("s")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := e.SignRecord("s", "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4", 1)
	if err != nil {
		t.Fatal(err)
	}
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

// A routing endpoint that takes the request and never answers must not hold up a put.
func TestPutRecordDoesNotWaitOnAHungRoutingEndpoint(t *testing.T) {
	release := make(chan struct{})
	reached := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
		<-release
	}))
	defer srv.Close()
	defer close(release) // LIFO: the handler is released before srv.Close waits for it
	ctx := context.Background()
	e := NewEmbedded(t.TempDir())
	e.Offline = true
	e.RoutingPuts = []string{srv.URL + "/routing/v1/ipns/"}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	name, _ := e.Keystore().Generate("s")
	rec, _ := e.SignRecord("s", "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4", 1)
	start := time.Now()
	e.PutRecord(ctx, name, rec)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("a hung routing endpoint held up the put for %s", d)
	}
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the endpoint never got the request")
	}
}

// A relay puts a record for any name in the DHT and sends it nowhere else: a
// routing endpoint takes records from anyone, so nothing it takes may pile up
// as background sends or topics. Another name's record is refused.
func TestRelayRecordPutsInTheDHTAndSendsNowhereElse(t *testing.T) {
	paths := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case paths <- r.URL.Path:
		default:
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	e := NewEmbedded(t.TempDir())
	e.Offline = true
	e.RoutingPuts = []string{srv.URL + "/routing/v1/ipns/"}
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	const c = "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4"
	relayed, _ := e.Keystore().Generate("r")
	relayedRec, _ := e.SignRecord("r", c, 1)
	own, _ := e.Keystore().Generate("o")
	ownRec, _ := e.SignRecord("o", c, 1)
	if err := e.RelayRecord(ctx, relayed, ownRec); err == nil {
		t.Fatal("relayed another name's record")
	}
	e.RelayRecord(ctx, relayed, relayedRec) // stored locally first; offline there is no peer to send it to
	if got, err := e.GetRecord(ctx, relayed); err != nil || !bytes.Equal(got, relayedRec) {
		t.Fatalf("the DHT does not hold the relayed record: %v", err)
	}
	// a put of the node's own does reach the endpoint, and a relay's would have by now
	e.PutRecord(ctx, own, ownRec)
	for {
		select {
		case p := <-paths:
			if p == "/routing/v1/ipns/"+relayed {
				t.Fatal("a relayed record went on to the routing endpoint")
			}
			if p == "/routing/v1/ipns/"+own {
				return
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the endpoint never got the node's own put")
		}
	}
}

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

// savePeers remembers a connected peer by its public addresses only, and an
// empty list leaves the last good file alone.
func TestSavePeersKeepsPublicAddressesAndTheLastGoodList(t *testing.T) {
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
	a, b, c := start(), start(), start()
	if err := a.host.Connect(ctx, peer.AddrInfo{ID: b.host.ID(), Addrs: b.host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	pub := ma.StringCast("/ip4/1.2.3.4/tcp/4001")
	a.host.Peerstore().AddAddr(b.host.ID(), pub, time.Hour)
	// an offline peer is also known by its loopback address, which must not be kept
	if n := len(a.host.Peerstore().Addrs(b.host.ID())); n < 2 {
		t.Fatalf("a knows %d addresses for b, want its loopback one and the public one", n)
	}
	a.savePeers()
	got := a.loadPeers()
	if len(got) != 1 || got[0].ID != b.host.ID() || len(got[0].Addrs) != 1 || !got[0].Addrs[0].Equal(pub) {
		t.Fatalf("savePeers kept %v, want only %s of b", got, pub)
	}

	// c has no connections: a save must leave its last good file as it was
	if n := len(c.host.Network().Peers()); n != 0 {
		t.Fatalf("c is connected to %d peers", n)
	}
	sentinel := []byte(`[{"id":"12D3KooWDSjjQ4GuTGwEbxw6QnfRu3GLa45GzN5s7vAgKTo6wLqY","addrs":["/dns4/altaria.proxy.rlwy.net/tcp/35880"]}]`)
	if err := os.WriteFile(c.peersFile(), sentinel, 0o644); err != nil {
		t.Fatal(err)
	}
	c.savePeers()
	if back, err := os.ReadFile(c.peersFile()); err != nil || !bytes.Equal(back, sentinel) {
		t.Fatalf("an empty list replaced the last good one: %q %v", back, err)
	}
}

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
	// every way a manifest is wrong on its own terms is refused as that, so a
	// host answers its caller with it and keeps its own failures apart
	ups := writeTree(t, map[string]string{"p1/index.html": "x"})
	for _, bad := range []struct {
		carry []string
		why   string
	}{
		{[]string{"nope"}, "not in the parent"},
		{[]string{"index.html/x"}, "through a file"},
		{[]string{"p1"}, "the upload p1/index.html is inside it"},
		{[]string{"p1/index.html"}, "the upload p1/index.html is given twice"},
		{[]string{"assets", "assets/site.css"}, "one carried path inside another"},
		{[]string{"../x"}, "climbs out of the version"},
		{[]string{""}, "empty"},
	} {
		if _, err := e.Rebuild(ctx, parent, ups, bad.carry); !errors.Is(err, ErrBadManifest) {
			t.Fatalf("carry %q (%s): %v, want a bad manifest", bad.carry, bad.why, err)
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

// A failure of the machine is not a bad manifest: a node that does not hold the
// parent cannot say what it has, and a staged file it cannot read cannot be
// added. A host answers those with an error of its own, not as a 400.
func TestRebuildFailuresOfTheMachineAreNotBadManifests(t *testing.T) {
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
	a, b := start(), start()
	parent, err := a.AddDir(ctx, writeTree(t, map[string]string{"index.html": "home", "assets/site.css": "css"}))
	if err != nil {
		t.Fatal(err)
	}
	// b was never given the parent's blocks, and waits for them until its
	// context ends: even an offline node does
	short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if _, err := b.Rebuild(short, parent, writeTree(t, map[string]string{"index.html": "home 2"}), []string{"assets"}); err == nil || errors.Is(err, ErrBadManifest) {
		t.Fatalf("without the parent's blocks: %v, want a failure that is not a bad manifest", err)
	}
	t.Run("a staged file that cannot be read", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("the file stays readable")
		}
		ups := writeTree(t, map[string]string{"zz.txt": "zz"})
		if err := os.Chmod(filepath.Join(ups, "zz.txt"), 0); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Rebuild(ctx, parent, ups, []string{"assets"}); err == nil || errors.Is(err, ErrBadManifest) {
			t.Fatalf("with an unreadable upload: %v, want a failure that is not a bad manifest", err)
		}
	})
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

// A node that holds only a version's folder blocks can still rebuild the next
// version, sharded folders included: the carried files are linked, never
// fetched, and nothing is stored under their CIDs (a sharded folder adds each
// child to its DAG before linking it, which stored an empty block there).
func TestRebuildFromFolderBlocksAloneStoresNothingForCarriedFiles(t *testing.T) {
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
	a, b := start(), start()
	name := func(i int) string { return fmt.Sprintf("post-%04d-%s.html", i, strings.Repeat("a", 200)) }
	before, after := map[string]string{}, map[string]string{}
	for i := 0; i < 1500; i++ {
		before[name(i)] = fmt.Sprint(i)
		after[name(i)] = fmt.Sprint(i)
	}
	after[name(7)] = "changed"
	parent, err := a.AddDir(ctx, writeTree(t, before))
	if err != nil {
		t.Fatal(err)
	}
	pb, _ := a.Block(ctx, parent)
	nd, _ := merkledag.DecodeProtobuf(pb)
	if fsn, err := ft.FSNodeFromBytes(nd.Data()); err != nil || fsn.Type() != ft.THAMTShard {
		t.Fatal("the folder is not sharded; raise the count or the name length")
	}
	want, err := a.AddDir(ctx, writeTree(t, after))
	if err != nil {
		t.Fatal(err)
	}
	carried, err := a.Links(ctx, parent) // name -> CID, of what b is never given
	if err != nil {
		t.Fatal(err)
	}
	delete(carried, name(7))
	blocks, err := a.DirBlocks(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	for c, data := range blocks {
		if err := b.PutBlock(ctx, c, data); err != nil {
			t.Fatal(err)
		}
	}
	var carry []string
	for n := range carried {
		carry = append(carry, n)
	}
	got, err := b.Rebuild(ctx, parent, writeTree(t, map[string]string{name(7): "changed"}), carry)
	if err != nil || got != want {
		t.Fatalf("rebuild from the folder blocks gave %s, %v; adding the whole tree gives %s", got, err, want)
	}
	for _, c := range carried {
		if data, err := b.Block(ctx, c); err == nil {
			t.Fatalf("b stored %d bytes under %s, the CID of a carried file it was never given", len(data), c)
		}
	}
	// and b can list the version from those folders, with the sizes the links hold
	files, err := b.Files(ctx, got)
	if err != nil || len(files) != len(after) {
		t.Fatalf("b lists %d files, want %d: %v", len(files), len(after), err)
	}
	for _, f := range files {
		if f.Path == name(7) && f.Size != int64(len("changed")) {
			t.Fatalf("the changed file is %d bytes", f.Size)
		}
	}
}

// Files and OpenFile read a version from its blocks whatever shape its files
// have: a small one, an empty one, and one chunked into several blocks, which
// reads back whole and seeks.
func TestFilesAndOpenFileReadChunkedAndEmptyFiles(t *testing.T) {
	ctx := context.Background()
	e := NewEmbedded(t.TempDir())
	e.Offline = true
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	big := make([]byte, 3*chunkSize+1234) // its root is a dag-pb node over raw leaves
	rand.Read(big)
	dir := writeTree(t, map[string]string{"index.html": "home", "empty": "", "media/clip.bin": string(big)})
	root, err := e.AddDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []VersionFile{{"empty", 0, ""}, {"index.html", 4, ""}, {"media/clip.bin", int64(len(big)), ""}}
	files, err := e.Files(ctx, root)
	if err != nil || len(files) != len(want) {
		t.Fatalf("files: %v, %v", files, err)
	}
	for i, w := range want {
		own, err := e.FileCID(ctx, filepath.Join(dir, filepath.FromSlash(w.Path)))
		if err != nil || files[i].Path != w.Path || files[i].Size != w.Size || files[i].CID != own {
			t.Fatalf("file %d is %+v, want %s of %d bytes with CID %s (%v)", i, files[i], w.Path, w.Size, own, err)
		}
	}
	r, size, err := e.OpenFile(ctx, root, "media/clip.bin")
	if err != nil || size != int64(len(big)) {
		t.Fatalf("open: %d bytes, %v", size, err)
	}
	if _, err := r.Seek(chunkSize+10, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	part := make([]byte, 100)
	if _, err := io.ReadFull(r, part); err != nil || !bytes.Equal(part, big[chunkSize+10:chunkSize+110]) {
		t.Fatalf("read after a seek across a chunk: %v", err)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if all, err := io.ReadAll(r); err != nil || !bytes.Equal(all, big) {
		t.Fatalf("read back %d bytes of %d: %v", len(all), len(big), err)
	}
	r.Close()
	r, size, err = e.OpenFile(ctx, root, "empty")
	if err != nil || size != 0 {
		t.Fatalf("open empty: %d, %v", size, err)
	}
	if all, _ := io.ReadAll(r); len(all) != 0 {
		t.Fatalf("empty file read back %d bytes", len(all))
	}
	r.Close()
	if _, _, err := e.OpenFile(ctx, root, "nope.html"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing file gave %v", err)
	}
	for _, bad := range []string{"media", "index.html/x", "../x", ""} {
		if _, _, err := e.OpenFile(ctx, root, bad); err == nil {
			t.Fatalf("OpenFile %q must fail", bad)
		}
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
	e.AnnounceRecord(ctx, "s", c, rec) // stored locally first; offline there is no peer to send it to
	got, err := e.GetRecord(ctx, name)
	if err != nil || string(got) != string(rec) {
		t.Fatalf("the network holds another record: %v", err)
	}
}
