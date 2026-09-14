package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/follow"
	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

func TestPlainText(t *testing.T) {
	in := "# Title\n\nSome **bold** text with a [link](https://x.y) and <b>html</b>.<script type=\"module\">alert(1)</script>\n\n`code`"
	got := plainText(in, 240)
	want := "Title Some bold text with a link and html. code"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	long := plainText(strings.Repeat("word ", 100), 40)
	if len(long) > 45 || !strings.HasSuffix(long, "…") {
		t.Fatalf("truncation: %q", long)
	}
}

func TestHasPreview(t *testing.T) {
	if !hasPreview(render.PublicPost{Attachments: []string{"preview.js"}}) {
		t.Fatal("attachment")
	}
	if !hasPreview(render.PublicPost{Content: `<script type="croptop/preview">export default () => {}</script>`}) {
		t.Fatal("inline")
	}
	if hasPreview(render.PublicPost{Content: "<script type=\"module\"></script>"}) {
		t.Fatal("module is not a preview")
	}
}

// These requests use only saved data. With no IPFS engine configured, any
// attempt to refresh a followed site would fail instead of reaching the network.
func feedTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	root := t.TempDir()
	s := &Server{Store: &store.Store{Root: root}, Follow: &follow.Store{Root: root}}
	mux := http.NewServeMux()
	s.routesFollow(mux)
	return s, mux
}

func saveFeedFollowing(t *testing.T, s *Server, entry follow.Entry, name string, posts ...render.PublicPost) {
	t.Helper()
	dir := s.Follow.SiteDir(entry.IPNS)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for filename, value := range map[string]any{
		filepath.Join(filepath.Dir(dir), "follow.json"): entry,
		filepath.Join(dir, "planet.json"):               map[string]any{"name": name, "articles": posts},
	} {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type feedTestItem struct {
	SiteID      string          `json:"siteID"`
	IPNS        string          `json:"ipns"`
	Site        string          `json:"site"`
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Content     string          `json:"content"`
	Attachments []string        `json:"attachments"`
	Link        string          `json:"link"`
	URL         string          `json:"url"`
	Created     store.AppleTime `json:"created"`
	Preview     bool            `json:"preview"`
	Pinned      bool            `json:"pinned"`
	Hero        string          `json:"hero"`
}

func getFeed(t *testing.T, handler http.Handler, query string) []feedTestItem {
	t.Helper()
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v0/croptop/feed"+query, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET feed%s: %d %s", query, rr.Code, rr.Body.String())
	}
	var items []feedTestItem
	if err := json.Unmarshal(rr.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode feed: %v; body %s", err, rr.Body.String())
	}
	if items == nil {
		t.Fatal("feed must return an array, not null")
	}
	for _, item := range items {
		if item.Attachments == nil {
			t.Errorf("%s/%s: attachments must be an array, not missing or null", item.IPNS, item.ID)
		}
	}
	return items
}

