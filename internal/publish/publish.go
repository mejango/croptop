// Package publish renders a site, adds it to IPFS, and updates its IPNS name
// with sequence numbers taken from the network, so a site can be published
// from whichever machine currently holds its key.
package publish

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

const (
	networkTimeout = 25 * time.Second
	publishTimeout = 3 * time.Minute
)

type Publisher struct {
	Store  *store.Store
	Node   *ipfs.Node
	Render *render.Renderer
	Log    func(string)
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
	nctx, cancel := context.WithTimeout(ctx, networkTimeout)
	rec, netErr := p.Node.NetworkRecord(nctx, site.IPNS)
	cancel()
	if netErr == nil {
		p.log("network has sequence %d -> %s", rec.Sequence, rec.Value)
	} else {
		p.log("network record: %v", netErr)
	}
	seq, err := nextSequence(site.IPNSSequence, deref(site.LastPublishedCID), rec, netErr, force)
	if err != nil {
		if err == ErrPublishedElsewhere {
			site.PublishedElsewhere = true
			p.Store.SaveSite(site)
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
	site.IPNSSequence = seq
	site.LastPublishedCID = &cid
	site.LastPublished = &now
	site.PublishedElsewhere = false
	if err := p.Store.SaveSite(site); err != nil {
		return Result{}, err
	}
	return Result{CID: cid, Sequence: seq}, nil
}

// Keepalive republishes the current CID so the record does not expire, but
// only while this machine still owns the name.
func (p *Publisher) Keepalive(ctx context.Context, siteID string) error {
	site, err := p.Store.Site(siteID)
	if err != nil {
		return err
	}
	if site.LastPublishedCID == nil || !p.Node.Keystore().Has(site.ID) {
		return nil
	}
	if site.PublishedElsewhere && site.IPNSSequence > 0 {
		// flagged after we had published: leave the name alone until the user syncs,
		// but keep checking whether the other machine went quiet and the record is ours again
	}
	nctx, cancel := context.WithTimeout(ctx, networkTimeout)
	rec, err := p.Node.NetworkRecord(nctx, site.IPNS)
	cancel()
	if err != nil {
		return err // offline: try again next time
	}
	if rec.Value != "/ipfs/"+*site.LastPublishedCID {
		if site.IPNSSequence == 0 {
			// never published from here: the network's record is the baseline
			cid := strings.TrimPrefix(rec.Value, "/ipfs/")
			site.LastPublishedCID = &cid
			site.IPNSSequence = rec.Sequence
			site.PublishedElsewhere = false
			return p.Store.SaveSite(site)
		}
		if rec.Sequence >= site.IPNSSequence {
			p.log("%s is now published from another machine (sequence %d)", site.Name, rec.Sequence)
			site.PublishedElsewhere = true
			return p.Store.SaveSite(site)
		}
		return nil // network is behind us; the DHT will catch up
	}
	site.PublishedElsewhere = false // the network serves our CID; nobody else is publishing
	seq := max(rec.Sequence, site.IPNSSequence) + 1
	pctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	if err := p.Node.NamePublish(pctx, site.ID, *site.LastPublishedCID, seq); err != nil {
		return err
	}
	site.IPNSSequence = seq
	return p.Store.SaveSite(site)
}

// RunKeepalive loops over all sites every interval until ctx ends.
func (p *Publisher) RunKeepalive(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sites, _ := p.Store.Sites()
			for _, s := range sites {
				if err := p.Keepalive(ctx, s.ID); err != nil {
					p.log("keepalive %s: %v", s.Name, err)
				}
			}
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
