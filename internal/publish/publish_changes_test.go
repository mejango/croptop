package publish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

// noPeers is an offline node whose announcements are not failures. A record
// it announces goes into its own DHT store, where NetworkRecord finds it
// again, and the put then fails for want of a peer to send it to ("failed to
// find any peer in table"): the one error an offline test node always gives.
// beforeAnnounce, if set, is called as AnnounceRecord begins, which is how a
// changes-only publish announces; NamePublish does not call it.
type noPeers struct {
	*ipfs.Embedded
	beforeAnnounce func(c string)
	failAnnounce   error // if set, AnnounceRecord fails with it
}

func (n *noPeers) NamePublish(ctx context.Context, key, c string, seq uint64) error {
	return storedLocally(n.Embedded.NamePublish(ctx, key, c, seq))
}

func (n *noPeers) AnnounceRecord(ctx context.Context, key, c string, rec []byte) error {
	if n.beforeAnnounce != nil {
		n.beforeAnnounce(c)
	}
	if n.failAnnounce != nil {
		return n.failAnnounce
	}
	return storedLocally(n.Embedded.AnnounceRecord(ctx, key, c, rec))
}

// racyNode runs during once, inside the laptop's NetworkRecord: after the
// publish has first read the host and before it reads it again.
type racyNode struct {
	*noPeers
	during func()
}

func (n *racyNode) NetworkRecord(ctx context.Context, name string) (*ipfs.Record, error) {
	if f := n.during; f != nil {
		n.during = nil
		f()
	}
	return n.noPeers.NetworkRecord(ctx, name)
}

func storedLocally(err error) error {
	if err != nil && strings.Contains(err.Error(), "failed to find any peer in table") {
		return nil
	}
	return err
}

// changesRig is a laptop that has published the fixture site once, and a real
// host that holds that version, behind a wrapper that counts the pushes it
// sees and can refuse them or lose their answers.
type changesRig struct {
	t      *testing.T
	srv    *httptest.Server
	laptop *noPeers
	store  *store.Store
	p      *Publisher
	ipns   string
	post   *store.Post // the post the tests edit
	// what the host held for each version as it was announced
	heldAtAnnounce map[string]bool

	mu        sync.Mutex
	sent      int64 // bytes of pushes seen since the last reset
	manifests int   // manifest pushes seen since the last reset
	refuse    int   // how many of the next manifest pushes to refuse (409)
	lose      int   // how many of the next to commit without telling the client: it gets a 409 naming its own version
	// beforeManifest, if set, runs once as the next manifest push arrives,
	// before the host sees it
	beforeManifest func()
}

