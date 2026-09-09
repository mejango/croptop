package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/ipfs"
)

func offline(t *testing.T) *ipfs.Embedded {
	t.Helper()
	e := ipfs.NewEmbedded(t.TempDir())
	e.Offline = true
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Stop() })
	return e
}

func TestClaimPushServe(t *testing.T) {
	ctx := context.Background()
	site := offline(t) // the publisher's node
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "planet.json"), []byte(`{"name":"Probe"}`), 0o644)
	os.MkdirAll(filepath.Join(dir, "post"), 0o755)
	os.WriteFile(filepath.Join(dir, "post", "index.html"), []byte("<h1>hi</h1>"), 0o644)
	root, err := site.AddDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	ipnsName, err := site.Keystore().Generate("site1")
	if err != nil {
		t.Fatal(err)
	}

	h := &Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offline(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	now := time.Now().Unix()

	// claim a name
	sig, _ := site.Keystore().Sign("site1", ClaimMessage("crop.test", "probe", ipnsName, now))
	body, _ := json.Marshal(map[string]any{"name": "probe", "ipns": ipnsName, "time": now, "sig": base64.StdEncoding.EncodeToString(sig)})
	req, _ := http.NewRequest("POST", srv.URL+"/v0/host/names", bytes.NewReader(body))
	req.Host = "crop.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("claim: %v %v", err, resp.Status)
	}
	// a second key cannot take it
	site.Keystore().Generate("site2")
	other, _ := site.Keystore().Name("site2")
	sig2, _ := site.Keystore().Sign("site2", ClaimMessage("crop.test", "probe", other, now))
	body2, _ := json.Marshal(map[string]any{"name": "probe", "ipns": other, "time": now, "sig": base64.StdEncoding.EncodeToString(sig2)})
	req2, _ := http.NewRequest("POST", srv.URL+"/v0/host/names", bytes.NewReader(body2))
	req2.Host = "crop.test"
	if resp2, _ := http.DefaultClient.Do(req2); resp2.StatusCode != 409 {
		t.Fatalf("second claim: want 409, got %s", resp2.Status)
	}

	// push the site as files
	form := func() (*bytes.Buffer, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(dir, path)
			w, _ := mw.CreateFormFile("file:"+filepath.ToSlash(rel), filepath.Base(rel))
			b, _ := os.ReadFile(path)
			w.Write(b)
			return nil
		})
		mw.Close()
		return &buf, mw.FormDataContentType()
	}
	body3, ctype := form()
	psig, _ := site.Keystore().Sign("site1", PushMessage("crop.test", ipnsName, root, 3, now))
	preq, _ := http.NewRequest("POST", srv.URL+"/v0/host/push", body3)
	preq.Host = "crop.test"
	preq.Header.Set("Content-Type", ctype)
	preq.Header.Set("X-Croptop-Ipns", ipnsName)
	preq.Header.Set("X-Croptop-Cid", root)
	preq.Header.Set("X-Croptop-Seq", "3")
	preq.Header.Set("X-Croptop-Time", strconv.FormatInt(now, 10))
	preq.Header.Set("X-Croptop-Sig", base64.StdEncoding.EncodeToString(psig))
	presp, err := http.DefaultClient.Do(preq)
	if err != nil || presp.StatusCode != 200 {
		b, _ := readAll(presp)
		t.Fatalf("push: %v %v %s", err, presp.Status, b)
	}

	// served at crop.test/probe/
	get := func(host, p string) (int, string) {
		r, _ := http.NewRequest("GET", srv.URL+p, nil)
		r.Host = host
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := readAll(resp)
		return resp.StatusCode, b
	}
	if code, b := get("crop.test", "/probe/planet.json"); code != 200 || !strings.Contains(b, "Probe") {
		t.Fatalf("path serve: %d %s", code, b)
	}
	if code, b := get("crop.test", "/probe/post/"); code != 200 || !strings.Contains(b, "<h1>hi</h1>") {
		t.Fatalf("dir index: %d %s", code, b)
	}
	// raw IPNS name and CID subdomains
	if code, _ := get(ipnsName+".crop.test", "/planet.json"); code != 200 {
		t.Fatalf("ipns subdomain: %d", code)
	}
	if code, _ := get(root+".crop.test", "/planet.json"); code != 200 {
		t.Fatalf("cid subdomain: %d", code)
	}
	// directory lists the name
	if code, b := get("crop.test", "/"); code != 200 || !strings.Contains(b, `href="/probe/"`) {
		t.Fatalf("directory: %d %s", code, b)
	}
	// a stale sequence is refused
	preq.Header.Set("X-Croptop-Seq", "2")
	psig2, _ := site.Keystore().Sign("site1", PushMessage("crop.test", ipnsName, root, 2, now))
	preq.Header.Set("X-Croptop-Sig", base64.StdEncoding.EncodeToString(psig2))
	body4, ctype4 := form()
	preq.Body = io.NopCloser(body4)
	preq.Header.Set("Content-Type", ctype4)
	if r, _ := http.DefaultClient.Do(preq); r.StatusCode != 409 {
		t.Fatalf("stale push: want 409, got %s", r.Status)
	}
	// with a root site, unclaimed bare paths come from it and the directory moves
	h.Root = root
	if code, b := get("crop.test", "/planet.json"); code != 200 || !strings.Contains(b, "Probe") {
		t.Fatalf("root site: %d %s", code, b)
	}
	if code, b := get("crop.test", "/probe/planet.json"); code != 200 || !strings.Contains(b, "Probe") {
		t.Fatalf("claimed name still wins: %d %s", code, b)
	}
	if code, b := get("crop.test", "/directory"); code != 200 || !strings.Contains(b, `href="/probe/"`) {
		t.Fatalf("directory at /directory: %d %s", code, b)
	}
	h.Root = ""
	// registry survives a restart
	h2 := &Host{Domain: "crop.test", DataDir: h.DataDir, Engine: h.Engine}
	if err := h2.Start(); err != nil {
		t.Fatal(err)
	}
	if h2.reg.Names["probe"] != ipnsName || h2.reg.Keys[ipnsName].Sequence != 3 {
		t.Fatal("registry not persisted")
	}
}

func TestBadSignatureAndNames(t *testing.T) {
	h := &Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offline(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	post := func(name, ipns, sig string) int {
		body, _ := json.Marshal(map[string]any{"name": name, "ipns": ipns, "time": time.Now().Unix(), "sig": sig})
		r, _ := http.NewRequest("POST", srv.URL+"/v0/host/names", bytes.NewReader(body))
		r.Host = "crop.test"
		resp, _ := http.DefaultClient.Do(r)
		return resp.StatusCode
	}
	if c := post("www", "k51qzi5uqu5dilqjwgdm3zj0g24zaowqfxoargdm1lbj7s4frpwuzqp2tmxm4g", ""); c != 400 {
		t.Fatalf("reserved: %d", c)
	}
	if c := post("Bad Name", "k51qzi5uqu5dilqjwgdm3zj0g24zaowqfxoargdm1lbj7s4frpwuzqp2tmxm4g", ""); c != 400 {
		t.Fatalf("invalid: %d", c)
	}
	if c := post("fine", "k51qzi5uqu5dilqjwgdm3zj0g24zaowqfxoargdm1lbj7s4frpwuzqp2tmxm4g", base64.StdEncoding.EncodeToString(make([]byte, 64))); c != 403 {
		t.Fatalf("forged: %d", c)
	}
}

func readAll(r *http.Response) (string, error) {
	defer r.Body.Close()
	var b bytes.Buffer
	_, err := b.ReadFrom(r.Body)
	return b.String(), err
}
