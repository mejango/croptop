package render

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/gateway"
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
	// This fixture was published by Planet using eth.sucks. Keep that explicit
	// so the comparison tests the original output independently of new defaults.
	site, err := s.Site(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	gateway.Set(site, "sucks")
	if err := s.SaveSite(site); err != nil {
		t.Fatal(err)
	}
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
	in := `{% if item.externalLink.count > 0 %}{% if x != nil %}{% if y == true %}{{ user_settings['maintenanceMessage'] }}{% if user_settings['maintenanceMessage'].count > 0 %}{% include './post-page.html' %}{% extends 'base.html' %}{{ article.hasVideo }}{% if article.hasVideo %}{{ planet.plausibleEnabled }}`
	want := `{% if item.externalLink|length > 0 %}{% if x %}{% if y %}{{ user_settings.maintenanceMessage }}{% if user_settings.maintenanceMessage|length > 0 %}{% include "./post-page.html" %}{% extends "base.html" %}{{ article.hasVideo|lower }}{% if article.hasVideo %}{{ planet.plausibleEnabled|lower }}`
	if got := applyShims(in); got != want {
		t.Fatalf("\ngot  %s\nwant %s", got, want)
	}
}

func TestPagesRenderButStayOutOfFeed(t *testing.T) {
	r, s := fixtureRenderer(t)
	slug := "about"
	page := &store.Post{ID: "AAAAAAAA-0000-4000-8000-000000000001", Title: "About", Content: "hi", Created: store.Now(), ArticleType: 1, Link: "/about/", Slug: &slug, Attachments: []string{}}
	if err := s.SavePost(fixtureID, page); err != nil {
		t.Fatal(err)
	}
	if err := r.Render(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	var pj struct {
		Articles []map[string]any `json:"articles"`
	}
	b, _ := os.ReadFile(filepath.Join(s.PublicDir(fixtureID), "planet.json"))
	json.Unmarshal(b, &pj)
	for _, a := range pj.Articles {
		if a["id"] == page.ID {
			t.Fatal("page leaked into planet.json articles")
		}
	}
	for _, dir := range []string{page.ID, "about"} {
		if _, err := os.Stat(filepath.Join(s.PublicDir(fixtureID), dir, "index.html")); err != nil {
			t.Fatalf("page not rendered at %s", dir)
		}
	}
}

func TestRSSMatchesPlanetShape(t *testing.T) {
	r, s := fixtureRenderer(t)
	if err := r.Render(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.PublicDir(fixtureID), "rss.xml"))
	if err != nil {
		t.Fatal("rss.xml missing")
	}
	var feed struct {
		Channel struct {
			Title string `xml:"title"`
			Link  string `xml:"link"`
			Items []struct {
				Link        string `xml:"link"`
				PubDate     string `xml:"pubDate"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(b, &feed); err != nil {
		t.Fatalf("rss.xml is not valid XML: %v\n%s", err, b[:400])
	}
	if feed.Channel.Title != "CocoPay 🥥" || len(feed.Channel.Items) != 18 {
		t.Fatalf("title %q items %d", feed.Channel.Title, len(feed.Channel.Items))
	}
	site, _ := s.Site(fixtureID)
	if feed.Channel.Link != RootPrefix(site)+"/" || feed.Channel.Link != "https://cocopay.eth.shop/" { // Planet uses a non-.eth domain as written
		t.Fatalf("channel link %s", feed.Channel.Link)
	}
	it := feed.Channel.Items[0]
	if !strings.HasPrefix(it.Link, feed.Channel.Link) || !strings.Contains(it.PubDate, "2025") {
		t.Fatalf("item %+v", it)
	}
	if !strings.Contains(it.Description, `src="https://`) {
		t.Fatalf("image not absolute: %s", it.Description)
	}
}

// Booleans dropped into the page's JavaScript must be lowercase, or the
// post page dies with "False is not defined" before it renders anything.
func TestPostPageHasNoPythonBooleans(t *testing.T) {
	r, s := fixtureRenderer(t)
	if err := r.Render(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	posts, _ := s.Posts(fixtureID)
	html, _ := os.ReadFile(filepath.Join(s.PublicDir(fixtureID), posts[0].ID, "index.html"))
	for _, bad := range []string{"= False", "= True", "(False", "(True", " False;", " True;"} {
		if strings.Contains(string(html), bad) {
			t.Fatalf("post page contains a Python-style boolean %q", bad)
		}
	}
	if !strings.Contains(string(html), "false") {
		t.Fatal("expected a lowercase boolean in the post page script")
	}
}

// Tags come from other machines too, so a tag must not be able to write
// outside the site, replace its home page, or break out of its tag page.
func TestHostileTagsGetNoPage(t *testing.T) {
	r, s := fixtureRenderer(t)
	hostile := []string{"../outside", "a/b", "Index", "page1", "x'y", "x\"y", "<b>", "ok"}
	tags := map[string]string{}
	for _, k := range hostile {
		tags[k] = k
	}
	empty := ""
	if err := s.SavePost(fixtureID, &store.Post{ID: "TAGGED", Title: "tagged", Link: "/TAGGED/", Attachments: []string{}, Tags: tags, Summary: &empty}); err != nil {
		t.Fatal(err)
	}
	if err := r.Render(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	pub := s.PublicDir(fixtureID)
	if _, err := os.Stat(filepath.Join(pub, "..", "outside.html")); err == nil {
		t.Error("a tag wrote outside the site's folder")
	}
	if b, _ := os.ReadFile(filepath.Join(pub, "index.html")); strings.Contains(string(b), "tag_key") || strings.Contains(string(b), "'Index' in article.tags") {
		t.Error("a tag replaced the home page")
	}
	if _, err := os.Stat(filepath.Join(pub, "ok.html")); err != nil {
		t.Error("an ordinary tag lost its page")
	}
	entries, _ := os.ReadDir(pub)
	for _, e := range entries {
		if strings.ContainsAny(e.Name(), `'"<`) {
			t.Errorf("unsafe tag page %s", e.Name())
		}
	}
}

// Two renders of the same site give the same bytes whatever the clock, RSS
// dates do not depend on the machine's time zone, and an attachment already
// in place is not copied again.
func TestRendersAreStable(t *testing.T) {
	r, s := fixtureRenderer(t)
	ctx := context.Background()
	zone := time.Local
	t.Cleanup(func() { time.Local = zone })
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(s.PublicDir(fixtureID), name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	posts, err := s.Posts(fixtureID)
	if err != nil || len(posts) == 0 {
		t.Fatal("fixture has no posts", err)
	}
	p := posts[0]
	src := filepath.Join(s.PostDir(fixtureID, p.ID), "big.bin")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	p.Attachments = append(p.Attachments, "big.bin")
	if err := s.SavePost(fixtureID, p); err != nil {
		t.Fatal(err)
	}

	time.Local = time.FixedZone("BRT", -3*3600)
	if err := r.Render(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	index, rss := read("index.html"), read("rss.xml")
	dst := filepath.Join(s.PublicDir(fixtureID), p.ID, "big.bin")
	si, _ := os.Stat(src)
	di, err := os.Stat(dst)
	if err != nil || !di.ModTime().Equal(si.ModTime()) {
		t.Fatalf("the copy of an attachment must keep its modification time: %v", err)
	}
	if err := os.Chmod(dst, 0o444); err != nil { // a second copy would fail to open it for writing
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dst, 0o644) })

	time.Sleep(1100 * time.Millisecond) // the old build_timestamp was the second of the render
	if err := r.Render(ctx, fixtureID); err != nil {
		t.Fatalf("an attachment already in place was copied again: %v", err)
	}
	if !bytes.Equal(index, read("index.html")) {
		t.Fatal("index.html changed between two renders of the same site")
	}
	time.Local = time.FixedZone("JST", 9*3600)
	if err := r.Render(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rss, read("rss.xml")) {
		t.Fatal("rss.xml changed with the machine's time zone")
	}
	if strings.Contains(string(rss), "-0300") {
		t.Fatal("rss.xml dates are not in UTC")
	}
}
