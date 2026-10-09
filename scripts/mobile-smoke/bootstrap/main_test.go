package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/store"
)

func TestLivePublicationRequiresBothExplicitFlags(t *testing.T) {
	for _, o := range []options{{publish: true}, {publish: true, host: "http://crop.top"}, {publish: true, host: "https://other.example"}, {host: "https://other.example"}} {
		if err := run(context.Background(), o, &bytes.Buffer{}); err == nil {
			t.Fatalf("unsafe arguments accepted: %+v", o)
		}
	}
}

func TestPrepareIsIsolatedAndRerunsKeepIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fixture")
	var first, second bytes.Buffer
	if err := run(context.Background(), options{dir: dir}, &first); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), options{dir: dir}, &second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("rerun changed the fixture identity")
	}
	var got state
	if err := json.Unmarshal(first.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.IPNS == "" || got.InitialCID == "" || got.PublishedAt != "" || len(got.TemplateDigest) != 64 {
		t.Fatalf("unexpected safe preparation: %+v", got)
	}
	if strings.Contains(first.String(), "PRIVATE KEY") {
		t.Fatal("output contains a private key")
	}
	info, err := os.Stat(got.KeyFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private key permissions: %v, %v", info, err)
	}
	st := &store.Store{Root: filepath.Join(dir, "data")}
	sites, err := st.Sites()
	if err != nil || len(sites) != 1 || sites[0].ID != got.SiteID || !sites[0].HostingEnabled() {
		t.Fatalf("fixture must contain exactly its new hosted site: %v, %v", sites, err)
	}
	posts, err := st.Posts(got.SiteID)
	if err != nil || len(posts) != 0 {
		t.Fatal("bootstrap must not copy or create any posts")
	}
}

func TestRefuseExistingUnrelatedDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing"), []byte("user file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), options{dir: dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted unrelated directory")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "existing" {
		t.Fatal("changed unrelated directory")
	}
}
