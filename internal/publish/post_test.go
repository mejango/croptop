package publish

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

func offlineNode(t *testing.T) *ipfs.Embedded {
	t.Helper()
	e := ipfs.NewEmbedded(t.TempDir())
	e.Offline = true
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Stop() })
	return e
}

func TestPreparePostRestoresNameAfterBindingSourceHost(t *testing.T) {
	f := newPreparedFixture(t)
	entry, err := hostEntry(f.ctx, f.hostURL, f.ipns)
	if err != nil {
		t.Fatal(err)
	}
	entry.Name = "source-name"
	// A published site can name a previous endpoint. Binding the source host
	// must happen before restoring the name returned by that source's registry.
	target, err := url.Parse(f.hostURL)
	if err != nil {
		t.Fatal(err)
	}
	source := httptest.NewServer(httputil.NewSingleHostReverseProxy(target))
	t.Cleanup(source.Close)
	prepared, err := f.service.preparePost(f.ctx, f.service.Node.(postEngine), source.URL, f.ipns, NewPost{Title: "New post"}, t.TempDir(), entry)
	if err != nil {
		t.Fatal(err)
	}
	if got := HostOf(prepared.site); got != source.URL {
		t.Errorf("prepared host = %q, want %q", got, source.URL)
	}
	if got := NameOf(prepared.site); got != entry.Name {
		t.Errorf("source host's restored name = %q, want %q", got, entry.Name)
	}
}

