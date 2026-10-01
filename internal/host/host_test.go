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
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
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
	// pull: a host mirrors a version from a URL, verifying the cid
	files := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer files.Close()
	psig7, _ := site.Keystore().Sign("site1", PushMessage("crop.test", ipnsName, root2, 7, now))
	pbody, _ := json.Marshal(map[string]any{"base": files.URL + "/", "files": []string{"planet.json", "post/index.html", "big.bin"}})
	prq, _ := http.NewRequest("POST", srv.URL+"/v0/host/pull", bytes.NewReader(pbody))
	prq.Host = "crop.test"
	prq.Header.Set("Content-Type", "application/json")
	for k, v := range map[string]string{"Ipns": ipnsName, "Cid": root2, "Seq": "7", "Time": strconv.FormatInt(now, 10), "Sig": base64.StdEncoding.EncodeToString(psig7)} {
		prq.Header.Set("X-Croptop-"+k, v)
	}
	if pr, _ := http.DefaultClient.Do(prq); pr.StatusCode != 202 {
		b, _ := readAll(pr)
		t.Fatalf("pull: %s %s", pr.Status, b)
	}
	for i := 0; i < 100; i++ {
		h.mu.Lock()
		seqNow := h.reg.Keys[ipnsName].Sequence
		h.mu.Unlock()
		if seqNow == 7 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if h.reg.Keys[ipnsName].Sequence != 7 {
		t.Fatalf("pull did not register sequence 7")
	}
	// registry survives a restart
	h2 := &Host{Domain: "crop.test", DataDir: h.DataDir, Engine: h.Engine}
	if err := h2.Start(); err != nil {
		t.Fatal(err)
	}
	if h2.reg.Names["probe"] != ipnsName || h2.reg.Keys[ipnsName].Sequence != 7 {
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
	if c := post("agents", "k51qzi5uqu5dilqjwgdm3zj0g24zaowqfxoargdm1lbj7s4frpwuzqp2tmxm4g", ""); c != 400 {
		t.Fatalf("agents is reserved for the Worker's /agents.md: %d", c)
	}
	if c := post("Bad Name", "k51qzi5uqu5dilqjwgdm3zj0g24zaowqfxoargdm1lbj7s4frpwuzqp2tmxm4g", ""); c != 400 {
		t.Fatalf("invalid: %d", c)
	}
	if c := post("fine", "k51qzi5uqu5dilqjwgdm3zj0g24zaowqfxoargdm1lbj7s4frpwuzqp2tmxm4g", base64.StdEncoding.EncodeToString(make([]byte, 64))); c != 403 {
		t.Fatalf("forged: %d", c)
	}
}

// A push with a parent carries only what changed; the host adds it on top of
// the version it holds and refuses it once that version has been replaced.
func TestPushOnParent(t *testing.T) {
	ctx := context.Background()
	site := offline(t)
	ipnsName, _ := site.Keystore().Generate("s")
	h := &Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offline(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	push := func(dir, c string, seq uint64, parent string) (int, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if !d.IsDir() {
				rel, _ := filepath.Rel(dir, path)
				w, _ := mw.CreateFormFile("file:"+filepath.ToSlash(rel), d.Name())
				b, _ := os.ReadFile(path)
				w.Write(b)
			}
			return nil
		})
		mw.Close()
		now := time.Now().Unix()
		sig, _ := site.Keystore().Sign("s", PushMessage("crop.test", ipnsName, c, seq, now))
		req, _ := http.NewRequest("POST", srv.URL+"/v0/host/push", &buf)
		req.Host = "crop.test"
		req.Header.Set("Content-Type", mw.FormDataContentType())
		for k, v := range map[string]string{"Ipns": ipnsName, "Cid": c, "Seq": strconv.FormatUint(seq, 10), "Time": strconv.FormatInt(now, 10), "Sig": base64.StdEncoding.EncodeToString(sig), "Parent": parent} {
			req.Header.Set("X-Croptop-"+k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := readAll(resp)
		return resp.StatusCode, b
	}
	full, over := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(full, "old"), 0o755)
	os.WriteFile(filepath.Join(full, "old", "photo.png"), []byte("old photo"), 0o644)
	os.WriteFile(filepath.Join(full, "planet.json"), []byte(`{"articles":["old"]}`), 0o644)
	os.MkdirAll(filepath.Join(over, "new"), 0o755)
	os.WriteFile(filepath.Join(over, "new", "index.html"), []byte("new post"), 0o644)
	os.WriteFile(filepath.Join(over, "planet.json"), []byte(`{"articles":["new","old"]}`), 0o644)
	v1, _ := site.AddDir(ctx, full)
	v2, err := site.AddOver(ctx, v1, over)
	if err != nil {
		t.Fatal(err)
	}
	if code, b := push(over, v2, 2, v1); code != 409 || !strings.Contains(b, "holds nothing") {
		t.Fatalf("parent the host never had: %d %s", code, b)
	}
	if code, b := push(full, v1, 1, ""); code != 200 {
		t.Fatalf("full push: %d %s", code, b)
	}
	if code, b := push(over, v2, 2, v1); code != 200 {
		t.Fatalf("push on parent: %d %s", code, b)
	}
	for p, want := range map[string]string{"/old/photo.png": "old photo", "/new/": "new post", "/planet.json": `["new","old"]`} {
		r, _ := http.NewRequest("GET", srv.URL+"/ipfs/"+v2+p, nil)
		r.Host = "crop.test"
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := readAll(resp); resp.StatusCode != 200 || !strings.Contains(b, want) {
			t.Fatalf("%s: %d %q", p, resp.StatusCode, b)
		}
	}
	// a second post built on v1 would drop the first one
	if code, b := push(over, v2, 3, v1); code != 409 || !strings.Contains(b, "holds "+v2) {
		t.Fatalf("stale parent: %d %s", code, b)
	}
}

// A push or pull names its version in X-Croptop-Cid, and the signature only
// proves that the signer chose the string: any key signs "../x". The host once
// joined it into its staging path, so "../../../x" wrote outside the data dir
// and ".." on a final push deleted registry.json along with the rest of host/.
// A request whose version is not a CID is refused before the disk is touched.
func TestVersionMustBeACID(t *testing.T) {
	ctx := context.Background()
	site := offline(t)
	eng := offline(t)
	ipnsName, err := site.Keystore().Generate("s")
	if err != nil {
		t.Fatal(err)
	}
	planet := []byte(`{"name":"Probe"}`)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "planet.json"), planet, 0o644)
	root, err := site.AddDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	// the CIDs croptop makes pass, a v1 directory root and a v0 file CID; nothing
	// that names a path does
	v0, err := site.FileCIDv0(ctx, filepath.Join(dir, "planet.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, cid := range []string{root, v0} {
		if !cidRe.MatchString(cid) {
			t.Errorf("%q is a CID croptop makes, and the host would refuse it", cid)
		}
	}
	for _, cid := range []string{"", ".", "..", "../escape", "bafy/../escape", "bafy..", "bafy/x", `bafy\x`, "bafy%2e%2e", "bafy ", "bafy\n", "bafy", "Qm", "qmabc", "/etc/passwd"} {
		if cidRe.MatchString(cid) {
			t.Errorf("%q passes as a CID", cid)
		}
	}

	// newHost starts a host whose data dir is alone in its parent, top: anything
	// that shows up in top besides the data dir has escaped it
	newHost := func() (h *Host, srv *httptest.Server, top, data string) {
		top = t.TempDir()
		data = filepath.Join(top, "data")
		h = &Host{Domain: "crop.test", DataDir: data, Engine: eng}
		if err := h.Start(); err != nil {
			t.Fatal(err)
		}
		srv = httptest.NewServer(h)
		t.Cleanup(srv.Close)
		return h, srv, top, data
	}
	// sign gives req the headers of a publisher's request; the key signs any cid
	sign := func(req *http.Request, cid string, seq uint64) {
		now := time.Now().Unix()
		sig, _ := site.Keystore().Sign("s", PushMessage("crop.test", ipnsName, cid, seq, now))
		req.Host = "crop.test"
		for k, v := range map[string]string{"Ipns": ipnsName, "Cid": cid, "Seq": strconv.FormatUint(seq, 10), "Time": strconv.FormatInt(now, 10), "Sig": base64.StdEncoding.EncodeToString(sig)} {
			req.Header.Set("X-Croptop-"+k, v)
		}
	}
	do := func(req *http.Request) (int, string) {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := readAll(resp)
		return resp.StatusCode, b
	}
	// push sends planet.json as the files of cid, as part "i/n" when part is set
	push := func(srv *httptest.Server, cid string, seq uint64, part string) (int, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		w, _ := mw.CreateFormFile("file:planet.json", "planet.json")
		w.Write(planet)
		mw.Close()
		req, _ := http.NewRequest("POST", srv.URL+"/v0/host/push", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		sign(req, cid, seq)
		if part != "" {
			req.Header.Set("X-Croptop-Part", part)
		}
		return do(req)
	}
	// snapshot maps every path under dir to its size, -1 for a folder
	snapshot := func(dir string) map[string]int64 {
		out := map[string]int64{}
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(dir, p)
			out[rel] = -1
			if info, err := d.Info(); err == nil && !d.IsDir() {
				out[rel] = info.Size()
			}
			return nil
		})
		return out
	}
	// changes says what was added, removed or resized between two snapshots
	changes := func(before, after map[string]int64) map[string]string {
		out := map[string]string{}
		for p, n := range after {
			if m, ok := before[p]; !ok {
				out[p] = "added"
			} else if m != n {
				out[p] = "changed"
			}
		}
		for p := range before {
			if _, ok := after[p]; !ok {
				out[p] = "removed"
			}
		}
		return out
	}

	// parts of a push named for a folder in staging's place, in host/'s, and
	// beside the data dir
	{
		_, srv, top, data := newHost()
		before := snapshot(top)
		for _, cid := range []string{"../escape", "../../escape", "../../../escape"} {
			if code, body := push(srv, cid, 1, "1/2"); code != 400 || !strings.Contains(body, "bad cid") {
				t.Errorf("part of a push for %q: want 400 bad cid, got %d %q", cid, code, body)
			}
		}
		// so is a chunk of a big file
		creq, _ := http.NewRequest("POST", srv.URL+"/v0/host/push", strings.NewReader("chunk"))
		creq.Header.Set("Content-Type", "application/octet-stream")
		sign(creq, "../../../escape", 1)
		creq.Header.Set("X-Croptop-File", "big.bin")
		creq.Header.Set("X-Croptop-Chunk", "1/2")
		if code, body := do(creq); code != 400 || !strings.Contains(body, "bad cid") {
			t.Errorf("chunk of a push for a version outside staging: want 400 bad cid, got %d %q", code, body)
		}
		if _, err := os.Stat(filepath.Join(data, "..", "escape")); err == nil {
			t.Errorf("a push wrote outside the data dir: %s exists", filepath.Join(data, "..", "escape"))
		}
		if d := changes(before, snapshot(top)); len(d) > 0 {
			t.Errorf("refused pushes left traces on disk: %v", d)
		}
	}

	// a final push removes its staging folder, which for ".." is host/ with
	// registry.json in it, for "." or "" is staging/ with everyone's uploads
	// under way, and for "../.." is the whole data dir
	for _, cid := range []string{"..", "../..", ".", ""} {
		_, srv, top, data := newHost()
		if code, body := push(srv, root, 1, ""); code != 200 {
			t.Fatalf("push of a real version: %d %s", code, body)
		}
		if code, body := push(srv, "bafyupload", 2, "1/2"); code != 200 { // the next version's upload under way
			t.Fatalf("part of a push: %d %s", code, body)
		}
		before := snapshot(top)
		if code, body := push(srv, cid, 2, ""); code != 400 || !strings.Contains(body, "bad cid") {
			t.Errorf("final push for %q: want 400 bad cid, got %d %q", cid, code, body)
		}
		if _, err := os.Stat(filepath.Join(data, "host", "registry.json")); err != nil {
			t.Errorf("final push for %q deleted registry.json: %v", cid, err)
		}
		if d := changes(before, snapshot(top)); len(d) > 0 {
			t.Errorf("refused final push for %q changed the disk: %v", cid, d)
		}
	}

	// pull downloads into the same staging path, from a URL the request names
	{
		h, srv, top, _ := newHost()
		var fetched atomic.Int32
		files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fetched.Add(1)
			http.FileServer(http.Dir(dir)).ServeHTTP(w, r)
		}))
		defer files.Close()
		before := snapshot(top)
		for _, cid := range []string{"../../../escape", ".."} {
			body, _ := json.Marshal(map[string]any{"base": files.URL + "/", "files": []string{"planet.json"}})
			req, _ := http.NewRequest("POST", srv.URL+"/v0/host/pull", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			sign(req, cid, 1)
			if code, b := do(req); code != 400 || !strings.Contains(b, "bad cid") {
				t.Errorf("pull for %q: want 400 bad cid, got %d %q", cid, code, b)
			}
		}
		// mirror, which pull hands the version to, refuses it for any other caller
		if err := h.mirror(ctx, ipnsName, "../../../escape", 1, nil, files.URL+"/", []string{"planet.json"}); err == nil {
			t.Error("mirror took a version that is not a CID")
		}
		if n := fetched.Load(); n != 0 {
			t.Errorf("the host fetched %d files for versions that are not CIDs", n)
		}
		if d := changes(before, snapshot(top)); len(d) > 0 {
			t.Errorf("refused pulls left traces on disk: %v", d)
		}
	}
}

