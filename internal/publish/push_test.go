package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

func TestHostDefaultsAndCustomHost(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"new or imported", `{}`, DefaultHost},
		{"saved empty", `{"croptopHost":""}`, DefaultHost},
		{"whitespace", `{"croptopHost":"  "}`, DefaultHost},
		{"null", `{"croptopHost":null}`, DefaultHost},
		{"custom", `{"croptopHost":" https://host.example/ "}`, "https://host.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var site store.Site
			if err := json.Unmarshal([]byte(tc.raw), &site); err != nil {
				t.Fatal(err)
			}
			if got := HostOf(&site); got != tc.want {
				t.Fatalf("HostOf = %q, want %q", got, tc.want)
			}
		})
	}
	var site store.Site
	SetHost(&site, "https://host.example/")
	if got := HostOf(&site); got != "https://host.example" {
		t.Fatalf("custom host = %q", got)
	}
	SetHost(&site, "")
	if got := HostOf(&site); got != DefaultHost {
		t.Fatalf("clearing host = %q, want %q", got, DefaultHost)
	}
}

type hostTransport func(*http.Request) (*http.Response, error)

func (f hostTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPublishPushesDefaultAndCustomHost(t *testing.T) {
	empty, custom := "", "https://host.example/"
	for _, tc := range []struct {
		name string
		host *string
		want string
	}{
		{name: "missing host", want: DefaultHost},
		{name: "saved empty host", host: &empty, want: DefaultHost},
		{name: "custom host", host: &custom, want: "https://host.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, s, _ := fakePublisher(t)
			p.SkipPrewarm, p.Wait = false, true
			site, err := s.Site(fixtureID)
			if err != nil {
				t.Fatal(err)
			}
			delete(site.Raw, HostKey)
			if tc.host != nil {
				SetHost(site, *tc.host)
			}
			cid := "bafyOLD"
			site.LastPublishedCID, site.IPNSSequence = &cid, 2
			if err := s.SaveSite(site); err != nil {
				t.Fatal(err)
			}
			const privateMarker = "private-source-file-must-stay-local"
			if err := os.WriteFile(filepath.Join(s.SiteDir(fixtureID), "private-note.txt"), []byte(privateMarker), 0o600); err != nil {
				t.Fatal(err)
			}
			transport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = transport })
			pushes := 0
			http.DefaultTransport = hostTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPost && r.URL.Path == "/v0/host/push" {
					pushes++
					if got := r.URL.Scheme + "://" + r.URL.Host; got != tc.want {
						t.Errorf("push host = %q, want %q", got, tc.want)
					}
					if r.Header.Get("X-Croptop-Cid") != "bafyFAKE" || r.Header.Get("X-Croptop-Seq") != "8" || r.Header.Get("X-Croptop-Sig") == "" {
						t.Error("push did not carry the newly published version and signature")
					}
					defer r.Body.Close()
					body, err := io.ReadAll(r.Body)
					if err != nil {
						return nil, err
					}
					if !strings.Contains(string(body), `name="file:index.html"`) {
						t.Error("push did not include the rendered site")
					}
					if strings.Contains(string(body), privateMarker) {
						t.Error("push included a private source file")
					}
				}
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
			})
			if _, err := p.Publish(context.Background(), fixtureID, false); err != nil {
				t.Fatal(err)
			}
			if pushes == 0 {
				t.Fatal("publishing did not push to its host")
			}
		})
	}
}

// An upload keeps going while its body moves, however slowly, and is given
// up when it stops moving or the host never answers.
func TestUploadsKeepGoingWhileTheyMove(t *testing.T) {
	stall, reply := stallAfter, replyWithin
	stallAfter, replyWithin = 300*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { stallAfter, replyWithin = stall, reply })
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/silent" {
			<-release // the body is in; the answer never comes
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	defer close(release) // runs before srv.Close, which waits for the handler
	send := func(path string, body io.Reader) error {
		req, _ := http.NewRequest("POST", srv.URL+path, body)
		resp, err := sendWatched(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, resp.Body)
		return resp.Body.Close()
	}
	if err := send("/", &slowReader{n: 12, every: 50 * time.Millisecond}); err != nil {
		t.Fatalf("a slow body that keeps moving was given up: %v", err)
	}
	stuck := make(chan struct{})
	defer close(stuck)
	start := time.Now()
	if err := send("/", &stuckReader{wait: stuck}); !errors.Is(err, errStalled) || time.Since(start) > 5*time.Second {
		t.Fatalf("a body that stopped moving was not given up as stalled in time: %v after %s", err, time.Since(start))
	}
	if err := send("/silent", strings.NewReader("all of it")); !errors.Is(err, errNoAnswer) {
		t.Fatalf("a host that never answers was not given up as silent: %v", err)
	}
}

