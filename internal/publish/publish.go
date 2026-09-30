// Package publish renders a site, adds it to IPFS, and updates its IPNS name
// with sequence numbers taken from the network, so a site can be published
// from whichever machine currently holds its key.
package publish

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

const (
	networkTimeout = 25 * time.Second
	hostTimeout    = 15 * time.Second
	publishTimeout = 3 * time.Minute
)

type Publisher struct {
	Store  *store.Store
	Node   ipfs.Engine
	Render *render.Renderer
	Log    func(string)
	// SkipPrewarm disables the gateway warm-up after publishing (tests).
	SkipPrewarm bool
	// Wait runs the push and gateway warm-up before Publish returns instead
	// of in the background; the command line sets it so the process does
	// not exit with the upload half done.
	Wait bool
	// mu serializes Publish and Keepalive: both read the site, spend tens of
	// seconds on the network, then save it. Interleaved, the keepalive saved
	// a stale copy over a fresh publish and flagged the site as published
	// elsewhere (CROPTOP, 2026-09-19).
	mu sync.Mutex
}

func (p *Publisher) log(format string, a ...any) {
	if p.Log != nil {
		p.Log(fmt.Sprintf(format, a...))
	}
}

type Result struct {
	CID      string
	Sequence uint64
}

// Publish renders, adds, and publishes one site.
func (p *Publisher) Publish(ctx context.Context, siteID string, force bool) (Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	site, err := p.Store.Site(siteID)
	if err != nil {
		return Result{}, err
	}
	if !p.Node.Keystore().Has(site.ID) {
		return Result{}, fmt.Errorf("no IPNS key for %s on this machine; run `croptop key import %s <file.pem>`", site.Name, site.ID)
	}
	p.log("rendering %s", site.Name)
	if err := p.Render.Render(ctx, siteID); err != nil {
		return Result{}, fmt.Errorf("render: %w", err)
	}
	p.log("adding to IPFS")
	cid, err := p.Node.AddDir(ctx, p.Store.PublicDir(siteID))
	if err != nil {
		return Result{}, err
	}
	site, _ = p.Store.Site(siteID) // render may have changed post files, not the site, but reload anyway
	rec, netErr := p.latestRecord(ctx, site)
	if netErr == nil {
		p.log("network has sequence %d -> %s", rec.Sequence, rec.Value)
	} else {
		p.log("network record: %v", netErr)
	}
	seq, err := nextSequence(site.IPNSSequence, deref(site.LastPublishedCID), rec, netErr, force)
	if err != nil {
		if err == ErrPublishedElsewhere {
			p.saveSite(site.ID, func(s *store.Site) { s.PublishedElsewhere = true })
		}
		return Result{}, err
	}
	p.log("publishing %s at sequence %d", cid, seq)
	pctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	if err := p.Node.NamePublish(pctx, site.ID, cid, seq); err != nil {
		return Result{}, err
	}
	now := store.Now()
	if err := p.saveSite(site.ID, func(s *store.Site) {
		s.IPNSSequence, s.LastPublishedCID, s.LastPublished, s.PublishedElsewhere = seq, &cid, &now, false
	}); err != nil {
		return Result{}, err
	}
	if !p.SkipPrewarm {
		p.log("asking gateways to fetch the new version")
		after := func() {
			pctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			if err := p.Push(pctx, site, cid, seq); err != nil {
				p.log("push to %s failed: %v", HostOf(site), err)
			} else {
				p.log("pushed %s to %s", site.Name, HostOf(site))
			}
			cancel()
			p.prewarm(context.Background(), site, cid)
			p.prewarmAll(site, cid)
		}
		if p.Wait {
			after()
		} else {
			go after()
		}
	}
	return Result{CID: cid, Sequence: seq}, nil
}

// Keepalive republishes the current CID so the record does not expire. When
// another machine has published since, it takes that version in instead.
func (p *Publisher) Keepalive(ctx context.Context, siteID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	site, err := p.Store.Site(siteID)
	if err != nil {
		return err
	}
	if site.LastPublishedCID == nil || !p.Node.Keystore().Has(site.ID) {
		return nil
	}
	rec, err := p.latestRecord(ctx, site)
	if err != nil {
		return err // offline: try again next time
	}
	if rec.Value != "/ipfs/"+*site.LastPublishedCID {
		if site.IPNSSequence == 0 {
			// never published from here: the network's record is the baseline
			cid := strings.TrimPrefix(rec.Value, "/ipfs/")
			return p.saveSite(site.ID, func(s *store.Site) {
				s.LastPublishedCID, s.IPNSSequence, s.PublishedElsewhere = &cid, rec.Sequence, false
			})
		}
		if rec.Sequence >= site.IPNSSequence {
			return p.takeIn(ctx, site, rec)
		}
		return nil // network is behind us; the DHT will catch up
	}
	seq := max(rec.Sequence, site.IPNSSequence) + 1
	pctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	if err := p.Node.NamePublish(pctx, site.ID, *site.LastPublishedCID, seq); err != nil {
		return err
	}
	// the network serves our CID; nobody else is publishing
	return p.saveSite(site.ID, func(s *store.Site) { s.IPNSSequence, s.PublishedElsewhere = seq, false })
}

