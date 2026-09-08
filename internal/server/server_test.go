package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mejango/croptop/internal/config"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake ipfs is a shell script")
	}
	root := t.TempDir()
	t.Setenv("FAKE_LOG", filepath.Join(root, "calls.log"))
	script, _ := filepath.Abs("../publish/testdata/fake-ipfs")
	node := ipfs.NewNode(script, filepath.Join(root, "ipfs"))
	st := &store.Store{Root: root}
	r := &render.Renderer{Store: st, Templates: templates.FS, CIDs: node}
	pub := &publish.Publisher{Store: st, Node: node, Render: r}
	s := &Server{
		Store: st, Pub: pub, Node: node, Cfg: &config.Config{Listen: config.DefaultListen},
		UI: fstest.MapFS{"index.html": {Data: []byte("<h1>ui</h1>")}}, Templates: templates.FS, Version: "test",
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func multipartBody(t *testing.T, fields map[string]string, files map[string][]byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	for name, data := range files {
		fw, _ := mw.CreateFormFile("attachments", name)
		fw.Write(data)
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func do(t *testing.T, method, url string, body io.Reader, ctype string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, body)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(b, &m)
	if m == nil {
		m = map[string]any{"_raw": string(b)}
	}
	return resp.StatusCode, m
}

func TestAPIFlow(t *testing.T) {
	s, ts := testServer(t)
	if code, m := do(t, "GET", ts.URL+"/v0/ping", nil, ""); code != 200 || m["_raw"] != "pong" {
		t.Fatalf("ping %d %v", code, m)
	}
	body, ctype := multipartBody(t, map[string]string{"name": "Test Site", "about": "hi", "template": "Croptop"}, nil)
	code, site := do(t, "POST", ts.URL+"/v0/planets/my", body, ctype)
	if code != 200 || site["id"] == nil || !strings.HasPrefix(site["ipns"].(string), "k51") {
		t.Fatalf("create %d %v", code, site)
	}
	id := site["id"].(string)
	if _, err := os.Stat(filepath.Join(s.Store.PublicDir(id), "index.html")); err != nil {
		t.Fatal("new site not rendered")
	}

	body, ctype = multipartBody(t, map[string]string{"title": "First", "content": "hello **world**", "tags": "a, b"}, map[string][]byte{"photo.png": pngBytes()})
	code, post := do(t, "POST", ts.URL+"/v0/planets/my/"+id+"/articles", body, ctype)
	if code != 200 {
		t.Fatalf("create article %d %v", code, post)
	}
	pid := post["id"].(string)
	if _, err := os.Stat(filepath.Join(s.Store.PostDir(id, pid), "photo.png")); err != nil {
		t.Fatal("attachment not stored")
	}
	if cids, _ := post["cids"].(map[string]any); cids["photo.png"] == nil {
		t.Fatalf("cid not computed: %v", post)
	}
	if _, err := os.Stat(filepath.Join(s.Store.PublicDir(id), pid, "nft.json.cid.txt")); err != nil {
		t.Fatal("nft metadata not generated")
	}
	code, list := do(t, "GET", ts.URL+"/v0/planets/my/"+id+"/articles", nil, "")
	if code != 200 {
		t.Fatalf("list %d", code)
	}
	_ = list
	code, _ = do(t, "GET", ts.URL+"/"+id+"/"+pid+"/", nil, "")
	if code != 200 {
		t.Fatalf("public post page %d", code)
	}
	code, pub := do(t, "POST", ts.URL+"/v0/planets/my/"+id+"/publish", nil, "")
	if code != 200 || pub["cid"] != "bafyFAKE" {
		t.Fatalf("publish %d %v", code, pub)
	}
	code, _ = do(t, "DELETE", ts.URL+"/v0/planets/my/"+id+"/articles/"+pid, nil, "")
	if code != 200 {
		t.Fatalf("delete %d", code)
	}
	if _, err := s.Store.Post(id, pid); err == nil {
		t.Fatal("post still exists")
	}
}

func TestAuth(t *testing.T) {
	s, ts := testServer(t)
	s.Cfg.SetPasscode("secret")
	code, _ := do(t, "GET", ts.URL+"/v0/planets/my", nil, "")
	if code != 401 {
		t.Fatalf("want 401 got %d", code)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/v0/planets/my", nil)
	req.SetBasicAuth("Croptop", "secret")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("want 200 got %d", resp.StatusCode)
	}
	req.SetBasicAuth("Croptop", "wrong")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 401 {
		t.Fatalf("wrong passcode: want 401 got %d", resp.StatusCode)
	}
	// public trees stay open
	os.MkdirAll(s.Store.PublicDir("FF5F456D-904F-4EE6-8BB5-AD175C65319A"), 0o755)
	os.WriteFile(filepath.Join(s.Store.PublicDir("FF5F456D-904F-4EE6-8BB5-AD175C65319A"), "index.html"), []byte("site"), 0o644)
	code, _ = do(t, "GET", ts.URL+"/FF5F456D-904F-4EE6-8BB5-AD175C65319A/", nil, "")
	if code != 200 {
		t.Fatalf("public tree should be open: %d", code)
	}
}

// pngBytes is a 2x2 PNG.
func pngBytes() []byte {
	return []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x02\x00\x00\x00\x02\x08\x02\x00\x00\x00\xfd\xd4\x9as\x00\x00\x00\x12IDATx\x9cc\xfc\xcf\xc0\xc0\xc0\xc0\xc4\xc0\xc0\xc0\x00\x00\x0b\x08\x01\x03\xb0\xb1\x8d\xa4\x00\x00\x00\x00IEND\xaeB`\x82")
}

func TestPostControls(t *testing.T) {
	s, ts := testServer(t)
	body, ctype := multipartBody(t, map[string]string{"name": "Ctl"}, nil)
	_, site := do(t, "POST", ts.URL+"/v0/planets/my", body, ctype)
	id := site["id"].(string)
	body, ctype = multipartBody(t, map[string]string{"title": "A", "content": "x"}, map[string][]byte{"b.png": pngBytes(), "a.png": pngBytes()})
	_, post := do(t, "POST", ts.URL+"/v0/planets/my/"+id+"/articles", body, ctype)
	pid := post["id"].(string)
	if post["heroImageWidth"] == nil {
		t.Fatalf("hero dims not computed: %v", post)
	}
	body, ctype = multipartBody(t, map[string]string{"pinned": "true", "includeInNavigation": "true", "navigationWeight": "3", "heroImage": "b.png"}, nil)
	code, upd := do(t, "POST", ts.URL+"/v0/planets/my/"+id+"/articles/"+pid, body, ctype)
	if code != 200 || upd["pinned"] == nil || upd["isIncludedInNavigation"] != true || upd["navigationWeight"] != float64(3) || upd["heroImage"] != "b.png" {
		t.Fatalf("controls not saved: %d %v", code, upd)
	}
	var pj struct {
		Articles []map[string]any `json:"articles"`
	}
	b, _ := os.ReadFile(filepath.Join(s.Store.PublicDir(id), "planet.json"))
	json.Unmarshal(b, &pj)
	if hf, _ := pj.Articles[0]["heroImageFilename"].(string); hf != "b.png" {
		t.Fatalf("published hero %q", hf)
	}
	body, ctype = multipartBody(t, map[string]string{"pinned": "false", "heroImage": ""}, nil)
	_, upd = do(t, "POST", ts.URL+"/v0/planets/my/"+id+"/articles/"+pid, body, ctype)
	if upd["pinned"] != nil || upd["heroImage"] != nil {
		t.Fatalf("controls not cleared: %v", upd)
	}
	code, m := do(t, "POST", ts.URL+"/v0/croptop/markdown", strings.NewReader("**b**"), "text/plain")
	if code != 200 || !strings.Contains(m["_raw"].(string), "<strong>b</strong>") {
		t.Fatalf("markdown preview: %d %v", code, m)
	}
	if _, err := os.Stat(filepath.Join(s.Store.PublicDir(id), "rss.xml")); err != nil {
		t.Fatal("rss.xml not written for a new site")
	}
}
