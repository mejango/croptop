package publish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
	"github.com/mejango/croptop/templates"
)

// Publishing never waits on the site's host. With the host hung (it takes the
// connection and never answers), a publish still returns within the host and
// network timeouts, and another peer resolves the name to the new version and
// fetches it from the publisher. Every later sub-project keeps this green.
func TestPublishingNeedsNoHost(t *testing.T) {
	for _, mode := range []string{store.StorageP2P, store.StorageHosted} {
		t.Run(mode, func(t *testing.T) { publishingNeedsNoHost(t, mode) })
	}
}

func publishingNeedsNoHost(t *testing.T, mode string) {
	t.Helper()
	ctx := context.Background()
	ht, nt := hostTimeout, networkTimeout
	hostTimeout, networkTimeout = 300*time.Millisecond, 2*time.Second
	t.Cleanup(func() { hostTimeout, networkTimeout = ht, nt })

	reached := make(chan struct{}, 16)
	release := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reached <- struct{}{}:
		default:
		}
		<-release
	}))
	defer hung.Close()
	defer close(release) // runs before Close, which waits for the handlers

	laptop, peer := offlineNode(t), offlineNode(t)
	if err := peer.Dial(ctx, laptop.Addrs()); err != nil {
		t.Fatal(err)
	}
	s := &store.Store{Root: t.TempDir()}
	if err := os.CopyFS(s.SiteDir(fixtureID), os.DirFS("../store/testdata/site")); err != nil {
		t.Fatal(err)
	}
	name, err := laptop.Keystore().Generate(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	site, err := s.Site(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	site.IPNS = name
	if err := site.SetStorage(mode); err != nil {
		t.Fatal(err)
	}
	SetHost(site, hung.URL)
	if err := s.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	p := &Publisher{Store: s, Node: laptop, Render: &render.Renderer{Store: s, Templates: templates.FS, CIDs: laptop}, SkipPrewarm: true}

	start := time.Now()
	res, err := p.Publish(ctx, fixtureID, false)
	if err != nil {
		t.Fatalf("publish with the host hung: %v", err)
	}
	// two host lookups (the newest record, then what changed) and one network lookup
	if took, limit := time.Since(start), 2*hostTimeout+networkTimeout+5*time.Second; took > limit {
		t.Fatalf("publish waited %s on a hung host, limit %s", took, limit)
	}
	select {
	case <-reached:
		if mode == store.StorageP2P {
			t.Fatal("P2P publishing contacted the configured host")
		}
	default:
		if mode == store.StorageHosted {
			t.Fatal("the opted-in publish never asked the host, so this test proves nothing")
		}
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	got, err := peer.Resolve(rctx, name)
	if err != nil || strings.TrimPrefix(got, "/ipfs/") != res.CID {
		t.Fatalf("the peer resolves %s to %q, %v; want %s", name, got, err, res.CID)
	}
	// Links reads over bitswap: the root comes from the publisher, the host has none
	if links, err := peer.Links(rctx, res.CID); err != nil || len(links) == 0 {
		t.Fatalf("the peer cannot fetch the version from the publisher: %d links, %v", len(links), err)
	}
}
