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
