package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/store"
)

const sharedID = "11111111-1111-1111-1111-111111111111"
const writerID = "33333333-3333-3333-3333-333333333333"
const contributionID = "22222222-2222-2222-2222-222222222222"
const sharedName = "k51qzi5uqu5djdyf7zp3ds34teqww8zkq6ii8n85gs86elk3n100k39rbtvc7y"
const writerName = "k51qzi5uqu5dlm2dzx1irz57ujwo3r11kifvxzf5l9z8g97u3f3ppx0540awwt"

type collaborationNode struct {
	ipfs.Engine
	roots   map[string]string
	records map[string]string
}

func (n *collaborationNode) NetworkRecord(_ context.Context, name string) (*ipfs.Record, error) {
	cid, ok := n.records[name]
	if !ok {
		return nil, errors.New("not published")
	}
	return &ipfs.Record{Value: "/ipfs/" + cid}, nil
}
func (n *collaborationNode) Get(_ context.Context, path, dest string) error {
	root, ok := n.roots[strings.TrimPrefix(path, "/ipfs/")]
	if !ok {
		return errors.New("unknown root")
	}
	return os.CopyFS(dest, os.DirFS(root))
}
func (n *collaborationNode) Provide(context.Context, string) error { return nil }
func collaborationServer(t *testing.T) (*Server, *httptest.Server, *collaborationNode) {
	t.Helper()
	s, old := testServer(t)
	old.Close()
	n := &collaborationNode{Engine: s.Node, roots: map[string]string{}, records: map[string]string{}}
	s.Node = n
	s.Follow.Engine = n
	for _, site := range []*store.Site{{ID: sharedID, IPNS: sharedName, Name: "Field Notes", About: "A shared journal of places, people, and things worth noticing.", TemplateName: "Croptop", Created: store.Now()}, {ID: writerID, IPNS: writerName, Name: "Maya", About: "Walking, looking, collecting.", TemplateName: "Croptop", Created: store.Now()}} {
		if e := s.Store.SaveSite(site); e != nil {
			t.Fatal(e)
		}
		if e := s.Pub.Render.Render(context.Background(), site.ID); e != nil {
			t.Fatal(e)
		}
	}
	p := &store.Post{ID: contributionID, Title: "A quieter way home", Content: "The long way home took us past an empty garden.\n\n**A small discovery:** the things we notice change when we slow down.\n\nWhat have you noticed on your usual walk?", Created: store.Now(), Link: "/" + contributionID + "/", Attachments: []string{"photo.png"}}
	if e := s.Store.SavePost(writerID, p); e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(s.Store.PostDir(writerID, p.ID), 0755)
	os.WriteFile(filepath.Join(s.Store.PostDir(writerID, p.ID), "photo.png"), pngBytes(), 0644)
	if e := s.Pub.Render.Render(context.Background(), writerID); e != nil {
		t.Fatal(e)
	}
	n.roots["writer-1"] = s.Store.PublicDir(writerID)
	n.records[writerName] = "writer-1"
	n.roots["shared-1"] = s.Store.PublicDir(sharedID)
	n.records[sharedName] = "shared-1"
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts, n
}
func TestCompositeWorkflow(t *testing.T) {
	s, ts, n := collaborationServer(t)
	base := "/v0/croptop/sites/" + sharedID + "/collaboration"
	code, out := do(t, "POST", ts.URL+base+"/sources", strings.NewReader(`{"names":["`+writerName+`"]}`), "application/json")
	if code != 200 {
		t.Fatalf("add sources %d %v", code, out)
	}
	posts, _ := s.Store.Posts(sharedID)
	if len(posts) != 1 || posts[0].OriginalSiteDomain != writerName {
		t.Fatal("source did not merge automatically")
	}
	source, _ := s.Store.Post(writerID, contributionID)
	source.Content = "Source update"
	s.Store.SavePost(writerID, source)
	s.Pub.Render.Render(context.Background(), writerID)
	n.roots["writer-2"] = s.Store.PublicDir(writerID)
	n.records[writerName] = "writer-2"
	code, out = do(t, "POST", ts.URL+base+"/refresh", strings.NewReader(`{}`), "application/json")
	if code != 200 {
		t.Fatal(out)
	}
	posts, _ = s.Store.Posts(sharedID)
	if posts[0].Content != "Source update" {
		t.Fatal("source edit not merged")
	}
	for _, suffix := range []string{"/contributors", "/review/item"} {
		code, _ := do(t, "POST", ts.URL+base+suffix, strings.NewReader(`{}`), "application/json")
		if code != 410 {
			t.Fatal("obsolete review/invitation route still active")
		}
	}
	s.Cfg.SetPasscode("test-only")
	code, _ = do(t, "GET", ts.URL+base, nil, "")
	if code != 401 {
		t.Fatal("source settings lack console auth")
	}
}
func TestCurateCreationResolvesSourcesBeforeCreatingSite(t *testing.T) {
	s, ts, _ := collaborationServer(t)
	before, _ := s.Store.Sites()
	body, kind := multipartBody(t, map[string]string{"name": "New composite", "sources": "not-a-site"}, nil)
	code, _ := do(t, "POST", ts.URL+"/v0/planets/my", body, kind)
	after, _ := s.Store.Sites()
	if code != 400 || len(before) != len(after) {
		t.Fatal("invalid sources created an empty site")
	}
	body, kind = multipartBody(t, map[string]string{"name": "New composite", "sources": writerName}, nil)
	code, out := do(t, "POST", ts.URL+"/v0/planets/my", body, kind)
	if code != 200 {
		t.Fatal(out)
	}
	posts, _ := s.Store.Posts(out["id"].(string))
	if len(posts) != 1 {
		t.Fatal("Curate did not create a merged site")
	}
}
