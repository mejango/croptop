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
