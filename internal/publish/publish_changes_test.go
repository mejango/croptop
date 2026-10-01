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

// A second publish sends only what changed, as a manifest on the host's
// version, announces it once the host holds it, and the host then holds exactly
// the new version. If the host refuses it because the site moved on, the
// publish takes that version in and tries again, at most twice. If the host
// committed the push and the answer was lost, so that all the publish meets is
// a refusal naming its own version as the one the host holds, that version is
// announced: it is no news to take in.
func TestPublishSendsOnlyWhatChanged(t *testing.T) {
	ctx := context.Background()
	h := &host.Host{Domain: "crop.test", DataDir: t.TempDir(), Engine: offlineNode(t)}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sent int64
	// the manifest pushes seen, and how many of the next ones to refuse (409),
	// and to commit without telling the client (it gets a 409 naming its own version)
	manifests, refuse, lose := 0, 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v0/host/push" {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			sent += int64(len(b))
			isManifest := bytes.Contains(b, []byte(`name="manifest"`))
			if isManifest {
				manifests++
			}
			doRefuse := isManifest && refuse > 0
			if doRefuse {
				refuse--
			}
			doLose := isManifest && !doRefuse && lose > 0
			if doLose {
				lose--
			}
			mu.Unlock()
			if doRefuse {
				http.Error(w, "the site changed during this push; post again", 409)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(b))
			if doLose {
				answer := httptest.NewRecorder()
				h.ServeHTTP(answer, r)
				if answer.Code != 200 {
					t.Errorf("the host did not commit the push whose answer was to be lost: %d %s", answer.Code, answer.Body)
				}
				http.Error(w, fmt.Sprintf("host holds %s, not %s", r.Header.Get("X-Croptop-Cid"), r.Header.Get("X-Croptop-Parent")), 409)
				return
			}
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	laptop := &noPeers{Embedded: offlineNode(t)}
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	ipnsName, _ := laptop.Keystore().Generate(fixtureID)
	site, _ := s.Site(fixtureID)
	site.IPNS = ipnsName
	SetHost(site, srv.URL)
	s.SaveSite(site)
	p := &Publisher{Store: s, Node: laptop, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: laptop}, SkipPrewarm: true, Wait: true}
	push := func() (int64, int, Result) {
		mu.Lock()
		sent, manifests = 0, 0
		mu.Unlock()
		res, err := p.Publish(ctx, fixtureID, false)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		return sent, manifests, res
	}
	// what the host held for each version as it was announced
	heldAtAnnounce := map[string]bool{}
	laptop.beforeAnnounce = func(c string) {
		e, err := hostEntry(ctx, srv.URL, ipnsName)
		heldAtAnnounce[c] = err == nil && e.CID == c
	}
	// SkipPrewarm skips the background upload of the announce-first path, so the first version goes up by hand
	first, err := p.Publish(ctx, fixtureID, false)
	if err != nil {
		t.Fatal(err)
	}
	site, _ = s.Site(fixtureID)
	if err := p.Push(ctx, site, first.CID, first.Sequence); err != nil {
		t.Fatal(err)
	}
	full := int64(0)
	filepath.WalkDir(s.PublicDir(fixtureID), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				full += info.Size()
			}
		}
		return nil
	})

	posts, _ := s.Posts(fixtureID)
	posts[0].Content = posts[0].Content + "\n\nedited"
	s.SavePost(fixtureID, posts[0])
	bytesSent, used, res := push()
	if used != 1 || bytesSent >= full/2 {
		t.Fatalf("a one-post edit sent %d bytes in %d manifest pushes; the whole site is %d", bytesSent, used, full)
	}
	if e, err := hostEntry(ctx, srv.URL, ipnsName); err != nil || e.CID != res.CID {
		t.Fatalf("host holds %+v, %v; published %s", e, err, res.CID)
	}
	if !heldAtAnnounce[res.CID] {
		t.Fatalf("%s was not announced after the host held it", res.CID)
	}

	mu.Lock()
	refuse = 1
	mu.Unlock()
	posts[0].Content = posts[0].Content + " again"
	s.SavePost(fixtureID, posts[0])
	_, used, res = push()
	if used != 2 {
		t.Fatalf("after a 409 the publish should push again once: %d manifest pushes", used)
	}
	if e, err := hostEntry(ctx, srv.URL, ipnsName); err != nil || e.CID != res.CID {
		t.Fatalf("after the retry the host holds %+v, %v; published %s", e, err, res.CID)
	}

	mu.Lock()
	lose = 1
	mu.Unlock()
	posts[0].Content = posts[0].Content + " once more"
	s.SavePost(fixtureID, posts[0])
	_, used, res = push()
	if used != 1 {
		t.Fatalf("a push the host had committed was sent %d times", used)
	}
	// the host's own record of the version is the one announced: the same sequence, not a newer one
	if e, err := hostEntry(ctx, srv.URL, ipnsName); err != nil || e.CID != res.CID || e.Sequence != res.Sequence {
		t.Fatalf("after the lost answer the host holds %+v, %v; published %+v", e, err, res)
	}
	if rec, err := laptop.NetworkRecord(ctx, ipnsName); err != nil || rec.Value != "/ipfs/"+res.CID || rec.Sequence != res.Sequence {
		t.Fatalf("after the lost answer the network holds %+v, %v; published %+v", rec, err, res)
	}

	// a host that refuses every push is given up on: the first push and two retries
	mu.Lock()
	refuse = 100
	mu.Unlock()
	posts[0].Content = posts[0].Content + " and again"
	s.SavePost(fixtureID, posts[0])
	mu.Lock()
	manifests = 0
	mu.Unlock()
	if _, err := p.Publish(ctx, fixtureID, false); err == nil || !strings.Contains(err.Error(), "keeps getting newer versions") {
		t.Fatalf("a host that refused every push: %v", err)
	}
	mu.Lock()
	used = manifests
	mu.Unlock()
	if used != 3 {
		t.Fatalf("a host that refused every push was pushed to %d times, want 3", used)
	}
}