func TestFeedOwnedAggregatesOnlyActiveSites(t *testing.T) {
	s, handler := feedTestServer(t)
	archived, active := true, false
	a := &store.Site{ID: "owned-a", IPNS: "k51owned-a", Name: "First site"}
	b := &store.Site{ID: "owned-b", IPNS: "k51owned-b", Name: "Second site", Archived: &active}
	if err := gateway.SetCustomDomain(b, "journal.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, site := range []*store.Site{a, b, {ID: "archived", Archived: &archived}, {ID: "empty"}} {
		if err := s.Store.SaveSite(site); err != nil {
			t.Fatal(err)
		}
	}
	pin := store.AppleTime(100)
	content := "# Full entry\n\n" + strings.Repeat("A paragraph with **formatting**. ", 30) + "\n\nThe ending survives."
	for _, fixture := range []struct {
		siteID string
		post   store.Post
	}{
		{a.ID, store.Post{ID: "shared-post", Title: " Older ", Content: content, Created: 10, Pinned: &pin, Attachments: []string{"portrait.jpg", "voice.mp3", "preview.js"}}},
		{a.ID, store.Post{ID: "middle", Content: "Middle entry", Created: 20}},
		{b.ID, store.Post{ID: "shared-post", Content: "Second site's copy", Created: 30}},
		{b.ID, store.Post{ID: "page", ArticleType: 1, Created: 40}},
		{"archived", store.Post{ID: "hidden", Created: 50}},
	} {
		if err := s.Store.SavePost(fixture.siteID, &fixture.post); err != nil {
			t.Fatal(err)
		}
	}
	saveFeedFollowing(t, s, follow.Entry{IPNS: "k51followed"}, "Followed site", render.PublicPost{ID: "remote", Created: 60})

	items := getFeed(t, handler, "?source=owned")
	if len(items) != 3 {
		t.Fatalf("owned feed should contain three posts across active sites: %+v", items)
	}
	for i, want := range []struct {
		site *store.Site
		id   string
		date store.AppleTime
	}{{b, "shared-post", 30}, {a, "middle", 20}, {a, "shared-post", 10}} {
		got := items[i]
		if got.SiteID != want.site.ID || got.IPNS != want.site.IPNS || got.Site != want.site.Name || got.ID != want.id || got.Created != want.date {
			t.Errorf("item %d identity/order: %+v", i, got)
		}
		if got.Link != "/"+want.site.ID+"/"+want.id+"/" || got.URL != gateway.URL(want.site)+want.id+"/" {
			t.Errorf("item %d links: local %q public %q", i, got.Link, got.URL)
		}
	}
	oldest := items[2]
	if oldest.Content != content || !reflect.DeepEqual(oldest.Attachments, []string{"portrait.jpg", "voice.mp3", "preview.js"}) {
		t.Fatalf("full content or attachments changed: %+v", oldest)
	}
	if oldest.Title != "Older" || !oldest.Pinned || !oldest.Preview || oldest.Hero != "portrait.jpg" {
		t.Errorf("existing card metadata was lost: %+v", oldest)
	}
	limited := getFeed(t, handler, "?source=owned&limit=1")
	if len(limited) != 1 || limited[0].SiteID != b.ID || limited[0].ID != "shared-post" {
		t.Fatalf("owned limit should follow aggregation and sorting: %+v", limited)
	}
	if zero := getFeed(t, handler, "?source=owned&offset=0&limit=1"); !reflect.DeepEqual(zero, limited) {
		t.Fatalf("explicit zero offset differs from default: %+v", zero)
	}
	for offset, want := range items {
		page := getFeed(t, handler, "?source=owned&offset="+strconv.Itoa(offset)+"&limit=1")
		if len(page) != 1 || !reflect.DeepEqual(page[0], want) {
			t.Fatalf("owned offset %d should follow aggregation and sorting: %+v", offset, page)
		}
	}
	if page := getFeed(t, handler, "?source=owned&offset=3&limit=1"); len(page) != 0 {
		t.Fatalf("owned offset at the end should return an empty array: %+v", page)
	}
	defaultItems := getFeed(t, handler, "")
	if len(defaultItems) != 1 || defaultItems[0].ID != "remote" || defaultItems[0].SiteID != "" {
		t.Fatalf("default feed must include only followed posts: %+v", defaultItems)
	}
}

func TestFeedFollowingFiltersBeforeLimitAndPreservesContent(t *testing.T) {
	s, handler := feedTestServer(t)
	selected := follow.Entry{IPNS: "k51selected", Name: "selected.eth", Title: "Saved title", CID: "saved-cid", Checked: 12, Changed: 8}
	content := "# A full post\n\n" + strings.Repeat("Keep **all** of this text. ", 30) + "\n\nFinal paragraph."
	pin := store.AppleTime(100)
	saveFeedFollowing(t, s, selected, "", render.PublicPost{ID: "older", Content: "Old text", Created: 10, Pinned: &pin}, render.PublicPost{ID: "selected-newest", Content: content, Created: 20, Attachments: []string{"first.png", "second.png", "sound.mp3"}}, render.PublicPost{ID: "selected-page", ArticleType: 1, Created: 100})
	saveFeedFollowing(t, s, follow.Entry{IPNS: "k51other", Title: "Other entry"}, "Published title", render.PublicPost{ID: "other-newest", Created: 30})
	entryPath := filepath.Join(filepath.Dir(s.Follow.SiteDir(selected.IPNS)), "follow.json")
	before, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatal(err)
	}

	all := getFeed(t, handler, "")
	if len(all) != 3 || all[0].ID != "other-newest" || all[1].ID != "selected-newest" || all[2].ID != "older" {
		t.Fatalf("following feed order/pages: %+v", all)
	}
	if all[0].Site != "Published title" {
		t.Errorf("published site name missing: %+v", all[0])
	}
	if explicit := getFeed(t, handler, "?source=following"); !reflect.DeepEqual(explicit, all) {
		t.Fatalf("explicit following differs from default: %+v", explicit)
	}
	for _, query := range []string{"?ipns=k51selected&limit=1", "?source=following&ipns=k51selected&limit=1"} {
		filtered := getFeed(t, handler, query)
		if len(filtered) != 1 || filtered[0].ID != "selected-newest" {
			t.Fatalf("selection must happen before the global limit: %+v", filtered)
		}
		got := filtered[0]
		if got.SiteID != "" || got.IPNS != selected.IPNS || got.Site != selected.Title {
			t.Errorf("selected site identity: %+v", got)
		}
		if got.Link != "/f/k51selected/selected-newest/" || got.URL != "https://selected."+gateway.Get("crop.top").Domain+"/selected-newest/" {
			t.Errorf("followed links changed: %+v", got)
		}
		if got.Content != content || !reflect.DeepEqual(got.Attachments, []string{"first.png", "second.png", "sound.mp3"}) {
			t.Errorf("full content or attachment order changed: %+v", got)
		}
	}
	for _, tc := range []struct{ query, id string }{
		{"?ipns=k51selected&offset=0&limit=1", "selected-newest"},
		{"?ipns=k51selected&offset=1&limit=1", "older"},
		{"?source=following&ipns=k51selected&offset=1&limit=1", "older"},
		{"?source=following&offset=1&limit=1", "selected-newest"},
	} {
		page := getFeed(t, handler, tc.query)
		if len(page) != 1 || page[0].ID != tc.id {
			t.Fatalf("offset must follow scope filtering and newest-first sorting: query %s, got %+v", tc.query, page)
		}
	}
	for _, query := range []string{"?ipns=k51selected&offset=2&limit=1", "?ipns=k51selected&offset=100", "?offset=100"} {
		if page := getFeed(t, handler, query); len(page) != 0 {
			t.Fatalf("offset at or past the end should return an empty array: query %s, got %+v", query, page)
		}
	}
	after, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("reading the feed changed the saved follow entry")
	}
}

