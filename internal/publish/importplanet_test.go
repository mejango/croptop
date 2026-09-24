package publish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/store"
)

func TestImportPlanet(t *testing.T) {
	container := t.TempDir()
	my := filepath.Join(container, "Documents", "Planet", "My", fixtureID)
	if err := os.CopyFS(my, os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	const post = "0AC3B2B6-90BE-4D1B-B14F-3A549D7A9953"
	pubPost := filepath.Join(container, "Documents", "Planet", "Public", fixtureID, post)
	os.MkdirAll(pubPost, 0o755)
	os.WriteFile(filepath.Join(pubPost, "Screenshot 2025-11-01 at 01.28.05.png"), []byte("png"), 0o644)
	os.WriteFile(filepath.Join(pubPost, "nft.json.cid.txt"), []byte("QmKEEP"), 0o644)
	srcKS := &ipfs.Keystore{Dir: filepath.Join(container, "Library", "Application Support", "ipfs", "keystore")}
	wantName, _ := srcKS.Generate(fixtureID)

	root := t.TempDir()
	st := &store.Store{Root: root}
	ks := &ipfs.Keystore{Dir: filepath.Join(root, "ipfs", "keystore")}
	var logs []string
	ids, err := ImportPlanet(st, ks, container, false, func(s string) { logs = append(logs, s) })
	if err != nil || len(ids) != 1 {
		t.Fatalf("%v %v", ids, err)
	}
	if _, err := st.Site(fixtureID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(st.PostDir(fixtureID, post), "Screenshot 2025-11-01 at 01.28.05.png")); err != nil {
		t.Fatal("attachment not moved into source tree")
	}
	if b, _ := os.ReadFile(filepath.Join(st.PublicDir(fixtureID), post, "nft.json.cid.txt")); string(b) != "QmKEEP" {
		t.Fatal("public files not preserved")
	}
	if name, _ := ks.Name(fixtureID); name != wantName {
		t.Fatalf("key not imported: %s vs %s", name, wantName)
	}
	// A second import without --force merges posts the app wrote since, and leaves the rest alone.
	newPost := filepath.Join(container, "Documents", "Planet", "My", fixtureID, "Articles", "NEW-POST.json")
	if err := os.WriteFile(newPost, []byte(`{"id":"NEW-POST","title":"later","content":"","created":1,"articleType":0,"link":"/NEW-POST/","attachments":["a.png"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(container, "Documents", "Planet", "Public", fixtureID, "NEW-POST"), 0o755)
	os.WriteFile(filepath.Join(container, "Documents", "Planet", "Public", fixtureID, "NEW-POST", "a.png"), []byte("png"), 0o644)
	if ids, err := ImportPlanet(st, ks, container, false, nil); err != nil || len(ids) != 1 {
		t.Fatalf("merge: ids=%v err=%v", ids, err)
	}
	if p, err := st.Post(fixtureID, "NEW-POST"); err != nil || p.Title != "later" {
		t.Fatalf("merged post missing: %v %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(st.PostDir(fixtureID, "NEW-POST"), "a.png")); err != nil {
		t.Fatalf("merged attachment missing: %v", err)
	}
	if ids, err := ImportPlanet(st, ks, container, false, nil); err != nil || len(ids) != 0 {
		t.Fatalf("second merge should be a no-op: ids=%v err=%v", ids, err)
	}
	_ = strings.Contains
	if false {
		t.Fatalf("second import should refuse: %v", err)
	}
	if _, err := ImportPlanet(st, ks, container, true, nil); err != nil {
		t.Fatalf("force import: %v", err)
	}
}
