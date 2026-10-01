package publish

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

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

// testCtx ends after 30 seconds, so a call that waits for a block this node
// lacks fails its test instead of hanging it, as context.Background() would.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// The changes since the host's version are the files to upload and the
// largest unchanged files and folders to carry. Deleted ones are in neither.
// The result checks out against the new version even on a machine that has
// only the new version, reading the parent's folders from the host.
func TestChangesSinceTheHostsVersion(t *testing.T) {
	ctx := testCtx(t)
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
	if _, err := h.Engine.AddDir(ctx, writeSite(t, old)); err != nil { // the host holds the parent's blocks
		t.Fatal(err)
	}
	other := offlineNode(t)
	root2, err := other.AddDir(ctx, writeSite(t, now))
	if err != nil || root2 != root {
		t.Fatal(err)
	}
	if _, err := other.Block(ctx, parent); err == nil { // the premise: it has to read the parent's folders from the host
		t.Fatal("the other machine holds the parent already")
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

// A version in which no file is the parent's carries nothing. Its carry list is
// empty but not nil: that makes it a manifest push, which drops what is not
// uploaded (the deleted file), where a nil list would be a plain push on top of
// the parent and keep it.
func TestChangesWithNothingToCarryStillCarriesAList(t *testing.T) {
	ctx := testCtx(t)
	srv := httptest.NewServer(http.NotFoundHandler()) // holds no blocks: this node has the parent's folders
	defer srv.Close()
	old := map[string]string{"index.html": "home", "assets/site.css": "css", "p1/index.html": "one", "p1/photo.jpg": "photo", "notes.txt": "deleted"}
	now := map[string]string{"index.html": "home 2", "assets/site.css": "css 2", "p1/index.html": "one 2", "p1/photo.jpg": "photo 2"}
	wantUp := []string{"assets/site.css", "index.html", "p1/index.html", "p1/photo.jpg"}

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
	if err != nil || !reflect.DeepEqual(up, wantUp) {
		t.Fatalf("changes: upload %v carry %v, %v", up, carry, err)
	}
	if carry == nil || len(carry) != 0 {
		t.Fatalf("carry is %#v, want an empty list that is not nil", carry)
	}
	if err := laptop.CheckManifest(ctx, root, parent, up, carry); err != nil {
		t.Fatalf("check: %v", err)
	}
}

// A version that only deletes files has nothing to upload, and a push is
// committed with the last file sent: changes says so, and the version goes up
// whole. The very version the host holds has nothing to send either, but no
// push is wanted then: that is not an error.
func TestChangesWithNothingToUploadIsAnError(t *testing.T) {
	ctx := testCtx(t)
	srv := httptest.NewServer(http.NotFoundHandler()) // holds no blocks: this node has the parent's folders
	defer srv.Close()
	old := map[string]string{"index.html": "home", "assets/site.css": "css", "p1/index.html": "one", "p1/photo.jpg": "photo"}
	now := map[string]string{"index.html": "home", "assets/site.css": "css", "p1/index.html": "one"}

	laptop := offlineNode(t)
	parent, err := laptop.AddDir(ctx, writeSite(t, old))
	if err != nil {
		t.Fatal(err)
	}
	root, err := laptop.AddDir(ctx, writeSite(t, now))
	if err != nil {
		t.Fatal(err)
	}
	if root == parent {
		t.Fatal("the versions should differ")
	}
	p := &Publisher{Node: laptop}
	if up, carry, err := p.changes(ctx, laptop, srv.URL, root, parent); !errors.Is(err, errNothingToUpload) {
		t.Fatalf("changes: upload %v carry %v, %v", up, carry, err)
	}
	up, carry, err := p.changes(ctx, laptop, srv.URL, root, root)
	if err != nil || len(up) != 0 || !reflect.DeepEqual(carry, []string{"assets", "index.html", "p1"}) {
		t.Fatalf("changes against itself: upload %v carry %v, %v", up, carry, err)
	}
}

// A machine that holds only part of a sharded folder of the parent, such as one
// that took the parent in by pulling just the posts that changed, cannot
// compare that folder. The host gives the folder's first block, and the rest
// is waited for on the network; when the wait ends, what was read is not the
// folder's listing, and a diff made from it is not the folder's changes. So the
// folder is uploaded whole, or, when it is the top folder, there is no diff:
// never a part of one.
func TestChangesNeverPartiallyComparesAShardedParent(t *testing.T) {
	saved := ipfsFetchTimeout
	ipfsFetchTimeout = time.Second // how long the blocks this machine lacks are waited for
	t.Cleanup(func() { ipfsFetchTimeout = saved })
	for _, dir := range []string{"posts/", ""} {
		t.Run("sharded folder "+dir, func(t *testing.T) {
			ctx := testCtx(t)
			h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
			if err := h.Start(); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(h)
			defer srv.Close()
			// 1500 long names are too many for one block, so the folder is sharded
			name := func(i int) string { return fmt.Sprintf("%spost-%04d-%s.html", dir, i, strings.Repeat("a", 200)) }
			old, now := map[string]string{"index.html": "home"}, map[string]string{"index.html": "home 2"}
			for i := 0; i < 1500; i++ {
				old[name(i)], now[name(i)] = fmt.Sprint(i), fmt.Sprint(i)
			}
			now[name(7)] = "changed"
			parent, err := h.Engine.AddDir(ctx, writeSite(t, old)) // the host holds the parent
			if err != nil {
				t.Fatal(err)
			}
			other := offlineNode(t) // this machine has only the new version
			root, err := other.AddDir(ctx, writeSite(t, now))
			if err != nil {
				t.Fatal(err)
			}

			// the premises: the parent's folder is sharded, and its first block is not here
			folder := parent
			if dir != "" {
				links, err := h.Engine.Links(ctx, parent)
				if err != nil {
					t.Fatal(err)
				}
				folder = links["posts"]
			}
			if blocks, err := h.Engine.DirBlocks(ctx, folder); err != nil || len(blocks) < 2 {
				t.Fatalf("the parent's folder is not sharded: %d blocks, %v", len(blocks), err)
			}
			if _, err := other.Block(ctx, folder); err == nil {
				t.Fatal("this machine holds the parent's folder already")
			}

			var logs []string
			p := &Publisher{Node: other, Log: func(s string) { logs = append(logs, s) }}
			up, carry, err := p.changes(ctx, other, srv.URL, root, parent)
			if dir == "" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("changes with a top folder it cannot read: upload %d carry %d, %v", len(up), len(carry), err)
				}
				return
			}
			if err != nil || len(up) != len(now) || carry == nil || len(carry) != 0 {
				t.Fatalf("changes: upload %d of %d files, carry %d (nil %v), %v", len(up), len(now), len(carry), carry == nil, err)
			}
			if err := other.CheckManifest(ctx, root, parent, up, carry); err != nil {
				t.Fatalf("check: %v", err)
			}
			logged := false
			for _, l := range logs {
				logged = logged || strings.Contains(l, "posts")
			}
			if !logged {
				t.Errorf("the folder uploaded whole was not logged: %q", logs)
			}
		})
	}
}

