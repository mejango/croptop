package publish

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
}

func (n *noPeers) NamePublish(ctx context.Context, key, c string, seq uint64) error {
	return storedLocally(n.Embedded.NamePublish(ctx, key, c, seq))
}

func (n *noPeers) AnnounceRecord(ctx context.Context, key, c string, rec []byte) error {
	if n.beforeAnnounce != nil {
		n.beforeAnnounce(c)
	}
	return storedLocally(n.Embedded.AnnounceRecord(ctx, key, c, rec))
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
			r.mu.Unlock()
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
	r.ipns, _ = r.laptop.Keystore().Generate(fixtureID)
	site, _ := r.store.Site(fixtureID)
	site.IPNS = r.ipns
	SetHost(site, r.srv.URL)
	r.store.SaveSite(site)
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
	site, _ = r.store.Site(fixtureID)
	if err := r.p.Push(ctx, site, first.CID, first.Sequence); err != nil {
		t.Fatal(err)
	}
	posts, _ := r.store.Posts(fixtureID)
	r.post = posts[0]
	return r
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
	if _, err := r.p.Publish(ctx, fixtureID, false); err == nil || !strings.Contains(err.Error(), "keeps getting newer versions") {
		t.Fatalf("a host that refused every push: %v", err)
	}
	if _, used := r.counts(); used != 3 {
		t.Fatalf("a host that refused every push was pushed to %d times, want 3", used)
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