// CatchUp takes in what another machine published, if the site's host has
// it. It costs one small request, so it runs every minute, and a post made
// elsewhere, by an agent say, shows up here within a minute.
func (p *Publisher) CatchUp(ctx context.Context, siteID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	site, err := p.Store.Site(siteID)
	if err != nil {
		return err
	}
	if site.LastPublishedCID == nil || site.PublishedElsewhere || !p.Node.Keystore().Has(site.ID) {
		return nil // a failed take-in waits for the next Keepalive, or Sync
	}
	hctx, cancel := context.WithTimeout(ctx, hostTimeout)
	e, err := hostEntry(hctx, HostOf(site), site.IPNS)
	cancel()
	if err != nil || e.CID == *site.LastPublishedCID || e.Sequence < site.IPNSSequence {
		return nil // nothing newer there, or our own push is still on its way
	}
	return p.takeIn(ctx, site, &ipfs.Record{Value: "/ipfs/" + e.CID, Sequence: e.Sequence})
}

// takeIn pulls a version another machine published and renders the result,
// so this copy stays current without anyone pressing Sync. If that fails, the
// site is marked published elsewhere and Sync is left to the owner.
func (p *Publisher) takeIn(ctx context.Context, site *store.Site, rec *ipfs.Record) error {
	added, updated, err := p.pull(ctx, site, rec)
	if err != nil {
		p.log("%s was published from another machine (sequence %d); taking it in failed: %v", site.Name, rec.Sequence, err)
		return p.saveSite(site.ID, func(s *store.Site) { s.PublishedElsewhere = true })
	}
	p.log("%s: took in %d new and %d updated posts published from another machine", site.Name, added, updated)
	if added+updated > 0 {
		if err := p.Render.Render(ctx, site.ID); err != nil {
			p.log("render %s: %v", site.Name, err)
		}
	}
	return nil
}

// RunKeepalive renews every site each interval and, every minute in between,
// has each site catch up with its host, until ctx ends.
func (p *Publisher) RunKeepalive(ctx context.Context, every time.Duration) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	due := time.Now().Add(every)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		renew := !time.Now().Before(due)
		if renew {
			due = time.Now().Add(every)
		}
		sites, _ := p.Store.Sites()
		for _, s := range sites {
			var err error
			if renew {
				err = p.Keepalive(ctx, s.ID)
			} else {
				err = p.CatchUp(ctx, s.ID)
			}
			if err != nil {
				p.log("keepalive %s: %v", s.Name, err)
			}
		}
	}
}

// latestRecord is the newest version the network or the site's host knows.
// The host learns of a version the moment it is pushed, before the DHT does,
// which is how a post made elsewhere with `post --key` is seen here at once.
func (p *Publisher) latestRecord(ctx context.Context, site *store.Site) (*ipfs.Record, error) {
	hctx, cancel := context.WithTimeout(ctx, hostTimeout)
	e, herr := hostEntry(hctx, HostOf(site), site.IPNS)
	cancel()
	nctx, cancel := context.WithTimeout(ctx, networkTimeout)
	rec, err := p.Node.NetworkRecord(nctx, site.IPNS)
	cancel()
	if herr == nil && (err != nil || e.Sequence >= rec.Sequence) {
		return &ipfs.Record{Value: "/ipfs/" + e.CID, Sequence: e.Sequence}, nil
	}
	return rec, err
}

// saveSite applies change to the site as stored now rather than to a copy
// read before a network round trip, so a settings edit made meanwhile in the
// console is kept.
func (p *Publisher) saveSite(siteID string, change func(*store.Site)) error {
	site, err := p.Store.Site(siteID)
	if err != nil {
		return err
	}
	change(site)
	return p.Store.SaveSite(site)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ProvideAll re-announces every owned site's last published root. DHT
// provider records expire after 48 hours, so serve loops call this often.
func (p *Publisher) ProvideAll(ctx context.Context) {
	sites, _ := p.Store.Sites()
	for _, s := range sites {
		if s.LastPublishedCID == nil {
			continue
		}
		if err := p.Node.Provide(ctx, *s.LastPublishedCID); err != nil {
			p.log("provide %s: %v", s.Name, err)
		}
	}
}