// A host that starts its answer and then stops is held to the reply limit
// too: reading the answer is part of waiting for it.
func TestAStalledAnswerIsGivenUp(t *testing.T) {
	stall, reply := stallAfter, replyWithin
	stallAfter, replyWithin = 300*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { stallAfter, replyWithin = stall, reply })
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte(`{"upload":`))
		w.(http.Flusher).Flush()
		<-release // the rest of the answer never comes
	}))
	defer srv.Close()
	defer close(release) // runs before srv.Close, which waits for the handler
	req, _ := http.NewRequest("POST", srv.URL, strings.NewReader("all of it"))
	resp, err := sendWatched(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	read := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(resp.Body)
		read <- err
	}()
	select {
	case err := <-read:
		if !errors.Is(err, errNoAnswer) {
			t.Fatalf("a stalled answer ended with %v, want errNoAnswer", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a host that stopped mid-answer was waited on forever")
	}
}

// Every request of a push is signed when it goes, so a push longer than the
// hosts' 10-minute signature window still goes up; each signature is checked
// by a real host.
func TestEveryPushRequestIsSignedWhenItGoes(t *testing.T) {
	ctx := context.Background()
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var times []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			mu.Lock()
			times = append(times, r.Header.Get("X-Croptop-Time"))
			mu.Unlock()
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	batch, clock := pushBatch, pushClock
	pushBatch = 64 << 10 // several parts
	start := time.Now()
	var calls atomic.Int64
	pushClock = func() time.Time { return start.Add(time.Duration(calls.Add(1)) * time.Second) }
	t.Cleanup(func() { pushBatch, pushClock = batch, clock })

	laptop := offlineNode(t)
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	ipnsName, _ := laptop.Keystore().Generate(fixtureID)
	site, _ := s.Site(fixtureID)
	site.IPNS = ipnsName
	enableHosting(t, site)
	SetHost(site, srv.URL)
	s.SaveSite(site)
	p := &Publisher{Store: s, Node: laptop, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: laptop}}
	if err := p.Render.Render(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	c, err := laptop.AddDir(ctx, s.PublicDir(fixtureID))
	if err != nil {
		t.Fatal(err)
	}
	laptop.SignRecord(fixtureID, c, 1)
	if err := p.Push(ctx, site, c, 1); err != nil {
		t.Fatalf("push: %v", err) // the host checks each request's signature against its own time
	}
	if len(times) < 2 {
		t.Fatalf("the push went up in %d request(s); the fixture should split", len(times))
	}
	for i := 1; i < len(times); i++ {
		if times[i] <= times[i-1] {
			t.Fatalf("request %d was signed at %s, not after request %d at %s: a push signs once", i+1, times[i], i, times[i-1])
		}
	}
}

// A push with no file small enough to go in a part could never send the part
// that commits the version, so it is refused before anything goes up.
func TestAPushThatCannotCommitIsRefused(t *testing.T) {
	var posts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			io.Copy(io.Discard, r.Body)
		} else {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	batch := pushBatch
	pushBatch = 1 << 10
	t.Cleanup(func() { pushBatch = batch })
	p, s, _ := fakePublisher(t)
	site, _ := s.Site(fixtureID)
	SetHost(site, srv.URL)
	s.SaveSite(site)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "video.mp4"), bytes.Repeat([]byte("v"), 3<<10), 0o644)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("small"), 0o644)
	err := p.pushDir(context.Background(), site, fixtureID, "bafyNEW", 2, pushSpec{Parent: "bafyOLD", Files: []string{"video.mp4"}, Carry: []string{}, Dir: dir})
	if !errors.Is(err, errNoCommitFile) {
		t.Fatalf("a push of one big file gave %v, want errNoCommitFile", err)
	}
	if n := posts.Load(); n != 0 {
		t.Fatalf("%d requests went up before the push was refused", n)
	}
}