// A machine with only the key and an empty data directory adds posts to a
// site a laptop published to a host: the new version keeps every old file,
// and the post's page shows the site's navigation.
func TestPostWithOnlyTheKey(t *testing.T) {
	ctx := context.Background()
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	// CROPTOP_E2E_HOST runs this against another host, such as the Worker under
	// `wrangler dev --local --var SIGNING_HOST:127.0.0.1 --var NODE:`
	var host http.Handler = h
	if u := os.Getenv("CROPTOP_E2E_HOST"); u != "" {
		target, _ := url.Parse(u)
		host = httputil.NewSingleHostReverseProxy(target)
	}
	var mu sync.Mutex
	var asked []string // paths the host was asked for
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		host.ServeHTTP(w, r)
	}))
	defer srv.Close()
	get := func(p string) (int, string) {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// the laptop: the fixture site, an old post with a photo, two navigation links
	laptop := offlineNode(t)
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	ipnsName, err := laptop.Keystore().Generate(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	site, _ := s.Site(fixtureID)
	site.IPNS = ipnsName
	enableHosting(t, site)
	SetHost(site, srv.URL)
	s.SaveSite(site)
	photo := filepath.Join(t.TempDir(), "old.png")
	os.WriteFile(photo, []byte("old photo bytes"), 0o644)
	old, err := addPost(s, fixtureID, NewPost{Title: "Old post", Files: []string{photo}})
	if err != nil {
		t.Fatal(err)
	}
	yes, one, two, slug, ext := true, 1, 2, "about", "https://example.com/"
	s.SavePost(fixtureID, &store.Post{ID: "PAGE", ArticleType: 1, Title: "About & us", Slug: &slug, Link: "/about/", IsIncludedInNavigation: &yes, NavigationWeight: &one})
	s.SavePost(fixtureID, &store.Post{ID: "LINK", ArticleType: 1, Title: "Elsewhere", ExternalLink: &ext, Link: "/LINK/", IsIncludedInNavigation: &yes, NavigationWeight: &two})
	lp := &Publisher{Store: s, Node: laptop, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: laptop}}
	if err := lp.Render.Render(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	base, err := laptop.AddDir(ctx, s.PublicDir(fixtureID))
	if err != nil {
		t.Fatal(err)
	}
	laptop.SignRecord(fixtureID, base, 5)
	if err := lp.Push(ctx, site, base, 5); err != nil {
		t.Fatal(err)
	}
	pem, _ := laptop.Keystore().ExportPEM(fixtureID)

	// the agent: a fresh node and data directory, nothing but the key
	agent := offlineNode(t)
	as := &store.Store{Root: t.TempDir()}
	ap := &Publisher{Store: as, Node: agent, Render: &render.Renderer{Store: as, Templates: templates.FS, CIDs: agent}}
	shot := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(shot, []byte("new photo bytes"), 0o644)
	first, err := ap.Post(ctx, srv.URL, pem, NewPost{Title: "Did a thing", Content: "and **posted** it", Tags: "agent", Files: []string{shot}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 6 || first.CID == base {
		t.Fatalf("first post: %+v", first)
	}
	second, err := ap.Post(ctx, srv.URL, pem, NewPost{Title: "Did another"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Sequence != 7 {
		t.Fatalf("second post: %+v", second)
	}
	if _, b := get("/v0/host/keys/" + ipnsName); !strings.Contains(b, second.CID) {
		t.Fatalf("host holds %s", b)
	}
	v := "/ipfs/" + second.CID + "/"
	_, planet := get(v + "planet.json")
	for _, want := range []string{"Did another", "Did a thing", "Old post"} {
		if !strings.Contains(planet, want) {
			t.Errorf("planet.json lacks %q", want)
		}
	}
	if _, b := get(v + old.ID + "/old.png"); b != "old photo bytes" {
		t.Errorf("old attachment: %q", b)
	}
	if _, b := get(v + "rss.xml"); !strings.Contains(b, "Did a thing") || !strings.Contains(b, "Old post") {
		t.Errorf("rss.xml lacks a post")
	}
	if code, _ := get(v + "agent.html"); code != 200 {
		t.Errorf("no page for the new tag: %d", code)
	}
	_, baseIndex := get("/ipfs/" + base + "/index.html")
	if _, b := get(v + "index.html"); b != baseIndex {
		t.Error("index.html changed; only the new post, planet.json, rss.xml, and tag pages may")
	}
	id := first.URL[strings.LastIndex(strings.TrimSuffix(first.URL, "/"), "/")+1 : len(first.URL)-1]
	_, page := get(v + id + "/")
	for _, want := range []string{`href="../about/"`, ">About & us</a>", `href="https://example.com/"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the new post's page lacks %s", want)
		}
	}
	if _, b := get(v + id + "/shot.png"); b != "new photo bytes" {
		t.Errorf("new attachment: %q", b)
	}
	if keys, _ := os.ReadDir(agent.Keystore().Dir); len(keys) != 0 {
		t.Errorf("the key stayed in the agent's keystore: %v", keys)
	}

	// the laptop takes the agent's posts in within a minute, downloading only
	// them, and a post deleted here meanwhile stays deleted
	site, _ = s.Site(fixtureID)
	site.LastPublishedCID, site.IPNSSequence = &base, 5
	s.SaveSite(site)
	if err := s.DeletePost(fixtureID, "6610815C-71AE-4E29-9DEB-CD069682D63D"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	asked = nil
	mu.Unlock()
	if err := lp.CatchUp(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	if site, _ = s.Site(fixtureID); site.PublishedElsewhere || *site.LastPublishedCID != second.CID || site.IPNSSequence != 7 {
		t.Errorf("not caught up: elsewhere %v, cid %s, sequence %d", site.PublishedElsewhere, *site.LastPublishedCID, site.IPNSSequence)
	}
	titles := map[string]bool{}
	posts, _ := s.Posts(fixtureID)
	for _, p := range posts {
		titles[p.Title] = true
	}
	if !titles["Did a thing"] || !titles["Did another"] || !titles["Old post"] {
		t.Errorf("posts after catching up: %v", titles)
	}
	if _, err := s.Post(fixtureID, "6610815C-71AE-4E29-9DEB-CD069682D63D"); err == nil {
		t.Error("a post deleted here came back")
	}
	if b, _ := os.ReadFile(filepath.Join(s.PostDir(fixtureID, id), "shot.png")); string(b) != "new photo bytes" {
		t.Errorf("the agent's attachment was not taken in: %q", b)
	}
	if _, err := os.Stat(filepath.Join(s.PublicDir(fixtureID), id, "index.html")); err != nil {
		t.Error("the laptop's copy was not rendered with the agent's post")
	}
	mu.Lock()
	fromHost := false
	for _, p := range asked {
		fromHost = fromHost || strings.HasSuffix(p, "/shot.png")
		if strings.HasSuffix(p, "/old.png") {
			t.Errorf("downloaded a file this machine already has: %s", p)
		}
	}
	mu.Unlock()
	if !fromHost {
		t.Error("the agent's attachment did not come from the host")
	}
}

// A host from before posts on a parent would take the post for the whole site.
func TestPostRefusesAnOldHost(t *testing.T) {
	pushed := false
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pushed = pushed || r.Method == "POST"
		w.Write([]byte(`{"cid":"bafyold","sequence":3}`))
	}))
	defer old.Close()
	node := offlineNode(t)
	node.Keystore().Generate("k")
	pem, _ := node.Keystore().ExportPEM("k")
	ap := &Publisher{Store: &store.Store{Root: t.TempDir()}, Node: offlineNode(t)}
	if _, err := ap.Post(context.Background(), old.URL, pem, NewPost{Title: "x"}); err == nil || !strings.Contains(err.Error(), "needs updating") || pushed {
		t.Fatalf("err %v, pushed %v", err, pushed)
	}
}
