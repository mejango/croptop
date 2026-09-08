package tpl

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

const fixtureID = "FF5F456D-904F-4EE6-8BB5-AD175C65319A"

func resolver(t *testing.T, eng ipfs.Engine) (*Resolver, *store.Store) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, "sites", fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	st := &store.Store{Root: root}
	return &Resolver{DataDir: root, Store: st, Default: templates.FS, Engine: eng}, st
}

func TestResolutionForkReset(t *testing.T) {
	r, st := resolver(t, nil)
	site, _ := st.Site(fixtureID)
	if _, src, _ := r.For(site); src != "default" {
		t.Fatalf("source %s", src)
	}
	if err := r.WriteFile(site, "assets/style.css", []byte("x")); err == nil {
		t.Fatal("editing without a fork must fail")
	}
	if err := r.Fork(fixtureID); err != nil {
		t.Fatal(err)
	}
	site, _ = st.Site(fixtureID)
	if _, src, _ := r.For(site); src != "fork" {
		t.Fatalf("source after fork %s", src)
	}
	files, _ := r.Files(site)
	if len(files) == 0 || files[0] == "" || !Editable(files[0]) {
		t.Fatalf("files %v", files)
	}
	css, _ := r.ReadFile(site, "assets/style.css")
	if err := r.WriteFile(site, "assets/style.css", append(css, []byte("\n/* mine */\n")...)); err != nil {
		t.Fatal(err)
	}
	back, _ := r.ReadFile(site, "assets/style.css")
	if !strings.Contains(string(back), "/* mine */") {
		t.Fatal("edit not stored")
	}
	if err := r.WriteFile(site, "../escape.css", []byte("x")); err == nil {
		t.Fatal("path escape must fail")
	}
	if err := r.WriteFile(site, "assets/CapsulesVF.woff2", []byte("x")); err == nil {
		t.Fatal("binary must not be editable")
	}
	up, err := r.Upstream(context.Background(), site)
	if err != nil || len(up.Edited) != 1 || up.Edited[0] != "assets/style.css" {
		t.Fatalf("upstream %+v %v", up, err)
	}
	if err := r.Reset(fixtureID); err != nil {
		t.Fatal(err)
	}
	site, _ = st.Site(fixtureID)
	if _, src, _ := r.For(site); src != "default" {
		t.Fatalf("source after reset %s", src)
	}
}

func TestValidateRejectsBrokenTemplate(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "template.json"), []byte(`{"name":"x"}`), 0o644)
	if err := Validate(os.DirFS(dir)); err == nil {
		t.Fatal("no index.html should fail")
	}
	if err := Validate(templates.FS); err != nil {
		t.Fatalf("default template should validate: %v", err)
	}
}

// Publish then install through the offline embedded node round-trips a template.
func TestPublishInstallRoundTrip(t *testing.T) {
	e := ipfs.NewEmbedded(t.TempDir())
	e.Offline = true
	ctx := context.Background()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	r, st := resolver(t, e)
	src := filepath.Join(t.TempDir(), "mytemplate")
	if err := os.CopyFS(src, templates.FS); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(src, "assets", "style.css"), []byte("body{color:red}"), 0o644)
	cid, err := r.Publish(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(r.InstalledDir(cid)) // forget the local copy; install must fetch
	info, err := r.Install(ctx, cid)
	if err != nil {
		t.Fatal(err)
	}
	if info.CID != cid || info.Name != "Croptop" {
		t.Fatalf("info %+v", info)
	}
	if err := r.Use(fixtureID, cid); err != nil {
		t.Fatal(err)
	}
	site, _ := st.Site(fixtureID)
	fsys, src2, _ := r.For(site)
	if src2 != "installed:"+cid {
		t.Fatalf("source %s", src2)
	}
	if b, _ := fs.ReadFile(fsys, "assets/style.css"); string(b) != "body{color:red}" {
		t.Fatalf("installed css %q", b)
	}
	list, _ := r.Installed()
	if len(list) != 2 || !list[0].Default || list[1].CID != cid || len(list[1].Sites) != 1 {
		t.Fatalf("installed %+v", list)
	}
}