// With the embedded engine, a push that names files sends only those, read
// from the version's blocks.
func TestPushSendsOnlyTheNamedFilesOfAVersion(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var fields []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if strings.HasPrefix(part.FormName(), "file:") {
				mu.Lock()
				fields = append(fields, part.FormName())
				mu.Unlock()
			}
		}
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	laptop := offlineNode(t)
	dir := t.TempDir()
	for _, f := range []string{"a.html", "b.html", "c/d.html"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
		os.WriteFile(filepath.Join(dir, f), []byte(f), 0o644)
	}
	c, err := laptop.AddDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := laptop.Keystore().Generate(fixtureID)
	site := &store.Site{ID: fixtureID, IPNS: name}
	enableHosting(t, site)
	SetHost(site, srv.URL)
	p := &Publisher{Node: laptop}
	if err := p.pushDir(ctx, site, fixtureID, c, 1, pushSpec{Files: []string{"b.html", "c/d.html"}}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(fields) != "[file:b.html file:c/d.html]" {
		t.Fatalf("the host was sent %v, want only b.html and c/d.html", fields)
	}
}

type slowReader struct {
	n     int
	every time.Duration
}

func (r *slowReader) Read(b []byte) (int, error) {
	if r.n == 0 {
		return 0, io.EOF
	}
	time.Sleep(r.every)
	r.n--
	b[0] = 'x'
	return 1, nil
}

type stuckReader struct{ wait chan struct{} }

func (r *stuckReader) Read(b []byte) (int, error) {
	<-r.wait
	return 0, io.EOF
}

// A push sends the version's own bytes even when the folder changed after it
// was added, and a push cut short resumes: the retry skips what the host
// already holds.
func TestPushSendsTheVersionAndResumes(t *testing.T) {
	ctx := context.Background()
	stall, reply := stallAfter, replyWithin
	stallAfter, replyWithin = 10*time.Second, 10*time.Second // a leaked file fails fast, not after 5 minutes
	t.Cleanup(func() { stallAfter, replyWithin = stall, reply })
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	failLast := true
	var sent int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v0/host/push" {
			part := strings.SplitN(r.Header.Get("X-Croptop-Part"), "/", 2)
			mu.Lock()
			fail := failLast && len(part) == 2 && part[0] == part[1] && part[1] != "1" // the final part of a split push
			if fail {
				failLast = false
			}
			mu.Unlock()
			if fail {
				http.Error(w, "cut short", 500)
				return
			}
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			sent += int64(len(b))
			mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(b))
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	batch := pushBatch
	pushBatch = 64 << 10 // the fixture site splits into several parts
	t.Cleanup(func() { pushBatch = batch })

	laptop := offlineNode(t)
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	ipnsName, _ := laptop.Keystore().Generate(fixtureID)
	site, _ := s.Site(fixtureID)
	site.IPNS = ipnsName
	enableHosting(t, site)
	SetHost(site, srv.URL)
	s.SaveSite(site)
	p := &Publisher{Store: s, Node: laptop, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: laptop}}
	if err := p.Render.Render(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	c, err := laptop.AddDir(ctx, s.PublicDir(fixtureID))
	if err != nil {
		t.Fatal(err)
	}
	laptop.SignRecord(fixtureID, c, 1)
	// a render after the version was made must not leak into its upload
	os.WriteFile(filepath.Join(s.PublicDir(fixtureID), "index.html"), []byte("rendered later"), 0o644)

	if err := p.Push(ctx, site, c, 1); err == nil {
		t.Fatal("the cut-short push must fail")
	}
	mu.Lock()
	first := sent
	sent = 0
	mu.Unlock()
	if err := p.Push(ctx, site, c, 1); err != nil {
		t.Fatalf("retry: %v", err) // the host checks the files hash to c: a leaked file fails here
	}
	mu.Lock()
	retry := sent
	mu.Unlock()
	// resumed, the retry sends about one part (the one cut short); from scratch, the whole site
	if first <= retry || retry > pushBatch+(16<<10) {
		t.Fatalf("the retry sent %d bytes after %d went up the first time: it did not resume", retry, first)
	}
	e, err := hostEntry(ctx, srv.URL, ipnsName)
	if err != nil || e.CID != c || !e.AcceptsManifest {
		t.Fatalf("host entry %+v, %v", e, err)
	}
}

