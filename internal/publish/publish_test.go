package publish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

func TestNextSequence(t *testing.T) {
	rec := func(seq uint64, cid string) *ipfs.Record { return &ipfs.Record{Sequence: seq, Value: "/ipfs/" + cid} }
	cases := []struct {
		name    string
		local   uint64
		lastCID string
		net     *ipfs.Record
		netErr  error
		force   bool
		want    uint64
		wantErr error
	}{
		{"first ever", 0, "", nil, ipfs.ErrNoRecord, false, 1, nil},
		{"normal", 5, "bafyA", rec(5, "bafyA"), nil, false, 6, nil},
		{"network ahead but ours", 3, "bafyA", rec(7, "bafyA"), nil, false, 8, nil},
		{"published elsewhere", 3, "bafyA", rec(7, "bafyB"), nil, false, 0, ErrPublishedElsewhere},
		{"elsewhere but forced", 3, "bafyA", rec(7, "bafyB"), nil, true, 8, nil},
		{"offline with history", 4, "bafyA", nil, errors.New("timeout"), false, 5, nil},
		{"fresh install offline", 0, "bafyA", nil, errors.New("timeout"), false, 0, ErrWouldResetSequence},
		{"fresh import, stale DHT record", 0, "bafyA", rec(7, "bafyOLD"), nil, false, 8, nil},
		{"fresh install offline forced", 0, "bafyA", nil, errors.New("timeout"), true, 1, nil},
		{"adopted: network known, no local cid", 59, "", rec(59, "bafyX"), nil, false, 60, nil},
	}
	for _, c := range cases {
		got, err := nextSequence(c.local, c.lastCID, c.net, c.netErr, c.force)
		if !errors.Is(err, c.wantErr) || got != c.want {
			t.Errorf("%s: got %d %v want %d %v", c.name, got, err, c.want, c.wantErr)
		}
	}
}

const fixtureID = "FF5F456D-904F-4EE6-8BB5-AD175C65319A"

// fakePublisher wires a Publisher to testdata/fake-ipfs, a shell script that
// logs every call and answers like kubo.
func fakePublisher(t *testing.T) (*Publisher, *store.Store, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake ipfs is a shell script")
	}
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, "sites", fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	s := &store.Store{Root: root}
	log := filepath.Join(root, "calls.log")
	t.Setenv("FAKE_LOG", log)
	script, _ := filepath.Abs("testdata/fake-ipfs")
	node := ipfs.NewNode(script, filepath.Join(root, "ipfs"))
	if _, err := node.Keystore().Generate(fixtureID); err != nil {
		t.Fatal(err)
	}
	r := &render.Renderer{Store: s, Templates: templates.FS, CIDs: node}
	return &Publisher{Store: s, Node: node, Render: r, SkipPrewarm: true}, s, log
}

func TestPublishUsesNetworkSequence(t *testing.T) {
	p, s, log := fakePublisher(t)
	site, _ := s.Site(fixtureID)
	cid := "bafyOLD"
	site.LastPublishedCID = &cid
	site.IPNSSequence = 2
	s.SaveSite(site)

	res, err := p.Publish(context.Background(), fixtureID, false)
	if err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "--sequence=8") {
		t.Fatalf("expected --sequence=8 in calls:\n%s", calls)
	}
	if res.Sequence != 8 || res.CID != "bafyFAKE" {
		t.Fatalf("result %+v", res)
	}
	site, _ = s.Site(fixtureID)
	if site.IPNSSequence != 8 || *site.LastPublishedCID != "bafyFAKE" || site.PublishedElsewhere {
		t.Fatalf("site not updated: %+v", site)
	}
}

func TestPublishRefusesWhenElsewhere(t *testing.T) {
	p, s, log := fakePublisher(t)
	site, _ := s.Site(fixtureID)
	cid := "bafyMINE"
	site.LastPublishedCID = &cid
	site.IPNSSequence = 2
	s.SaveSite(site)

	_, err := p.Publish(context.Background(), fixtureID, false)
	if !errors.Is(err, ErrPublishedElsewhere) {
		t.Fatalf("got %v", err)
	}
	calls, _ := os.ReadFile(log)
	if strings.Contains(string(calls), "name publish") {
		t.Fatalf("must not publish:\n%s", calls)
	}
	site, _ = s.Site(fixtureID)
	if !site.PublishedElsewhere {
		t.Fatal("publishedElsewhere not flagged")
	}
	if _, err := p.Publish(context.Background(), fixtureID, true); err != nil {
		t.Fatalf("force should publish: %v", err)
	}
}

func TestKeepaliveBaselinesFreshImport(t *testing.T) {
	p, s, _ := fakePublisher(t)
	site, _ := s.Site(fixtureID)
	cid := "bafyIMPORTED"
	site.LastPublishedCID = &cid
	site.IPNSSequence = 0 // imported, never published from here; network says 7 -> bafyOLD
	s.SaveSite(site)
	if err := p.Keepalive(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	site, _ = s.Site(fixtureID)
	if site.PublishedElsewhere || site.IPNSSequence != 7 || *site.LastPublishedCID != "bafyOLD" {
		t.Fatalf("fresh import should adopt the network baseline: %+v", site)
	}
}

func TestKeepaliveDetectsTakeover(t *testing.T) {
	p, s, log := fakePublisher(t)
	site, _ := s.Site(fixtureID)
	cid := "bafyMINE"
	site.LastPublishedCID = &cid
	site.IPNSSequence = 2 // network says 7 -> bafyOLD, someone else's
	s.SaveSite(site)
	if err := p.Keepalive(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	site, _ = s.Site(fixtureID)
	if !site.PublishedElsewhere {
		t.Fatal("keepalive should flag takeover")
	}
	calls, _ := os.ReadFile(log)
	if strings.Contains(string(calls), "name publish") {
		t.Fatal("keepalive must not fight the other machine")
	}

	// our own record: republish at seq+1
	old := "bafyOLD"
	site.LastPublishedCID = &old
	site.PublishedElsewhere = false
	s.SaveSite(site)
	if err := p.Keepalive(context.Background(), fixtureID); err != nil {
		t.Fatal(err)
	}
	calls, _ = os.ReadFile(log)
	if !strings.Contains(string(calls), "--sequence=8") {
		t.Fatalf("keepalive should republish at 8:\n%s", calls)
	}
}
