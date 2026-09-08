package render

import (
	"context"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

const fixtureID = "FF5F456D-904F-4EE6-8BB5-AD175C65319A"

type fakeCIDs struct{}

func (fakeCIDs) FileCIDv0(ctx context.Context, p string) (string, error) {
	return "QmFAKE" + filepath.Base(p), nil
}

// kuboCIDs uses the downloaded kubo when present (no daemon needed for --only-hash).
func kuboCIDs(t *testing.T) CIDer {
	bin := filepath.Join("..", "..", ".data", "kubo", "ipfs")
	repo := filepath.Join("..", "..", ".data", "ipfs")
	if _, err := os.Stat(bin); err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(repo, "config")); err != nil {
		return nil
	}
	return ipfs.NewNode(bin, repo)
}

func fixtureRenderer(t *testing.T) (*Renderer, *store.Store) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, "sites", fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	s := &store.Store{Root: root}
	return &Renderer{Store: s, Templates: templates.FS, CIDs: fakeCIDs{}}, s
}

func TestRenderFixtureProducesTemplateContract(t *testing.T) {
	r, s := fixtureRenderer(t)
	if err := r.Render(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	pub := s.PublicDir(fixtureID)
	for _, f := range []string{"index.html", "page1.html", "planet.json", "templateSettings.json", "robots.txt", "assets/style.css", "mockups.html", "logo.html"} {
		if _, err := os.Stat(filepath.Join(pub, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	var pj struct {
		Articles []map[string]any `json:"articles"`
	}
	b, _ := os.ReadFile(filepath.Join(pub, "planet.json"))
	if err := json.Unmarshal(b, &pj); err != nil {
		t.Fatal(err)
	}
	if len(pj.Articles) != 18 {
		t.Fatalf("planet.json has %d articles", len(pj.Articles))
	}
	id := pj.Articles[0]["id"].(string)
	for _, f := range []string{"index.html", "simple.html", "article.json", "article.md", "nft.json", "nft.json.cid.txt"} {
		if _, err := os.Stat(filepath.Join(pub, id, f)); err != nil {
			t.Fatalf("missing %s/%s", id, f)
		}
	}
	html, _ := os.ReadFile(filepath.Join(pub, id, "index.html"))
	if !strings.Contains(string(html), "../assets/") {
		t.Fatal("assets_prefix not applied on post page")
	}
	if strings.Contains(string(html), "{{") || strings.Contains(string(html), "{%") {
		t.Fatal("unrendered template syntax in post page")
	}
	idx, _ := os.ReadFile(filepath.Join(pub, "index.html"))
	if !strings.Contains(string(idx), `custom-tag-mockups`) {
		t.Fatal("index did not render planet.tags")
	}
}

// The Mac app's published planet.json is the contract the template JS reads.
// Our articles must match it field for field (except contentRendered, whose
// whitespace differs between cmark and goldmark, and Apple epoch floats).
func TestArticlesMatchMacOutput(t *testing.T) {
	r, s := fixtureRenderer(t)
	if err := r.Render(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	var mac, ours struct {
		Articles []map[string]any `json:"articles"`
	}
	b, _ := os.ReadFile("testdata/public-planet.json")
	json.Unmarshal(b, &mac)
	b, _ = os.ReadFile(filepath.Join(s.PublicDir(fixtureID), "planet.json"))
	json.Unmarshal(b, &ours)
	if len(mac.Articles) != len(ours.Articles) {
		t.Fatalf("count %d vs %d", len(mac.Articles), len(ours.Articles))
	}
	for i := range mac.Articles {
		m, o := mac.Articles[i], ours.Articles[i]
		if m["id"] != o["id"] {
			t.Fatalf("order differs at %d: mac %s ours %s", i, m["id"], o["id"])
		}
		for k, mv := range m {
			if k == "contentRendered" {
				continue
			}
			if !reflect.DeepEqual(mv, o[k]) {
				t.Errorf("%s.%s: mac %v ours %v", m["id"], k, mv, o[k])
			}
		}
		for k := range o {
			if _, ok := m[k]; !ok {
				t.Errorf("%s.%s: extra key in ours", m["id"], k)
			}
		}
	}
}

func TestSwiftJSONReproducesMacNFTBytes(t *testing.T) {
	want, _ := os.ReadFile("testdata/public-post/nft.json")
	var v any
	dec := json.NewDecoder(strings.NewReader(string(want)))
	dec.UseNumber()
	dec.Decode(&v)
	got, err := SwiftJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("bytes differ\n--- want\n%s\n--- got\n%s", want, got)
	}
	if c := kuboCIDs(t); c != nil {
		p := filepath.Join(t.TempDir(), "nft.json")
		os.WriteFile(p, got, 0o644)
		cid, err := c.FileCIDv0(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		wantCID, _ := os.ReadFile("testdata/public-post/nft.json.cid.txt")
		if cid != strings.TrimSpace(string(wantCID)) {
			t.Fatalf("cid %s want %s", cid, wantCID)
		}
	} else {
		t.Log("kubo not downloaded; skipped CID check")
	}
}

// Regenerating nft.json for the imported fixture post must reproduce the
// Mac app's bytes, since new posts go through the same path.
func TestNFTRegenerationMatchesMac(t *testing.T) {
	c := kuboCIDs(t)
	if c == nil {
		t.Skip("kubo not downloaded")
	}
	r, s := fixtureRenderer(t)
	r.CIDs = c
	const post = "0AC3B2B6-90BE-4D1B-B14F-3A549D7A9953"
	// the attachment file is not in the fixture, so seed the CID Planet computed
	if err := r.Render(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(s.PublicDir(fixtureID), post, "nft.json"))
	want, _ := os.ReadFile("testdata/public-post/nft.json")
	if string(got) != string(want) {
		t.Fatalf("nft.json differs\n--- want\n%s\n--- got\n%s", want, got)
	}
	gotCID, _ := os.ReadFile(filepath.Join(s.PublicDir(fixtureID), post, "nft.json.cid.txt"))
	wantCID, _ := os.ReadFile("testdata/public-post/nft.json.cid.txt")
	if strings.TrimSpace(string(gotCID)) != strings.TrimSpace(string(wantCID)) {
		t.Fatalf("cid %s want %s", gotCID, wantCID)
	}
}

func TestCoverImage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "_cover.png")
	if err := WriteCover(p, "hello world, this is a fairly long line that needs wrapping to fit inside the box"); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(p)
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil || img.Bounds().Dx() != 512 || img.Bounds().Dy() != 512 {
		t.Fatalf("bad cover: %v %v", err, img.Bounds())
	}
}

func TestShims(t *testing.T) {
	in := `{% if item.externalLink.count > 0 %}{% if x != nil %}{% if y == true %}{{ user_settings['maintenanceMessage'] }}{% include './post-page.html' %}{% extends 'base.html' %}`
	want := `{% if item.externalLink|length > 0 %}{% if x %}{% if y %}{{ user_settings.maintenanceMessage }}{% include "./post-page.html" %}{% extends "base.html" %}`
	if got := applyShims(in); got != want {
		t.Fatalf("\ngot  %s\nwant %s", got, want)
	}
}