// The two limits are separate: a host busy with the last part of a big version
// may answer later than an upload may go without sending, and a body that has
// stopped is not given the time the host is allowed to answer in.
func TestUploadLimitsAreSeparate(t *testing.T) {
	stall, reply := stallAfter, replyWithin
	stallAfter, replyWithin = 200*time.Millisecond, 5*time.Second
	t.Cleanup(func() { stallAfter, replyWithin = stall, reply })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return // the upload was given up
		}
		time.Sleep(800 * time.Millisecond) // adding the files, checking their hash
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	send := func(body io.Reader) error {
		req, _ := http.NewRequest("POST", srv.URL, body)
		resp, err := sendWatched(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, resp.Body)
		return resp.Body.Close()
	}
	if err := send(strings.NewReader("all of it")); err != nil {
		t.Fatalf("an answer that took longer than the stall limit, within the reply limit, was given up: %v", err)
	}
	stuck := make(chan struct{})
	defer close(stuck)
	start := time.Now()
	if err := send(&stuckReader{wait: stuck}); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("a body that stopped was waited on for the reply limit: %v after %s", err, time.Since(start))
	}
}

// A retry skips the files the host holds at the same size, and when it holds
// them all it still sends one, so the part that commits the version goes.
func TestSkipHeldKeepsWhatTheHostLacks(t *testing.T) {
	files := []pushFile{{"a.html", 30}, {"b.html", 10}, {"c.html", 20}}
	rels := func(list []pushFile) string {
		var out []string
		for _, f := range list {
			out = append(out, f.rel)
		}
		return strings.Join(out, ",")
	}
	for _, tc := range []struct {
		name string
		have map[string]int64
		want string
	}{
		{"nothing held", map[string]int64{}, "a.html,b.html,c.html"},
		{"some held", map[string]int64{"a.html": 30, "c.html": 20}, "b.html"},
		{"held at another size", map[string]int64{"a.html": 31, "b.html": 10}, "a.html,c.html"},
		{"held but not in the version", map[string]int64{"z.html": 5}, "a.html,b.html,c.html"},
		{"all held: the smallest goes again", map[string]int64{"a.html": 30, "b.html": 10, "c.html": 20}, "b.html"},
	} {
		if got := rels(skipHeld(files, tc.have)); got != tc.want {
			t.Errorf("%s: sends %s, want %s", tc.name, got, tc.want)
		}
	}
	if got := skipHeld(nil, map[string]int64{"a.html": 1}); len(got) != 0 {
		t.Errorf("a version with no files sends %v", got)
	}
	// the file kept for the final part must fit in a part: a big file goes
	// in chunks, and chunks commit nothing
	batch := pushBatch
	pushBatch = 100
	t.Cleanup(func() { pushBatch = batch })
	withBig := []pushFile{{"big.mp4", 500}, {"a.html", 30}, {"b.html", 10}}
	for _, tc := range []struct {
		name  string
		files []pushFile
		have  map[string]int64
		want  string
	}{
		{"a big file left, the small ones held: the smallest small one goes again", withBig, map[string]int64{"a.html": 30, "b.html": 10}, "big.mp4,b.html"},
		{"all held: the smallest small one goes, not the big one", withBig, map[string]int64{"big.mp4": 500, "a.html": 30, "b.html": 10}, "b.html"},
		{"only big files: nothing small to add", []pushFile{{"big.mp4", 500}}, map[string]int64{"big.mp4": 499}, "big.mp4"},
	} {
		if got := rels(skipHeld(tc.files, tc.have)); got != tc.want {
			t.Errorf("%s: sends %s, want %s", tc.name, got, tc.want)
		}
	}
}

