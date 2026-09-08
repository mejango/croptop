package ipfs

import (
	"context"
	"crypto/rand"
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
