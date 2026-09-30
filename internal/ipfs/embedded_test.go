package ipfs

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
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