// A host that refuses a push because the site moved on says so in a type the
// caller can tell; any other refusal is a plain error.
func TestPushNamesAConflict(t *testing.T) {
	refusing := func(code int) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				http.NotFound(w, r) // it holds no files of this version yet
				return
			}
			io.Copy(io.Discard, r.Body)
			http.Error(w, "host holds bafyNEWER, not bafyOLD", code)
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	p, s, _ := fakePublisher(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.html"), []byte("a"), 0o644)
	push := func(url string) error {
		site, _ := s.Site(fixtureID)
		SetHost(site, url)
		s.SaveSite(site)
		return p.pushDir(context.Background(), site, fixtureID, "bafyNEW", 2, pushSpec{Parent: "bafyOLD", Dir: dir})
	}
	var conflict *hostConflict
	if err := push(refusing(409)); !errors.As(err, &conflict) || !strings.Contains(err.Error(), "host holds bafyNEWER, not bafyOLD") {
		t.Fatalf("a 409 gave %v", err)
	}
	if err := push(refusing(500)); err == nil || errors.As(err, &conflict) {
		t.Fatalf("a 500 gave %v", err)
	}
}

// What a push sends follows its spec: the files it names, the parent it builds
// on, and for a manifest push the carried paths, in the final part. A carry
// that is empty still makes a manifest push; no carry does not.
func TestPushSpecChoosesFilesParentAndManifest(t *testing.T) {
	type request struct {
		parent, part, manifest string
		fields                 []string
	}
	var mu sync.Mutex
	var got []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		req := request{parent: r.Header.Get("X-Croptop-Parent"), part: r.Header.Get("X-Croptop-Part")}
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FormName() == "manifest" {
				b, _ := io.ReadAll(part)
				req.manifest = string(b)
			} else {
				req.fields = append(req.fields, part.FormName())
			}
		}
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	p, s, _ := fakePublisher(t)
	site, _ := s.Site(fixtureID)
	SetHost(site, srv.URL)
	s.SaveSite(site)
	dir := t.TempDir()
	for _, f := range []string{"index.html", "other.html", "assets/site.css"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
		os.WriteFile(filepath.Join(dir, f), []byte(f), 0o644)
	}
	for _, spec := range []pushSpec{
		{Parent: "bafyOLD", Files: []string{"index.html", "assets/site.css"}, Carry: []string{"p1"}, Dir: dir},
		{Files: []string{"other.html"}, Carry: []string{}, Dir: dir},
		{Dir: dir},
	} {
		if err := p.pushDir(context.Background(), site, fixtureID, "bafyNEW", 2, spec); err != nil {
			t.Fatal(err)
		}
	}
	want := []request{
		{"bafyOLD", "1/1", `{"carry":["p1"]}`, []string{"file:assets/site.css", "file:index.html"}},
		{"", "1/1", `{"carry":[]}`, []string{"file:other.html"}},
		{"", "1/1", "", []string{"file:assets/site.css", "file:index.html", "file:other.html"}},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("the host was sent\n%+v\nwant\n%+v", got, want)
	}
}

// hostEntry says apart a host that holds no version of a site from one that
// cannot be asked, or does not answer sensibly.
func TestHostEntryNamesAMissingVersion(t *testing.T) {
	ctx := context.Background()
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/known") {
			w.Write([]byte(`{"cid":"","sequence":0}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer empty.Close()
	for _, name := range []string{"unknown", "known"} {
		if _, err := hostEntry(ctx, empty.URL, name); !errors.Is(err, errNoVersion) || !strings.Contains(err.Error(), "holds no version of "+name) {
			t.Errorf("%s: %v", name, err)
		}
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) }))
	defer broken.Close()
	if _, err := hostEntry(ctx, broken.URL, "k"); err == nil || errors.Is(err, errNoVersion) {
		t.Errorf("a host that fails: %v", err)
	}
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	if _, err := hostEntry(ctx, down.URL, "k"); err == nil || errors.Is(err, errNoVersion) {
		t.Errorf("a host that is down: %v", err)
	}
}