func readAll(r *http.Response) (string, error) {
	defer r.Body.Close()
	var b bytes.Buffer
	_, err := b.ReadFrom(r.Body)
	return b.String(), err
}

// The node answers the IPNS part of the Delegated Routing API that
// delegated-ipfs.dev used to: pushed sites from the registry, others from the
// DHT; PUT validates and stores. It lists its peers, never forwards records,
// refuses absurdly long names, and answers 429 when all its slots for DHT puts
// or lookups are busy.
func TestRoutingEndpointAndPeers(t *testing.T) {
	site := offline(t)
	eng := offline(t)
	eng.RoutingPuts = []string{"https://crop.top/routing/v1/ipns/"}
	eng.PeersURL = "https://crop.top/v0/host/peers"
	h := &Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: eng}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if len(eng.RoutingPuts) != 0 || eng.PeersURL != "" {
		t.Fatal("a host must not forward records or bootstrap from itself")
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	name, _ := site.Keystore().Generate("s")
	rec, _ := site.SignRecord("s", "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4", 7)
	get := func(n string) (int, []byte) {
		resp, err := http.Get(srv.URL + "/routing/v1/ipns/" + n)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	put := func(n string, b []byte) int {
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/routing/v1/ipns/"+n, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/vnd.ipfs.ipns-record")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code, _ := get(name); code != 404 {
		t.Fatalf("unknown name: %d", code)
	}
	if code := put(name, []byte("not a record")); code != 400 {
		t.Fatalf("garbage: %d", code)
	}
	site.Keystore().Generate("o")
	wrong, _ := site.SignRecord("o", "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4", 1)
	if code := put(name, wrong); code != 400 {
		t.Fatalf("another name's record: %d", code)
	}
	if err := eng.RelayRecord(context.Background(), name, wrong); err == nil {
		t.Fatal("RelayRecord took another name's record")
	}
	// a name is a short key; one of attacker length never reaches the parsers
	long := strings.Repeat("k", 200)
	if code, _ := get(long); code != 400 {
		t.Fatalf("200-character name, GET: %d", code)
	}
	if code := put(long, rec); code != 400 {
		t.Fatalf("200-character name, PUT: %d", code)
	}
	if code := put(name, rec); code != 200 {
		t.Fatalf("valid record: %d", code)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, b := get(name)
		if code == 200 && bytes.Equal(b, rec) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET after PUT: %d", code)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// a pushed site answers from the registry at once
	pushed, _ := site.Keystore().Generate("p")
	pushedRec, _ := site.SignRecord("p", "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4", 9)
	h.mu.Lock()
	h.reg.Keys[pushed] = &Entry{IPNS: pushed, Record: pushedRec}
	h.mu.Unlock()
	if code, b := get(pushed); code != 200 || !bytes.Equal(b, pushedRec) {
		t.Fatalf("pushed site: %d", code)
	}
	// no free lookup slot: 429, not another DHT search; the registry needs no slot
	h.lookupSlots = make(chan struct{})
	if code, b := get(pushed); code != 200 || !bytes.Equal(b, pushedRec) {
		t.Fatalf("pushed site with every lookup slot busy: %d", code)
	}
	unknown, _ := site.Keystore().Generate("u") // in neither the registry nor the DHT
	lresp, err := http.Get(srv.URL + "/routing/v1/ipns/" + unknown)
	if err != nil {
		t.Fatal(err)
	}
	lresp.Body.Close()
	if lresp.StatusCode != 429 || lresp.Header.Get("Retry-After") == "" {
		t.Fatalf("all lookup slots busy: %d, Retry-After %q", lresp.StatusCode, lresp.Header.Get("Retry-After"))
	}
	// no free slot: 429, not an unbounded pile of DHT puts
	h.routingSlots = make(chan struct{})
	if code := put(name, rec); code != 429 {
		t.Fatalf("all slots busy: %d", code)
	}
	resp, err := http.Get(srv.URL + "/v0/host/peers")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var peers struct {
		ID    string   `json:"id"`
		Addrs []string `json:"addrs"`
	}
	json.Unmarshal(body, &peers)
	if peers.ID == "" {
		t.Fatal("peers: no id")
	}
	// an offline node has only loopback addresses: an empty list, not null
	if !bytes.Contains(body, []byte(`"addrs":[`)) {
		t.Fatalf("peers: addrs is not a list: %s", body)
	}
}

// Every slot a routing PUT or a DHT lookup takes goes back when the work ends.
// A leaked slot would make the endpoint answer 429 for good after 32 calls.
func TestRoutingSlotsAreReleased(t *testing.T) {
	site := offline(t)
	h := &Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offline(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	name, err := site.Keystore().Generate("s")
	if err != nil {
		t.Fatal(err)
	}
	// more PUTs than there are slots, one after another: a 429 is fine while
	// earlier puts still run, anything else is not
	for seq := uint64(1); seq <= 40; seq++ {
		rec, err := site.SignRecord("s", "bafybeigdfeslmj3qh7cwiehrd5l4cfq6qhgctcxxlk6ou3y3ywq5wfqil4", seq)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/routing/v1/ipns/"+name, bytes.NewReader(rec))
		req.Header.Set("Content-Type", "application/vnd.ipfs.ipns-record")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 && resp.StatusCode != 429 {
			t.Fatalf("PUT of sequence %d: %d", seq, resp.StatusCode)
		}
	}
	// the puts run on after their answers; each one ends and frees its slot
	deadline := time.Now().Add(5 * time.Second)
	for len(h.routingSlots) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d routing slots still held 5 s after the last PUT", len(h.routingSlots))
		}
		time.Sleep(20 * time.Millisecond)
	}
	// each lookup of a name nobody put searches the DHT, and answers 404 only
	// once its slot is free again: 40 in a row never meet a busy endpoint
	for i := 0; i < 40; i++ {
		unknown, err := site.Keystore().Generate(fmt.Sprintf("u%d", i))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Get(srv.URL + "/routing/v1/ipns/" + unknown)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("lookup %d of a name nobody put: %d", i+1, resp.StatusCode)
		}
	}
	if n := len(h.lookupSlots); n != 0 {
		t.Fatalf("%d lookup slots still held after the lookups ended", n)
	}
}