func newChangesRig(t *testing.T) *changesRig {
	t.Helper()
	ctx := context.Background()
	r := &changesRig{t: t, heldAtAnnounce: map[string]bool{}}
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	r.laptop = &noPeers{Embedded: offlineNode(t)}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "POST" && req.URL.Path == "/v0/host/push" {
			b, _ := io.ReadAll(req.Body)
			r.mu.Lock()
			r.sent += int64(len(b))
			isManifest := bytes.Contains(b, []byte(`name="manifest"`))
			if isManifest {
				r.manifests++
			}
			doRefuse := isManifest && r.refuse > 0
			if doRefuse {
				r.refuse--
			}
			doLose := isManifest && !doRefuse && r.lose > 0
			if doLose {
				r.lose--
			}
			var hook func()
			if isManifest {
				hook, r.beforeManifest = r.beforeManifest, nil
			}
			r.mu.Unlock()
			if hook != nil {
				hook()
			}
			if doRefuse {
				http.Error(w, "the site changed during this push; post again", 409)
				return
			}
			req.Body = io.NopCloser(bytes.NewReader(b))
			if doLose {
				answer := httptest.NewRecorder()
				h.ServeHTTP(answer, req)
				if answer.Code != 200 {
					t.Errorf("the host did not commit the push whose answer was to be lost: %d %s", answer.Code, answer.Body)
				}
				http.Error(w, fmt.Sprintf("host holds %s, not %s", req.Header.Get("X-Croptop-Cid"), req.Header.Get("X-Croptop-Parent")), 409)
				return
			}
		}
		h.ServeHTTP(w, req)
	}))
	t.Cleanup(r.srv.Close)

	r.store = &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(r.store.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	var err error
	if r.ipns, err = r.laptop.Keystore().Generate(fixtureID); err != nil {
		t.Fatal(err)
	}
	site, err := r.store.Site(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	site.IPNS = r.ipns
	SetHost(site, r.srv.URL)
	if err := r.store.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	r.p = &Publisher{Store: r.store, Node: r.laptop, Render: &render.Renderer{Store: r.store, Templates: templates.FS, CIDs: r.laptop}, SkipPrewarm: true, Wait: true}
	r.laptop.beforeAnnounce = func(c string) {
		e, err := hostEntry(ctx, r.srv.URL, r.ipns)
		r.heldAtAnnounce[c] = err == nil && e.CID == c
	}
	// SkipPrewarm skips the background upload of the announce-first path, so the first version goes up by hand
	first, err := r.p.Publish(ctx, fixtureID, false)
	if err != nil {
		t.Fatal(err)
	}
	if site, err = r.store.Site(fixtureID); err != nil {
		t.Fatal(err)
	}
	if err := r.p.Push(ctx, site, first.CID, first.Sequence); err != nil {
		t.Fatal(err)
	}
	posts, err := r.store.Posts(fixtureID)
	if err != nil || len(posts) == 0 {
		t.Fatalf("the fixture's posts: %d, %v", len(posts), err)
	}
	r.post = posts[0]
	return r
}

// agent readies a machine with nothing but the site's key, as `croptop post
// --key` runs. The post it returns may be called from any goroutine: it adds
// a post titled title to the site through the host.
func (r *changesRig) agent() (post func(title string)) {
	r.t.Helper()
	pem, err := r.laptop.Keystore().ExportPEM(fixtureID)
	if err != nil {
		r.t.Fatal(err)
	}
	node := offlineNode(r.t)
	st := &store.Store{Root: r.t.TempDir()}
	ap := &Publisher{Store: st, Node: node, Render: &render.Renderer{Store: st, Templates: templates.FS, CIDs: node}}
	return func(title string) {
		if _, err := ap.Post(context.Background(), r.srv.URL, pem, NewPost{Title: title}); err != nil {
			r.t.Errorf("the agent's post %q: %v", title, err)
		}
	}
}

// keeps checks that the host holds res, the laptop's last publish; that it
// and the laptop's copy of the site both have the post titled title; and that
// the laptop's edit, text in the rig's post, is in it too.
func (r *changesRig) keeps(res Result, title, edit string) {
	r.t.Helper()
	e, err := r.entry()
	if err != nil || e.CID != res.CID {
		r.t.Fatalf("the host holds %+v, %v; the laptop published %+v", e, err, res)
	}
	a, status, err := httpGet(context.Background(), r.srv.URL+"/ipfs/"+e.CID+"/"+r.post.ID+"/article.json")
	if err != nil || status != 200 || !strings.Contains(string(a), edit) {
		r.t.Fatalf("the laptop's edit %q is not in the host's version: %d, %v", edit, status, err)
	}
	b, status, err := httpGet(context.Background(), r.srv.URL+"/ipfs/"+e.CID+"/planet.json")
	if err != nil || status != 200 {
		r.t.Fatalf("planet.json of %s: %d, %v", e.CID, status, err)
	}
	posts, err := r.store.Posts(fixtureID)
	if err != nil {
		r.t.Fatal(err)
	}
	local := false
	for _, p := range posts {
		local = local || p.Title == title
	}
	if onHost := strings.Contains(string(b), title); !local || !onHost {
		r.t.Fatalf("the post %q is lost: on the laptop %v, in the host's version %v", title, local, onHost)
	}
}

// edit adds text to the post, so the next publish has something to send.
func (r *changesRig) edit(text string) {
	r.t.Helper()
	r.post.Content += text
	if err := r.store.SavePost(fixtureID, r.post); err != nil {
		r.t.Fatal(err)
	}
}

// refuseNext makes the host refuse the next n manifest pushes.
func (r *changesRig) refuseNext(n int) {
	r.mu.Lock()
	r.refuse = n
	r.mu.Unlock()
}

// loseNext makes the host commit the next n manifest pushes and tell the client
// only what a retry would be told.
func (r *changesRig) loseNext(n int) {
	r.mu.Lock()
	r.lose = n
	r.mu.Unlock()
}

// reset starts the count of what the host is sent.
func (r *changesRig) reset() {
	r.mu.Lock()
	r.sent, r.manifests = 0, 0
	r.mu.Unlock()
}

// counts is what the host was sent since the last reset: bytes, and manifest pushes.
func (r *changesRig) counts() (int64, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent, r.manifests
}

// push publishes and says what the host was sent meanwhile.
func (r *changesRig) push() (int64, int, Result) {
	r.t.Helper()
	r.reset()
	res, err := r.p.Publish(context.Background(), fixtureID, false)
	if err != nil {
		r.t.Fatal(err)
	}
	sent, manifests := r.counts()
	return sent, manifests, res
}

// entry is the version the host holds.
func (r *changesRig) entry() (*hostKey, error) {
	return hostEntry(context.Background(), r.srv.URL, r.ipns)
}

// full is the size of the whole site as last rendered.
func (r *changesRig) full() int64 {
	var n int64
	filepath.WalkDir(r.store.PublicDir(fixtureID), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// A second publish sends only what changed, as a manifest on the host's
// version, announces it once the host holds it, and the host then holds exactly
// the new version. If the host refuses it because the site moved on, the
// publish takes that version in and tries again, at most twice. If the host
// committed the push and the answer was lost, so that all the publish meets is
// a refusal naming its own version as the one the host holds, that version is
// announced: it is no news to take in.
func TestPublishSendsOnlyWhatChanged(t *testing.T) {
	ctx := context.Background()
	r := newChangesRig(t)
	full := r.full()

	r.edit("\n\nedited")
	bytesSent, used, res := r.push()
	if used != 1 || bytesSent >= full/2 {
		t.Fatalf("a one-post edit sent %d bytes in %d manifest pushes; the whole site is %d", bytesSent, used, full)
	}
	if e, err := r.entry(); err != nil || e.CID != res.CID {
		t.Fatalf("host holds %+v, %v; published %s", e, err, res.CID)
	}
	if !r.heldAtAnnounce[res.CID] {
		t.Fatalf("%s was not announced after the host held it", res.CID)
	}

	r.refuseNext(1)
	r.edit(" again")
	_, used, res = r.push()
	if used != 2 {
		t.Fatalf("after a 409 the publish should push again once: %d manifest pushes", used)
	}
	if e, err := r.entry(); err != nil || e.CID != res.CID {
		t.Fatalf("after the retry the host holds %+v, %v; published %s", e, err, res.CID)
	}

	r.loseNext(1)
	r.edit(" once more")
	_, used, res = r.push()
	if used != 1 {
		t.Fatalf("a push the host had committed was sent %d times", used)
	}
	// the host's own record of the version is the one announced: the same sequence, not a newer one
	if e, err := r.entry(); err != nil || e.CID != res.CID || e.Sequence != res.Sequence {
		t.Fatalf("after the lost answer the host holds %+v, %v; published %+v", e, err, res)
	}
	if rec, err := r.laptop.NetworkRecord(ctx, r.ipns); err != nil || rec.Value != "/ipfs/"+res.CID || rec.Sequence != res.Sequence {
		t.Fatalf("after the lost answer the network holds %+v, %v; published %+v", rec, err, res)
	}

	// a host that refuses every push is given up on: the first push and two retries
	r.refuseNext(100)
	r.edit(" and again")
	r.reset()
	if _, err := r.p.Publish(ctx, fixtureID, false); !errors.Is(err, ErrSiteBusy) {
		t.Fatalf("a host that refused every push: %v, want ErrSiteBusy", err)
	}
	if _, used := r.counts(); used != 3 {
		t.Fatalf("a host that refused every push was pushed to %d times, want 3", used)
	}
}

// An agent's post that reaches the host while the laptop publishes is kept,
// on the host and on the laptop. It may land before the laptop's push, which
// the host then refuses, or between the laptop's two looks at the host, which
// the laptop must notice itself: an upload made without taking it in would
// replace the agent's version at the same sequence.
func TestAnAgentPostDuringAPublishIsKept(t *testing.T) {
	t.Run("before the push", func(t *testing.T) {
		r := newChangesRig(t)
		post := r.agent()
		r.mu.Lock()
		r.beforeManifest = func() { post("Agent wrote this first") }
		r.mu.Unlock()
		r.edit("\n\nlaptop edit")
		_, used, res := r.push()
		if used != 2 {
			t.Fatalf("the refused push was not made again once: %d manifest pushes", used)
		}
		r.keeps(res, "Agent wrote this first", "laptop edit")
	})
	t.Run("between the laptop's looks at the host", func(t *testing.T) {
		r := newChangesRig(t)
		post := r.agent()
		r.p.Node = &racyNode{noPeers: r.laptop, during: func() { post("Agent wrote this meanwhile") }}
		r.edit("\n\nlaptop edit")
		_, _, res := r.push()
		r.keeps(res, "Agent wrote this meanwhile", "laptop edit")
	})
}

// A version the host has committed is published, even if this machine's own
// announcement of it then fails: the host serves it and announces it too. It is
// recorded as this machine's latest, not reported as a failed publish.
func TestAnAnnounceFailureAfterTheHostCommitsIsNotAFailedPublish(t *testing.T) {
	r := newChangesRig(t)
	r.laptop.failAnnounce = errors.New("no peer to announce to")
	r.edit("\n\nedited")
	_, used, res := r.push() // fails the test on an error
	if used != 1 {
		t.Fatalf("%d manifest pushes, want 1", used)
	}
	if e, err := r.entry(); err != nil || e.CID != res.CID {
		t.Fatalf("the host holds %+v, %v; published %+v", e, err, res)
	}
	site, err := r.store.Site(fixtureID)
	if err != nil || site.LastPublishedCID == nil || *site.LastPublishedCID != res.CID || site.IPNSSequence != res.Sequence {
		t.Fatalf("the committed version is not recorded as published: %+v, %v", site, err)
	}
}

// A change too big to push inside Publish, which the console runs under its
// lock and a time limit, is announced first and uploaded after: the call sends
// the host nothing and still publishes. What counts is what would be uploaded,
// not the size of the site: an edit under the limit is pushed inside Publish.
func TestPublishUploadsBigChangesInTheBackground(t *testing.T) {
	ctx := context.Background()
	limit := syncPushMax
	t.Cleanup(func() { syncPushMax = limit })
	r := newChangesRig(t)

	// the site is over this limit, what a one-post edit uploads is not
	syncPushMax = r.full() / 2
	r.edit("\n\nsmall")
	if sent, used, res := r.push(); used != 1 {
		t.Fatalf("an edit under the limit sent %d bytes in %d manifest pushes, want it pushed inside Publish", sent, used)
	} else if e, err := r.entry(); err != nil || e.CID != res.CID {
		t.Fatalf("host holds %+v, %v; published %s", e, err, res.CID)
	}

	syncPushMax = 1
	before, err := r.entry()
	if err != nil {
		t.Fatal(err)
	}
	r.edit(" and some more")
	sent, used, res := r.push() // no error
	if sent != 0 || used != 0 {
		t.Fatalf("a change over the limit sent %d bytes in %d manifest pushes during Publish", sent, used)
	}
	if res.CID == before.CID || res.Sequence <= before.Sequence {
		t.Fatalf("published %+v on top of the host's %+v", res, before)
	}
	site, _ := r.store.Site(fixtureID)
	if site.LastPublishedCID == nil || *site.LastPublishedCID != res.CID || site.IPNSSequence != res.Sequence {
		t.Fatalf("the version is not recorded as published: %+v", site)
	}
	if rec, err := r.laptop.NetworkRecord(ctx, r.ipns); err != nil || rec.Value != "/ipfs/"+res.CID || rec.Sequence != res.Sequence {
		t.Fatalf("the network holds %+v, %v; published %+v", rec, err, res)
	}
	if e, err := r.entry(); err != nil || e.CID != before.CID {
		t.Fatalf("the host holds %+v, %v; it holds the old version until the upload follows", e, err)
	}
}

// While one upload runs, a newer version waits in its place and only the
// newest goes up after. A site whose host has no version is queued by the
// minute's catch-up.
func TestBackgroundPushesKeepOnlyTheNewest(t *testing.T) {
	ctx := context.Background()
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var mu sync.Mutex
	var cids []string
	blocked := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v0/host/push" {
			mu.Lock()
			if len(cids) == 0 || cids[len(cids)-1] != r.Header.Get("X-Croptop-Cid") {
				cids = append(cids, r.Header.Get("X-Croptop-Cid"))
			}
			wait := blocked
			mu.Unlock()
			if wait {
				<-release
			}
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	defer func() {
		mu.Lock()
		if blocked {
			blocked = false
			close(release)
		}
		mu.Unlock()
	}()

	laptop := offlineNode(t)
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	ipnsName, err := laptop.Keystore().Generate(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	site, err := s.Site(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	site.IPNS = ipnsName
	SetHost(site, srv.URL)
	if err := s.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	p := &Publisher{Store: s, Node: laptop, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: laptop}, SkipPrewarm: true}
	version := func(text string) (string, uint64) {
		t.Helper()
		posts, err := s.Posts(fixtureID)
		if err != nil {
			t.Fatal(err)
		}
		posts[0].Content = text
		if err := s.SavePost(fixtureID, posts[0]); err != nil {
			t.Fatal(err)
		}
		if err := p.Render.Render(ctx, fixtureID); err != nil {
			t.Fatal(err)
		}
		c, err := laptop.AddDir(ctx, s.PublicDir(fixtureID))
		if err != nil {
			t.Fatal(err)
		}
		site, err := s.Site(fixtureID)
		if err != nil {
			t.Fatal(err)
		}
		site.IPNSSequence++
		site.LastPublishedCID = &c
		if err := s.SaveSite(site); err != nil {
			t.Fatal(err)
		}
		if _, err := laptop.SignRecord(fixtureID, c, site.IPNSSequence); err != nil {
			t.Fatal(err)
		}
		return c, site.IPNSSequence
	}
	queue := func(c string, seq uint64) {
		t.Helper()
		site, err := s.Site(fixtureID)
		if err != nil {
			t.Fatal(err)
		}
		p.queuePush(site, c, seq)
	}
	a, sa := version("a")
	queue(a, sa)
	time.Sleep(200 * time.Millisecond) // a's upload is now blocked at the host
	b, sb := version("b")
	queue(b, sb)
	c, sc := version("c")
	queue(c, sc)
	mu.Lock()
	blocked = false
	close(release)
	mu.Unlock()
	waitFor(t, "the newest version reaching the host", func() bool {
		e, err := hostEntry(ctx, srv.URL, ipnsName)
		return err == nil && e.CID == c
	})
	mu.Lock()
	for _, x := range cids {
		if x == b {
			mu.Unlock()
			t.Fatalf("a version replaced while waiting was pushed anyway: %v", cids)
		}
	}
	mu.Unlock()

	// a host with no version of the site gets it from the minute's catch-up
	h2 := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h2.Start(); err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewServer(h2)
	defer srv2.Close()
	if site, err = s.Site(fixtureID); err != nil {
		t.Fatal(err)
	}
	SetHost(site, srv2.URL)
	if err := s.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	if err := p.CatchUp(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "catch-up sending the site to a host that had no version of it", func() bool {
		e, err := hostEntry(ctx, srv2.URL, ipnsName)
		return err == nil && e.CID == c
	})
}

// A background upload whose version another machine has published past (an
// agent's post) does not replace it, which the host refuses anyway: the
// laptop takes that version in and publishes again, so the host ends with both
// the agent's post and the laptop's edit.
func TestABackgroundUploadOvertakenElsewherePublishesAgain(t *testing.T) {
	ctx := context.Background()
	r := newChangesRig(t)
	post := r.agent()

	// the laptop makes a version at the next sequence that does not reach the
	// host yet, as a big change announced first would
	r.edit("\n\nlaptop edit")
	if err := r.p.Render.Render(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	mine, err := r.laptop.AddDir(ctx, r.store.PublicDir(fixtureID))
	if err != nil {
		t.Fatal(err)
	}
	site, err := r.store.Site(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	seq := site.IPNSSequence + 1
	if _, err := r.laptop.SignRecord(fixtureID, mine, seq); err != nil {
		t.Fatal(err)
	}
	if err := r.p.published(fixtureID, mine, seq); err != nil {
		t.Fatal(err)
	}
	// meanwhile an agent posts on the host's version, at the same sequence
	post("Agent wrote this meanwhile")
	agents, err := r.entry()
	if err != nil {
		t.Fatal(err)
	}
	if site, err = r.store.Site(fixtureID); err != nil {
		t.Fatal(err)
	}
	if err := r.p.pushVersion(ctx, &pushJob{site, mine, seq}); !errors.Is(err, errSuperseded) {
		t.Fatalf("an upload the host has moved past: %v, want errSuperseded", err)
	}
	r.p.queuePush(site, mine, seq)
	// taking the agent's version in makes it this machine's last publish for a
	// moment; what is waited for is the version published on top of it
	waitFor(t, "the laptop publishing again on top of the agent's version", func() bool {
		site, err := r.store.Site(fixtureID)
		e, herr := r.entry()
		return err == nil && herr == nil && site.LastPublishedCID != nil && *site.LastPublishedCID != mine &&
			*site.LastPublishedCID != agents.CID && e.CID == *site.LastPublishedCID
	})
	if site, err = r.store.Site(fixtureID); err != nil {
		t.Fatal(err)
	}
	r.keeps(Result{CID: *site.LastPublishedCID, Sequence: site.IPNSSequence}, "Agent wrote this meanwhile", "laptop edit")
}

// waitFor polls cond for up to 30 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !cond(); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}