// A file that became a folder has nothing to compare: the old entry is a raw
// block, never a folder, so the host is not asked for it. A Worker host refuses
// a raw block, and a machine that lacks it would wait for it over IPFS.
func TestChangesDoesNotAskForTheBlockOfAFileThatBecameAFolder(t *testing.T) {
	ctx := testCtx(t)
	old := map[string]string{"index.html": "home", "notes.txt": "a file that becomes a folder"}
	now := map[string]string{"index.html": "home", "notes.txt/index.html": "now a folder", "notes.txt/more.html": "with more"}
	laptop := offlineNode(t)
	parent, err := laptop.AddDir(ctx, writeSite(t, old))
	if err != nil {
		t.Fatal(err)
	}
	root, err := laptop.AddDir(ctx, writeSite(t, now))
	if err != nil {
		t.Fatal(err)
	}
	links, err := laptop.Links(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	file := links["notes.txt"]

	var mu sync.Mutex
	var asked []string // what the host was asked for
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := &Publisher{Node: laptop}
	up, carry, err := p.changes(ctx, laptop, srv.URL, root, parent)
	if err != nil || !reflect.DeepEqual(up, []string{"notes.txt/index.html", "notes.txt/more.html"}) || !reflect.DeepEqual(carry, []string{"index.html"}) {
		t.Fatalf("changes: upload %v carry %v, %v", up, carry, err)
	}
	mu.Lock()
	defer mu.Unlock()
	sawParent := false
	for _, path := range asked {
		sawParent = sawParent || path == "/v0/host/blocks/"+parent
		if strings.HasSuffix(path, file) {
			t.Errorf("the host was asked for %s, the file that became a folder", path)
		}
	}
	if !sawParent { // the stand-in does record what it is asked
		t.Errorf("the host was not asked for the parent's folder: %q", asked)
	}
}

// A context that ends in the middle of the walk gives no diff: the folders that
// could not be read because of it would be uploaded whole, and what is left
// would look like a diff.
func TestChangesOnAnEndedContextIsAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(testCtx(t))
	defer cancel()
	old := map[string]string{"index.html": "home", "assets/site.css": "css", "p1/index.html": "one", "p1/photo.jpg": "photo"}
	now := map[string]string{"index.html": "home 2", "assets/site.css": "css", "p1/index.html": "one 2", "p1/photo.jpg": "photo"}
	laptop := offlineNode(t)
	parent, err := laptop.AddDir(ctx, writeSite(t, old))
	if err != nil {
		t.Fatal(err)
	}
	root, err := laptop.AddDir(ctx, writeSite(t, now))
	if err != nil {
		t.Fatal(err)
	}
	links, err := laptop.Links(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	nested := links["p1"] // the folder the walk goes into after the top one
	// the host's answer for that folder is a 404 that comes as the context ends
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, nested) {
			cancel()
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := &Publisher{Node: laptop}
	up, carry, err := p.changes(ctx, laptop, srv.URL, root, parent)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("changes after the context ended: upload %v carry %v, %v", up, carry, err)
	}
}