// manifestEnv is a host with a publisher's node beside it. tree adds files to
// that node and gives their folder and CID; push sends a signed push of files
// to the host the way a client does, and gives the status and body it got; get
// asks the host for a path.
type manifestEnv struct {
	h        *Host
	srv      *httptest.Server
	ipnsName string
	tree     func(files map[string]string) (dir, cid string)
	push     func(c string, seq uint64, parent, part string, files map[string]string, manifest string) (int, string)
	get      func(p string) (int, string)
}

func newManifestEnv(t *testing.T) *manifestEnv {
	t.Helper()
	ctx := context.Background()
	h := &Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offline(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	site := offline(t)
	ipnsName, _ := site.Keystore().Generate("site1")
	// a request that never gets its answer fails the test, which would
	// otherwise hang on the host's own long waits
	client := &http.Client{Timeout: 30 * time.Second}
	tree := func(files map[string]string) (string, string) {
		dir := t.TempDir()
		for rel, body := range files {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(body), 0o644)
		}
		c, err := site.AddDir(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		return dir, c
	}
	push := func(c string, seq uint64, parent, part string, files map[string]string, manifest string) (int, string) {
		now := time.Now().Unix()
		sig, _ := site.Keystore().Sign("site1", PushMessage("crop.test", ipnsName, c, seq, now)) // signing host, as the existing test
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for rel, body := range files {
			fw, _ := mw.CreateFormFile("file:"+rel, path.Base(rel))
			fw.Write([]byte(body))
		}
		if manifest != "" {
			mw.WriteField("manifest", manifest)
		}
		mw.Close()
		req, _ := http.NewRequest("POST", srv.URL+"/v0/host/push", &buf)
		req.Host = "crop.test" // as the existing test
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-Croptop-Ipns", ipnsName)
		req.Header.Set("X-Croptop-Cid", c)
		req.Header.Set("X-Croptop-Seq", strconv.FormatUint(seq, 10))
		req.Header.Set("X-Croptop-Time", strconv.FormatInt(now, 10))
		req.Header.Set("X-Croptop-Sig", base64.StdEncoding.EncodeToString(sig))
		if parent != "" {
			req.Header.Set("X-Croptop-Parent", parent)
		}
		if part != "" {
			req.Header.Set("X-Croptop-Part", part)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	get := func(p string) (int, string) {
		resp, err := client.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	return &manifestEnv{h, srv, ipnsName, tree, push, get}
}

// A manifest push is rebuilt from the parent the host holds plus the uploaded
// files: carried paths stay, everything else is gone, and the result must
// hash to the signed CID. Bad manifests are refused, and a push cut short can
// see what it already left here.
func TestManifestPush(t *testing.T) {
	env := newManifestEnv(t)
	h, srv, ipnsName, tree, push, get := env.h, env.srv, env.ipnsName, env.tree, env.push, env.get
	v1Files := map[string]string{"index.html": "home", "assets/a.css": "css", "p1/index.html": "one", "p1/photo.jpg": "photo", "p2/index.html": "two"}
	_, v1 := tree(v1Files)
	_, v2 := tree(map[string]string{"index.html": "home 2", "assets/a.css": "css", "p1/index.html": "one", "p1/photo.jpg": "photo", "p3/index.html": "three"})
	if code, body := push(v1, 1, "", "", v1Files, ""); code != 200 {
		t.Fatalf("v1: %d %s", code, body)
	}
	if code, body := push(v2, 2, v1, "", map[string]string{"index.html": "home 2", "p3/index.html": "three"}, `{"carry":["assets","p1"]}`); code != 200 {
		t.Fatalf("manifest push: %d %s", code, body)
	}
	h.mu.Lock()
	held := h.reg.Keys[ipnsName].CID
	h.mu.Unlock()
	if held != v2 { // v2 has no p2: the rebuild left it out
		t.Fatalf("host holds %s, want %s", held, v2)
	}
	_, v3 := tree(map[string]string{"index.html": "home 3"})
	for _, bad := range []struct{ parent, manifest string }{
		{v2, `{"carry":["nope"]}`},              // not in the parent
		{v2, `{"carry":["index.html"]}`},        // uploaded and carried
		{v2, `{"carry":["p1","p1/photo.jpg"]}`}, // one carried path inside another
		{"", `{"carry":[]}`},                    // no parent
	} {
		if code, body := push(v3, 3, bad.parent, "", map[string]string{"index.html": "home 3"}, bad.manifest); code != 400 {
			t.Fatalf("manifest %s on %q: %d %s, want 400", bad.manifest, bad.parent, code, body)
		}
	}
	// a file uploaded inside a folder that is carried whole
	if code, body := push(v3, 3, v2, "", map[string]string{"index.html": "home 3", "p1/extra.html": "x"}, `{"carry":["p1"]}`); code != 400 {
		t.Fatalf("an upload inside a carried folder: %d %s, want 400", code, body)
	}
	resp, err := http.Get(srv.URL + "/v0/host/keys/" + ipnsName)
	if err != nil {
		t.Fatal(err)
	}
	var entry struct {
		AcceptsManifest bool `json:"acceptsManifest"`
	}
	json.NewDecoder(resp.Body).Decode(&entry)
	resp.Body.Close()
	if !entry.AcceptsManifest {
		t.Fatal("keys must advertise acceptsManifest")
	}
	// part 1 of 2 of a version stays staged; the listing shows it
	_, v4 := tree(map[string]string{"a.txt": "aaaa", "b.txt": "b"})
	if code, body := push(v4, 4, "", "1/2", map[string]string{"a.txt": "aaaa"}, ""); code != 200 {
		t.Fatalf("part 1: %d %s", code, body)
	}
	resp, err = http.Get(srv.URL + "/v0/host/versions/" + v4 + "/files")
	if err != nil {
		t.Fatal(err)
	}
	var staged []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	json.NewDecoder(resp.Body).Decode(&staged)
	resp.Body.Close()
	if len(staged) != 1 || staged[0].Path != "a.txt" || staged[0].Size != 4 {
		t.Fatalf("staged files: %v", staged)
	}
	resp, err = http.Get(srv.URL + "/v0/host/versions/not-a-cid/files")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("a bad cid must be refused: %d", resp.StatusCode)
	}
	// what a pattern without its anchors would let through: a path out of the
	// folder (the %2f decodes to a slash) and a cid with a newline after it
	for _, c := range []string{"bafyabc%2f..%2f..", "bafyabc%0a"} {
		if code, body := get("/v0/host/versions/" + c + "/files"); code != 400 && code != 404 {
			t.Fatalf("cid %q: %d %s, want 400 or 404", c, code, body)
		}
	}

	// The rebuilt version is served from the blocks the host holds, a manifest
	// that makes some other tree than the signed CID moves nothing, and a push
	// in parts is committed by the manifest in its last part.
	for p, want := range map[string]string{"/p1/photo.jpg": "photo", "/assets/a.css": "css", "/p3/index.html": "three", "/index.html": "home 2"} {
		if code, body := get("/ipfs/" + v2 + p); code != 200 || body != want {
			t.Fatalf("%s of the rebuilt version: %d %q, want %q", p, code, body, want)
		}
	}
	if code, body := push(v3, 3, v2, "", map[string]string{"index.html": "home 3"}, `{"carry":["assets"]}`); code != 400 || !strings.Contains(body, "not "+v3) {
		t.Fatalf("files and carried paths that make another tree: %d %s", code, body)
	}
	for _, bad := range []string{`{"carry":`, `{"carry":"p1"}`} {
		if code, body := push(v3, 3, v2, "", map[string]string{"index.html": "home 3"}, bad); code != 400 {
			t.Fatalf("manifest %s: %d %s, want 400", bad, code, body)
		}
	}
	h.mu.Lock()
	held = h.reg.Keys[ipnsName].CID
	h.mu.Unlock()
	if held != v2 {
		t.Fatalf("refused pushes moved the host to %s", held)
	}
	_, v5 := tree(map[string]string{"index.html": "home 5", "assets/a.css": "css", "p1/index.html": "one", "p1/photo.jpg": "photo", "p5/index.html": "five"})
	// the manifest says what the whole version keeps, so only the last part may carry it
	if code, body := push(v5, 5, v2, "1/2", map[string]string{"index.html": "home 5"}, `{"carry":["assets","p1"]}`); code != 400 || strings.TrimSpace(body) != "the manifest goes in the final part" {
		t.Fatalf("a manifest in the first of two parts: %d %s, want 400 and the final part named", code, body)
	}
	if code, body := push(v5, 5, v2, "1/2", map[string]string{"index.html": "home 5"}, ""); code != 200 {
		t.Fatalf("part 1 of a manifest push: %d %s", code, body)
	}
	if code, body := push(v5, 5, v2, "2/2", map[string]string{"p5/index.html": "five"}, `{"carry":["assets","p1"]}`); code != 200 {
		t.Fatalf("part 2 of a manifest push: %d %s", code, body)
	}
	h.mu.Lock()
	held = h.reg.Keys[ipnsName].CID
	h.mu.Unlock()
	if held != v5 {
		t.Fatalf("host holds %s, want %s", held, v5)
	}
	if code, body := get("/v0/host/versions/" + v5 + "/files"); code != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("a committed push leaves nothing staged: %d %s", code, body)
	}
}

// Only a manifest the host refuses on its own terms is the pusher's fault. A
// failure of the host's own, such as a staged file it cannot read, is a 500
// that keeps the host's disk to itself and leaves the detail in its log.
func TestManifestPushFailureOfTheHostIsA500(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the file stays readable")
	}
	env := newManifestEnv(t)
	h, tree, push := env.h, env.tree, env.push
	logs := make(chan string, 16)
	h.Log = func(s string) {
		select {
		case logs <- s:
		default:
		}
	}
	logged := func() (all []string) {
		for {
			select {
			case l := <-logs:
				all = append(all, l)
			default:
				return
			}
		}
	}
	v1Files := map[string]string{"index.html": "home", "assets/a.css": "css"}
	_, v1 := tree(v1Files)
	if code, body := push(v1, 1, "", "", v1Files, ""); code != 200 {
		t.Fatalf("v1: %d %s", code, body)
	}
	_, v2 := tree(map[string]string{"index.html": "home 2", "assets/a.css": "css", "zz.txt": "zz"})
	// part 1 only stages zz.txt, which is unreadable when the last part rebuilds
	if code, body := push(v2, 2, v1, "1/2", map[string]string{"zz.txt": "zz"}, ""); code != 200 {
		t.Fatalf("part 1: %d %s", code, body)
	}
	if err := os.Chmod(filepath.Join(h.DataDir, "host", "staging", v2, "zz.txt"), 0); err != nil {
		t.Fatal(err)
	}
	code, body := push(v2, 2, v1, "2/2", map[string]string{"index.html": "home 2"}, `{"carry":["assets"]}`)
	if code != 500 {
		t.Fatalf("a staged file the host cannot read: %d %s, want 500", code, body)
	}
	if strings.Contains(body, h.DataDir) || strings.Contains(body, "permission") {
		t.Fatalf("the answer hands the host's disk to the pusher: %s", body)
	}
	if all := strings.Join(logged(), "\n"); !strings.Contains(all, "permission denied") {
		t.Fatalf("the host did not log what failed: %q", all)
	}
}

// A parent whose blocks this host does not hold would have the rebuild wait
// for peers: the wait is bounded, and giving up is the host's failure.
func TestManifestPushWithoutTheParentsBlocksGivesUp(t *testing.T) {
	env := newManifestEnv(t)
	h, tree, push := env.h, env.tree, env.push
	h.rebuildTimeout = 300 * time.Millisecond
	_, v1 := tree(map[string]string{"index.html": "home", "assets/a.css": "css"}) // only the publisher's node has these blocks
	h.mu.Lock()
	h.reg.Keys[env.ipnsName] = &Entry{IPNS: env.ipnsName, CID: v1, Sequence: 1}
	h.mu.Unlock()
	_, v2 := tree(map[string]string{"index.html": "home 2", "assets/a.css": "css"})
	if code, body := push(v2, 2, v1, "", map[string]string{"index.html": "home 2"}, `{"carry":["assets"]}`); code != 500 {
		t.Fatalf("a parent whose blocks are not here: %d %s, want 500", code, body)
	}
}

// A manifest is read up to 8 MiB. One byte more is refused as too large, not
// cut short and refused as JSON that does not parse.
func TestManifestSizeLimit(t *testing.T) {
	env := newManifestEnv(t)
	tree, push := env.tree, env.push
	v1Files := map[string]string{"index.html": "home", "assets/a.css": "css"}
	_, v1 := tree(v1Files)
	if code, body := push(v1, 1, "", "", v1Files, ""); code != 200 {
		t.Fatalf("v1: %d %s", code, body)
	}
	_, v2 := tree(map[string]string{"index.html": "home 2"})
	// an empty carry list padded with spaces, which JSON allows
	manifest := func(n int) string { return `{"carry":[]}` + strings.Repeat(" ", n-len(`{"carry":[]}`)) }
	files := map[string]string{"index.html": "home 2"}
	if code, body := push(v2, 2, v1, "", files, manifest(8<<20+1)); code != 400 || strings.TrimSpace(body) != "manifest too large" {
		t.Fatalf("a manifest one byte over the limit: %d %s, want 400 manifest too large", code, body)
	}
	if code, body := push(v2, 2, v1, "", files, manifest(8<<20)); code != 200 {
		t.Fatalf("a manifest of exactly the limit: %d %s", code, body)
	}
}

// A push left unfinished two days ago will not be resumed: a host starting up
// clears it out, and keeps what is more recent.
func TestStartClearsOldStaging(t *testing.T) {
	dir := t.TempDir()
	stage := filepath.Join(dir, "host", "staging")
	old, recent := filepath.Join(stage, "bafyold"), filepath.Join(stage, "bafyrecent")
	for _, d := range []string{old, recent} {
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "a.txt"), []byte("a"), 0o644)
	}
	then := time.Now().Add(-49 * time.Hour)
	os.Chtimes(old, then, then)
	h := &Host{Domain: "crop.test", DataDir: dir, Engine: offline(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("a staging dir untouched for 49 hours must go: %v", err)
	}
	if _, err := os.Stat(filepath.Join(recent, "a.txt")); err != nil {
		t.Fatalf("a recent staging dir must stay: %v", err)
	}
}

// conflict keeps every version a host holds: a push either builds on it (its
// parent), comes after it (a higher sequence), or is that same version again.
// A whole version at the held sequence with other content is refused: two
// machines chose the same next sequence, and the later one would replace the
// earlier unseen.
func TestConflictKeepsEveryVersion(t *testing.T) {
	held := &Entry{CID: "bafyheld", Sequence: 5}
	for _, tc := range []struct {
		name              string
		e                 *Entry
		seq               uint64
		cid, parent, want string
	}{
		{"a first push", nil, 1, "bafyfirst", "", ""},
		{"a newer whole version", held, 6, "bafynew", "", ""},
		{"the held version again", held, 5, "bafyheld", "", ""},
		{"another version at the held sequence", held, 5, "bafytwin", "", "host holds bafyheld at sequence 5"},
		{"an older sequence", held, 4, "bafyold", "", "host already has sequence 5"},
		{"a post on the held version", held, 6, "bafypost", "bafyheld", ""},
		{"a post on another version", held, 6, "bafypost", "bafyother", "host holds bafyheld, not bafyother"},
	} {
		if got := conflict(tc.e, tc.seq, tc.cid, tc.parent); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
