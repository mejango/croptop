package collaboration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

const owner = "k51qzi5uqu5djdyf7zp3ds34teqww8zkq6ii8n85gs86elk3n100k39rbtvc7y"
const author = "k51qzi5uqu5dlm2dzx1irz57ujwo3r11kifvxzf5l9z8g97u3f3ppx0540awwt"
const siteID = "11111111-1111-1111-1111-111111111111"
const postID = "22222222-2222-2222-2222-222222222222"

type node struct {
	ipfs.Engine
	root     string
	fail     bool
	seen     string
	revision string
}

func (n *node) NetworkRecord(context.Context, string) (*ipfs.Record, error) {
	if n.fail {
		return nil, errors.New("offline")
	}
	return &ipfs.Record{Value: "/ipfs/verified-root" + n.revision}, nil
}
func (n *node) Get(_ context.Context, p, d string) error {
	n.seen = p
	return os.CopyFS(d, os.DirFS(n.root))
}
func write(t *testing.T, p string, v any) {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(p, b, 0644); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) (*Manager, *node, render.PublicPost) {
	t.Helper()
	st := &store.Store{Root: t.TempDir()}
	if e := st.SaveSite(&store.Site{ID: siteID, Name: "Together", IPNS: owner}); e != nil {
		t.Fatal(e)
	}
	n := &node{root: t.TempDir()}
	m := &Manager{Store: st, Node: n}
	if e := m.AddSources(siteID, []store.Contributor{{IPNS: author, Name: "Author", Mode: "all"}}); e != nil {
		t.Fatal(e)
	}
	p := render.PublicPost{ID: postID, Title: "A contribution", Content: "Full original text", Created: store.Now(), Attachments: []string{"photo.png"}, SubmissionTargets: []string{owner}}
	source(t, n, p)
	return m, n, p
}
func source(t *testing.T, n *node, p render.PublicPost) {
	write(t, filepath.Join(n.root, "planet.json"), map[string]any{"name": "Author", "articles": []render.PublicPost{p}})
	write(t, filepath.Join(n.root, p.ID, "article.json"), p)
	if e := os.WriteFile(filepath.Join(n.root, p.ID, "photo.png"), []byte("image bytes"), 0644); e != nil {
		t.Fatal(e)
	}
}
func refresh(t *testing.T, m *Manager) State {
	t.Helper()
	s, e := m.Sync(context.Background(), siteID)
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Errors) > 0 {
		t.Fatal(s.Errors)
	}
	return s
}
func TestAutomaticMergeMirrorsEditsAndDeletions(t *testing.T) {
	m, n, p := fixture(t)
	p.SubmissionTargets = nil
	source(t, n, p)
	refresh(t, m)
	posts, e := m.Store.Posts(siteID)
	if e != nil || len(posts) != 1 {
		t.Fatalf("posts=%d %v", len(posts), e)
	}
	id := posts[0].ID
	modified := *posts[0].Modified
	if posts[0].OriginalSiteDomain != author || posts[0].OriginalPostID != postID {
		t.Fatal("authorship lost")
	}
	refresh(t, m)
	again, _ := m.Store.Post(siteID, id)
	if *again.Modified != modified {
		t.Fatal("unchanged source invalidates previews")
	}
	p.Content = "Updated at the source"
	source(t, n, p)
	n.revision = "-2"
	refresh(t, m)
	again, _ = m.Store.Post(siteID, id)
	if again.Content != p.Content {
		t.Fatal("source edit not merged")
	}
	write(t, filepath.Join(n.root, "planet.json"), map[string]any{"name": "Author", "articles": []render.PublicPost{}})
	n.revision = "-3"
	refresh(t, m)
	posts, _ = m.Store.Posts(siteID)
	if len(posts) != 0 {
		t.Fatal("deleted source post remains")
	}
	site, _ := m.Store.Site(siteID)
	if site.LastPublished != nil {
		t.Fatal("local merge published the site")
	}
}
func TestLegacySourcesAndExistingPostsKeepIDs(t *testing.T) {
	m, _, _ := fixture(t)
	s, _ := m.Store.Site(siteID)
	s.Contributors = []store.Contributor{}
	s.Aggregation = []string{author}
	m.Store.SaveSite(s)
	m.Store.SavePost(siteID, &store.Post{ID: postID, OriginalSiteDomain: author, OriginalPostID: postID})
	st := refresh(t, m)
	posts, _ := m.Store.Posts(siteID)
	if len(st.Contributors) != 1 || len(posts) != 1 || posts[0].ID != postID {
		t.Fatal("legacy composite duplicated")
	}
}
func TestFailedSourceRetainsPreviousCopies(t *testing.T) {
	m, n, _ := fixture(t)
	refresh(t, m)
	n.fail = true
	st, e := m.Sync(context.Background(), siteID)
	posts, _ := m.Store.Posts(siteID)
	if e != nil || len(posts) != 1 || !strings.Contains(st.Errors[author], "offline") {
		t.Fatal("failed source lost previous posts")
	}
}
func TestSourceValidationPrecedesChanges(t *testing.T) {
	for _, name := range []string{"../escape.png", "/tmp/escape.png", "..\\escape.png", "article.json"} {
		t.Run(name, func(t *testing.T) {
			m, n, p := fixture(t)
			refresh(t, m)
			p.Attachments = []string{name}
			p.Content = "unsafe changed post"
			source(t, n, p)
			n.revision = "-2"
			st, e := m.Sync(context.Background(), siteID)
			posts, _ := m.Store.Posts(siteID)
			if e != nil || len(st.Errors) != 1 || len(posts) != 1 || posts[0].Content == p.Content {
				t.Fatal("unsafe source modified site")
			}
		})
	}
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(external, []byte("secret"), 0600)
	os.Symlink(external, filepath.Join(root, "photo.png"))
	if _, e := readFile(root, "photo.png", 100); e == nil {
		t.Fatal("symlink accepted")
	}
}
func TestConcurrentMergeDoesNotDuplicate(t *testing.T) {
	m, _, _ := fixture(t)
	results := make(chan error, 8)
	for n := 0; n < 8; n++ {
		go func() { _, e := m.Sync(context.Background(), siteID); results <- e }()
	}
	for n := 0; n < 8; n++ {
		if e := <-results; e != nil {
			t.Fatal(e)
		}
	}
	posts, _ := m.Store.Posts(siteID)
	if len(posts) != 1 {
		t.Fatal("duplicate merged posts")
	}
}
func TestPreviouslyDismissedPostsAreStillMerged(t *testing.T) {
	m, n, p := fixture(t)
	p.SubmissionTargets = nil
	source(t, n, p)
	write(t, filepath.Join(m.dir(siteID), "review.json"), map[string]any{"items": []any{map[string]any{"status": "dismissed"}}})
	refresh(t, m)
	posts, _ := m.Store.Posts(siteID)
	if len(posts) != 1 {
		t.Fatal("old review state blocked merging")
	}
}
func TestRepeatedOriginIsDeduplicated(t *testing.T) {
	m, n, p := fixture(t)
	p.OriginalSiteDomain = author
	p.OriginalSiteName = "Author"
	p.OriginalPostID = postID
	source(t, n, p)
	second := author + "a"
	if e := m.AddSources(siteID, []store.Contributor{{IPNS: second, Name: "Also shares Author"}}); e != nil {
		t.Fatal(e)
	}
	refresh(t, m)
	posts, _ := m.Store.Posts(siteID)
	if len(posts) != 1 {
		t.Fatal("same origin duplicated across sources")
	}
}
