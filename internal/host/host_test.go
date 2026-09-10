package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	// classic path gateway on the bare domain
	if code, b := get("crop.test", "/ipfs/"+root+"/planet.json"); code != 200 || !strings.Contains(b, "Probe") {
		t.Fatalf("/ipfs path: %d %s", code, b)
	}
	if code, b := get("crop.test", "/ipns/"+ipnsName+"/post/"); code != 200 || !strings.Contains(b, "<h1>hi</h1>") {
		t.Fatalf("/ipns path: %d %s", code, b)
	}
	if code, _ := get("crop.test", "/v0/host/health"); code != 200 {
		t.Fatalf("health: %d", code)
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
	// a push forwarded from a trusted domain verifies against that domain,
	// and may carry its files base64-encoded
	h.Trust = []string{"crop.example"}
	body5, ctype5 := func() (*bytes.Buffer, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(dir, path)
			w, _ := mw.CreateFormFile("file:"+filepath.ToSlash(rel), filepath.Base(rel))
			b, _ := os.ReadFile(path)
			w.Write([]byte(base64.StdEncoding.EncodeToString(b)))
			return nil
		})
		mw.Close()
		return &buf, mw.FormDataContentType()
	}()
	fsig, _ := site.Keystore().Sign("site1", PushMessage("crop.example", ipnsName, root, 4, now))
	freq, _ := http.NewRequest("POST", srv.URL+"/v0/host/push", body5)
	freq.Host = "crop.test"
	freq.Header.Set("Content-Type", ctype5)
	freq.Header.Set("X-Croptop-Ipns", ipnsName)
	freq.Header.Set("X-Croptop-Cid", root)
	freq.Header.Set("X-Croptop-Seq", "4")
	freq.Header.Set("X-Croptop-Time", strconv.FormatInt(now, 10))
	freq.Header.Set("X-Croptop-Sig", base64.StdEncoding.EncodeToString(fsig))
	freq.Header.Set("X-Croptop-Signed-Host", "crop.example")
	freq.Header.Set("X-Croptop-Encoding", "base64")
	if fr, _ := http.DefaultClient.Do(freq); fr.StatusCode != 200 {
		b, _ := readAll(fr)
		t.Fatalf("forwarded push: %s %s", fr.Status, b)
	}
	body6, ctype6 := form()
	ureq, _ := http.NewRequest("POST", srv.URL+"/v0/host/push", body6)
	ureq.Host = "crop.test"
	ureq.Header = freq.Header.Clone()
	ureq.Header.Set("Content-Type", ctype6)
	ureq.Header.Set("X-Croptop-Signed-Host", "evil.example")
	ureq.Header.Del("X-Croptop-Encoding")
	if fr, err := http.DefaultClient.Do(ureq); err != nil || fr.StatusCode != 403 {
		t.Fatalf("untrusted forward: want 403, got %v %v", err, fr)
	}
	h.Trust = nil
	// a push may arrive in parts: the first is held, the last commits
	partBody := func(files map[string]string) (*bytes.Buffer, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for rel, path := range files {
			w, _ := mw.CreateFormFile("file:"+rel, filepath.Base(rel))
			b, _ := os.ReadFile(path)
			w.Write(b)
		}
		mw.Close()
		return &buf, mw.FormDataContentType()
	}
	psig5, _ := site.Keystore().Sign("site1", PushMessage("crop.test", ipnsName, root, 5, now))
	send := func(part string, files map[string]string) *http.Response {
		b, ct := partBody(files)
		rq, _ := http.NewRequest("POST", srv.URL+"/v0/host/push", b)
		rq.Host = "crop.test"
		rq.Header.Set("Content-Type", ct)
		rq.Header.Set("X-Croptop-Ipns", ipnsName)
		rq.Header.Set("X-Croptop-Cid", root)
		rq.Header.Set("X-Croptop-Seq", "5")
		rq.Header.Set("X-Croptop-Time", strconv.FormatInt(now, 10))
		rq.Header.Set("X-Croptop-Sig", base64.StdEncoding.EncodeToString(psig5))
		rq.Header.Set("X-Croptop-Part", part)
		resp, _ := http.DefaultClient.Do(rq)
		return resp
	}
	if r1 := send("1/2", map[string]string{"planet.json": filepath.Join(dir, "planet.json")}); r1.StatusCode != 200 {
		t.Fatalf("part 1: %s", r1.Status)
	}
	if e := h.reg.Keys[ipnsName]; e.Sequence != 4 {
		t.Fatalf("part 1 must not commit, sequence is %d", e.Sequence)
	}
	if r2 := send("2/2", map[string]string{"post/index.html": filepath.Join(dir, "post", "index.html")}); r2.StatusCode != 200 {
		b, _ := readAll(r2)
		t.Fatalf("part 2: %s %s", r2.Status, b)
	}
	if e := h.reg.Keys[ipnsName]; e.Sequence != 5 {
		t.Fatalf("part 2 must commit, sequence is %d", e.Sequence)
	}
	// a big file arrives in chunks, then the parts commit the version
	big := bytes.Repeat([]byte("croptop "), 4096) // 32 KiB, split in two
	os.WriteFile(filepath.Join(dir, "big.bin"), big, 0o644)
	root2, _ := site.AddDir(ctx, dir)
	psig6, _ := site.Keystore().Sign("site1", PushMessage("crop.test", ipnsName, root2, 6, now))
	chunk := func(i, n int, b []byte) int {
		rq, _ := http.NewRequest("POST", srv.URL+"/v0/host/push", bytes.NewReader(b))
		rq.Host = "crop.test"
		rq.Header.Set("Content-Type", "application/octet-stream")
		for k, v := range map[string]string{"Ipns": ipnsName, "Cid": root2, "Seq": "6", "Time": strconv.FormatInt(now, 10), "Sig": base64.StdEncoding.EncodeToString(psig6), "File": "big.bin", "Chunk": fmt.Sprintf("%d/%d", i, n)} {
			rq.Header.Set("X-Croptop-"+k, v)
		}
		resp, _ := http.DefaultClient.Do(rq)
		return resp.StatusCode
	}
	if c := chunk(2, 2, big[len(big)/2:]); c != 200 {
		t.Fatalf("chunk 2: %d", c)
	}
	if c := chunk(1, 2, big[:len(big)/2]); c != 200 {
		t.Fatalf("chunk 1: %d", c)
	}
	body7, ct7 := partBody(map[string]string{"planet.json": filepath.Join(dir, "planet.json"), "post/index.html": filepath.Join(dir, "post", "index.html")})
	rq7, _ := http.NewRequest("POST", srv.URL+"/v0/host/push", body7)
	rq7.Host = "crop.test"
	rq7.Header.Set("Content-Type", ct7)
	for k, v := range map[string]string{"Ipns": ipnsName, "Cid": root2, "Seq": "6", "Time": strconv.FormatInt(now, 10), "Sig": base64.StdEncoding.EncodeToString(psig6), "Part": "1/1"} {
		rq7.Header.Set("X-Croptop-"+k, v)
	}
	if r7, _ := http.DefaultClient.Do(rq7); r7.StatusCode != 200 {
		b, _ := readAll(r7)
		t.Fatalf("commit after chunks: %s %s", r7.Status, b)
	}
	if code, b := get("crop.test", "/probe/big.bin"); code != 200 || len(b) != len(big) {
		t.Fatalf("chunked file served: %d %d bytes", code, len(b))
	}
	// registry survives a restart
	h2 := &Host{Domain: "crop.test", DataDir: h.DataDir, Engine: h.Engine}
	if err := h2.Start(); err != nil {
		t.Fatal(err)
	}
	if h2.reg.Names["probe"] != ipnsName || h2.reg.Keys[ipnsName].Sequence != 6 {
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
