package publish

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/store"
)

// failNet makes every network lookup fail, as an unreachable DHT does.
type failNet struct{ *noPeers }

func (n *failNet) NetworkRecord(ctx context.Context, name string) (*ipfs.Record, error) {
	return nil, errors.New("dht lookup timed out")
}

// hostAhead says "published elsewhere" for a version this machine never
// published nor took in, whatever its sequence, and never for one of its own.
func TestHostAheadDecidesByVersionNotSequence(t *testing.T) {
	last := "bafylast"
	site := &store.Site{ID: fixtureID, IPNSSequence: 5, LastPublishedCID: &last}
	rememberVersion(site, "bafyolder")
	for _, tc := range []struct {
		name  string
		e     *hostKey
		extra []string
		want  bool
	}{
		{"this machine's last publish", &hostKey{CID: last, Sequence: 5}, nil, false},
		{"an older version of this machine, a host still uploading it", &hostKey{CID: "bafyolder", Sequence: 4}, nil, false},
		{"the version being published", &hostKey{CID: "bafynew", Sequence: 6}, []string{"bafynew"}, false},
		{"another machine's, above", &hostKey{CID: "bafyagent", Sequence: 6}, nil, true},
		{"another machine's, at the same sequence", &hostKey{CID: "bafyagent", Sequence: 5}, nil, true},
		{"another machine's, below (this machine's renewals raised its own)", &hostKey{CID: "bafyagent", Sequence: 2}, nil, true},
	} {
		if got := hostAhead(site, tc.e, tc.extra...); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// An agent that posts at the host's sequence + 1 can sit below this machine's
// own, which renewals raise every ten minutes without touching the host. Its
// version is still taken in, not uploaded over.
func TestAnAgentPostBelowThisMachinesSequenceIsTakenIn(t *testing.T) {
	ctx := context.Background()
	r := newChangesRig(t)
	post := r.agent()
	for i := 0; i < 2; i++ { // twenty minutes of the app running: two renewals
		if err := r.p.Keepalive(ctx, fixtureID); err != nil {
			t.Fatal(err)
		}
	}
	post("Agent post below the renewed sequence")
	agents, err := r.entry()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.p.CatchUp(ctx, fixtureID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // what a requeued upload would need to replace it
	e, err := r.entry()
	if err != nil || e.CID != agents.CID {
		t.Fatalf("the host holds %+v, %v; the agent's version %s was uploaded over", e, err, agents.CID)
	}
	posts, err := r.store.Posts(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range posts {
		if p.Title == "Agent post below the renewed sequence" {
			return
		}
	}
	t.Fatal("the agent's post was not taken in")
}

// A version of this machine's own that the host still holds (a newer one is
// uploading) is not taken in as if published elsewhere: the next publish goes
// above the sequence this machine announced, never below it.
func TestATakeInNeverLowersTheSequence(t *testing.T) {
	ctx := context.Background()
	r := newChangesRig(t)
	mine, seq := r.announceWithoutUpload("\n\nbig change")
	r.p.Node = &failNet{r.laptop}
	r.edit(" small edit")
	res, err := r.p.Publish(ctx, fixtureID, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sequence <= seq || res.CID == mine {
		t.Fatalf("published %+v, after this machine announced %s at %d", res, mine, seq)
	}
}

// After renewals, the same: and the older version's queued upload, which this
// machine's newer publish replaced, does not go up over it.
func TestAQueuedUploadNeverRevertsANewerPublish(t *testing.T) {
	ctx := context.Background()
	r := newChangesRig(t)
	for i := 0; i < 2; i++ {
		if err := r.p.Keepalive(ctx, fixtureID); err != nil {
			t.Fatal(err)
		}
	}
	old, err := r.entry()
	if err != nil {
		t.Fatal(err)
	}
	mine, seq := r.announceWithoutUpload("\n\nbig change")
	r.p.Node = &failNet{r.laptop}
	r.edit(" SMALL-EDIT")
	res, err := r.p.Publish(ctx, fixtureID, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sequence <= seq {
		t.Fatalf("published at %d, under the %d this machine announced", res.Sequence, seq)
	}
	r.p.Node = r.laptop
	if err := r.p.pushVersion(ctx, r.job(mine, seq)); err != nil {
		t.Fatalf("the replaced version's upload: %v", err)
	}
	if e, err := r.entry(); err != nil || e.CID != old.CID {
		t.Fatalf("the replaced version went up: the host holds %+v, %v", e, err)
	}
	if err := r.p.pushVersion(ctx, r.job(res.CID, res.Sequence)); err != nil {
		t.Fatal(err)
	}
	e, err := r.entry()
	if err != nil || e.CID != res.CID {
		t.Fatalf("the host holds %+v, %v; want %s", e, err, res.CID)
	}
}

// A background upload overtaken elsewhere does not publish what the user only
// saved: with edits saved since the version was published, the other version
// is taken in and nothing is published until the user publishes.
func TestAnOvertakenUploadDoesNotPublishSavedEdits(t *testing.T) {
	ctx := context.Background()
	r := newChangesRig(t)
	post := r.agent()
	mine, seq := r.announceWithoutUpload("\n\nlaptop edit")
	r.edit(" UNPUBLISHED-DRAFT-TEXT") // Save, not Publish
	post("Agent wrote this meanwhile")
	agents, err := r.entry()
	if err != nil {
		t.Fatal(err)
	}
	site, err := r.store.Site(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	r.p.queuePush(site, mine, seq, nil)
	waitFor(t, "the agent's post taken in", func() bool {
		posts, err := r.store.Posts(fixtureID)
		if err != nil {
			return false
		}
		for _, p := range posts {
			if p.Title == "Agent wrote this meanwhile" {
				return true
			}
		}
		return false
	})
	time.Sleep(time.Second) // what a publish would need to go up
	e, err := r.entry()
	if err != nil || e.CID != agents.CID {
		t.Fatalf("the host holds %+v, %v; want the agent's %s, untouched", e, err, agents.CID)
	}
	a, _, err := httpGet(ctx, r.srv.URL+"/ipfs/"+e.CID+"/"+r.post.ID+"/article.json")
	if err != nil || strings.Contains(string(a), "UNPUBLISHED-DRAFT-TEXT") {
		t.Fatalf("a saved-only edit is live on the host: %v", err)
	}
}

// A host that refuses the manifest push (400) is sent the whole version.
func TestARefusedManifestPushFallsBackToAWholePush(t *testing.T) {
	ctx := context.Background()
	r := newChangesRig(t)
	var mu sync.Mutex
	var manifests, plain int
	inner := r.srv.Config.Handler
	r.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "POST" && req.URL.Path == "/v0/host/push" {
			b, _ := io.ReadAll(req.Body)
			mu.Lock()
			if strings.Contains(string(b), `name="manifest"`) {
				manifests++
				mu.Unlock()
				http.Error(w, "the files and carried paths make bafyX, not bafyY", 400)
				return
			}
			plain++
			mu.Unlock()
			req.Body = io.NopCloser(strings.NewReader(string(b)))
		}
		inner.ServeHTTP(w, req)
	})
	mine, seq := r.announceWithoutUpload("\n\nedit")
	if err := r.p.pushVersion(ctx, r.job(mine, seq)); err != nil {
		t.Fatalf("the upload: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if manifests != 1 || plain == 0 {
		t.Fatalf("%d manifest pushes and %d whole pushes, want 1 refused and then the whole version", manifests, plain)
	}
	if e, err := r.entry(); err != nil || e.CID != mine {
		t.Fatalf("the host holds %+v, %v; want %s", e, err, mine)
	}
}

// A host that answers lookups at once but takes uploads slowly (a slow uplink
// looks the same) does not keep a publish from happening within the caller's
// deadline: the push inside Publish gives up and the version is announced, its
// upload left to the background.
func TestASlowPushStillPublishes(t *testing.T) {
	r := newChangesRig(t)
	inner := r.srv.Config.Handler
	r.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "POST" && req.URL.Path == "/v0/host/push" {
			buf := make([]byte, 512)
			for { // a trickle: progress every 50 ms, so the stall watchdog never fires
				if _, err := req.Body.Read(buf); err != nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			http.Error(w, "too slow to matter", 500)
			return
		}
		inner.ServeHTTP(w, req)
	})
	r.edit("\n\nedit on a slow link")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := r.p.Publish(ctx, fixtureID, false)
	if err != nil {
		t.Fatalf("publish on a slow link: %v", err)
	}
	rec, err := r.laptop.NetworkRecord(context.Background(), r.ipns)
	if err != nil || rec.Value != "/ipfs/"+res.CID {
		t.Fatalf("the network holds %+v, %v; published %s", rec, err, res.CID)
	}
}

// announceWithoutUpload makes this machine publish a version that does not
// reach the host, as a big change announced first does while it uploads.
func (r *changesRig) announceWithoutUpload(edit string) (string, uint64) {
	r.t.Helper()
	ctx := context.Background()
	r.edit(edit)
	if err := r.p.Render.Render(ctx, fixtureID); err != nil {
		r.t.Fatal(err)
	}
	mine, err := r.laptop.AddDir(ctx, r.store.PublicDir(fixtureID))
	if err != nil {
		r.t.Fatal(err)
	}
	site, err := r.store.Site(fixtureID)
	if err != nil {
		r.t.Fatal(err)
	}
	seq := site.IPNSSequence + 1
	if err := r.laptop.NamePublish(ctx, fixtureID, mine, seq); err != nil {
		r.t.Fatal(err)
	}
	if err := r.p.published(fixtureID, mine, seq); err != nil {
		r.t.Fatal(err)
	}
	return mine, seq
}

// job is the queued upload of version c at seq, as the site stands now.
func (r *changesRig) job(c string, seq uint64) *pushJob {
	r.t.Helper()
	site, err := r.store.Site(fixtureID)
	if err != nil {
		r.t.Fatal(err)
	}
	return &pushJob{site: site, cid: c, seq: seq}
}
