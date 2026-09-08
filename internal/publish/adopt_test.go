package publish

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

type fakeCIDs struct{}

func (fakeCIDs) FileCIDv0(ctx context.Context, p string) (string, error) {
	return "QmFAKE" + filepath.Base(p), nil
}

func TestRebuildSourceFromPublic(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, "sites", fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	st := &store.Store{Root: root}
	const post = "0AC3B2B6-90BE-4D1B-B14F-3A549D7A9953"
	os.MkdirAll(st.PostDir(fixtureID, post), 0o755)
	os.WriteFile(filepath.Join(st.PostDir(fixtureID, post), "Screenshot 2025-11-01 at 01.28.05.png"), []byte("png"), 0o644)
	r := &render.Renderer{Store: st, Templates: templates.FS, CIDs: fakeCIDs{}}
	if err := r.Render(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	origSite, _ := st.Site(fixtureID)
	origPosts, _ := st.Posts(fixtureID)

	rebuilt := &store.Store{Root: t.TempDir()}
	if err := rebuildSource(rebuilt, fixtureID, st.PublicDir(fixtureID)); err != nil {
		t.Fatal(err)
	}
	site, err := rebuilt.Site(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if site.Name != origSite.Name || site.IPNS != origSite.IPNS || site.TemplateName != "Croptop" {
		t.Fatalf("site mismatch: %+v", site)
	}
	posts, _ := rebuilt.Posts(fixtureID)
	if len(posts) != len(origPosts) {
		t.Fatalf("%d posts, want %d", len(posts), len(origPosts))
	}
	byID := map[string]*store.Post{}
	for _, p := range posts {
		byID[p.ID] = p
	}
	for _, o := range origPosts {
		p := byID[o.ID]
		if p == nil {
			t.Fatalf("missing post %s", o.ID)
		}
		if p.Title != o.Title || p.Content != o.Content || len(p.Attachments) != len(o.Attachments) || p.Created != o.Created {
			t.Fatalf("post %s differs:\n%+v\n%+v", o.ID, p, o)
		}
		if len(p.Tags) != len(o.Tags) {
			t.Fatalf("tags differ for %s", o.ID)
		}
	}
	if _, err := os.Stat(filepath.Join(rebuilt.PostDir(fixtureID, post), "Screenshot 2025-11-01 at 01.28.05.png")); err != nil {
		t.Fatal("attachment not restored")
	}
	ops, _ := rebuilt.Ops(fixtureID)
	if _, ok := ops[post+"-nft-adopted"]; !ok {
		t.Fatal("nft op not seeded")
	}
	if _, err := os.Stat(filepath.Join(rebuilt.SiteDir(fixtureID), "templateSettings.json")); err != nil {
		t.Fatal("templateSettings.json not restored")
	}
}

func TestMergePosts(t *testing.T) {
	at := func(v float64) *store.AppleTime { a := store.AppleTime(v); return &a }
	local := []*store.Post{
		{ID: "a", Title: "a-local", Created: 10, Modified: at(20)},
		{ID: "b", Title: "b-local", Created: 10, Modified: at(50)},
		{ID: "c", Title: "c-local-only", Created: 10},
	}
	remote := []*store.Post{
		{ID: "a", Title: "a-remote", Created: 10, Modified: at(30)},
		{ID: "b", Title: "b-remote", Created: 10, Modified: at(40)},
		{ID: "d", Title: "d-remote-only", Created: 10},
	}
	merged, changed, added, updated := mergePosts(local, remote)
	if added != 1 || updated != 1 || len(merged) != 4 || len(changed) != 2 {
		t.Fatalf("added %d updated %d merged %d changed %d", added, updated, len(merged), len(changed))
	}
	titles := map[string]string{}
	for _, p := range merged {
		titles[p.ID] = p.Title
	}
	if titles["a"] != "a-remote" || titles["b"] != "b-local" || titles["c"] != "c-local-only" || titles["d"] != "d-remote-only" {
		t.Fatalf("bad merge: %v", titles)
	}
}