func TestFeedSelectionErrorsAndUnavailableCaches(t *testing.T) {
	s, handler := feedTestServer(t)
	saveFeedFollowing(t, s, follow.Entry{IPNS: "k51good"}, "Good", render.PublicPost{ID: "available"})
	for _, fixture := range []struct{ ipns, data string }{
		{"k51missing", ""},
		{"k51malformed", "{broken"},
		{"k51null", "null"},
		{"k51wrongshape", `{"articles":{}}`},
	} {
		saveFeedFollowing(t, s, follow.Entry{IPNS: fixture.ipns}, "Unavailable")
		filename := filepath.Join(s.Follow.SiteDir(fixture.ipns), "planet.json")
		if fixture.data == "" {
			if err := os.Remove(filename); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(filename, []byte(fixture.data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, query string
		status      int
	}{
		{"unknown source", "?source=other", http.StatusBadRequest},
		{"owned with selection", "?source=owned&ipns=k51good", http.StatusBadRequest},
		{"unknown followed site", "?ipns=k51unknown", http.StatusNotFound},
		{"negative offset", "?offset=-1", http.StatusBadRequest},
		{"negative owned offset", "?source=owned&offset=-1", http.StatusBadRequest},
		{"fractional offset", "?offset=1.5", http.StatusBadRequest},
		{"nonnumeric offset", "?ipns=k51good&offset=invalid", http.StatusBadRequest},
		{"missing cache", "?ipns=k51missing", http.StatusServiceUnavailable},
		{"malformed cache", "?source=following&ipns=k51malformed", http.StatusServiceUnavailable},
		{"null cache", "?ipns=k51null", http.StatusServiceUnavailable},
		{"wrong cache shape", "?ipns=k51wrongshape", http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v0/croptop/feed"+tc.query, nil))
			if rr.Code != tc.status {
				t.Fatalf("status %d, want %d; %s", rr.Code, tc.status, rr.Body.String())
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || strings.TrimSpace(body.Error) == "" {
				t.Fatalf("expected a readable JSON error: %s", rr.Body.String())
			}
			if tc.status == http.StatusServiceUnavailable && !strings.Contains(strings.ToLower(body.Error), "refresh") {
				t.Errorf("unavailable cache error should explain how to recover: %q", body.Error)
			}
		})
	}
	all := getFeed(t, handler, "")
	if len(all) != 1 || all[0].ID != "available" {
		t.Fatalf("all-followed feed should skip unavailable caches: %+v", all)
	}
}

func TestFeedOffsetPreservesLimitBounds(t *testing.T) {
	s, handler := feedTestServer(t)
	posts := make([]render.PublicPost, 502)
	for i := range posts {
		posts[i] = render.PublicPost{ID: strconv.Itoa(i), Created: store.AppleTime(i)}
	}
	saveFeedFollowing(t, s, follow.Entry{IPNS: "k51many"}, "Many posts", posts...)
	for _, tc := range []struct {
		query string
		count int
	}{
		{"?offset=1", 50},
		{"?offset=1&limit=500", 500},
		{"?offset=1&limit=501", 50},
	} {
		page := getFeed(t, handler, tc.query)
		if len(page) != tc.count || page[0].ID != "500" || page[len(page)-1].ID != strconv.Itoa(501-tc.count) {
			t.Fatalf("offset/limit bounds for %s: got %d items, want %d, newest-first starting at 500", tc.query, len(page), tc.count)
		}
	}
}

func TestFeedEmptyResultsAreArrays(t *testing.T) {
	s, handler := feedTestServer(t)
	for _, query := range []string{"", "?source=following", "?source=owned"} {
		if items := getFeed(t, handler, query); len(items) != 0 {
			t.Errorf("empty feed%s: %+v", query, items)
		}
	}
	saveFeedFollowing(t, s, follow.Entry{IPNS: "k51empty"}, "Empty", render.PublicPost{ID: "page", ArticleType: 1})
	if items := getFeed(t, handler, "?ipns=k51empty"); len(items) != 0 {
		t.Errorf("selected site with only pages should have an empty feed: %+v", items)
	}
}
