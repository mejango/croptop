// Package publish renders a site, adds it to IPFS, and updates its IPNS name
// with sequence numbers taken from the network, so a site can be published
// from whichever machine currently holds its key.
package publish

import (
	"context"
	"errors"
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

// syncPushMax is the most a publish sends the host before it announces, in
// bytes of the files that changed: about 75 s at 230 KB/s. A bigger change is
// announced first and uploaded in the background, so that a big upload never
// holds Publish, which the console runs under its lock and a time limit. A
// variable so that tests can lower it.
var syncPushMax int64 = 16 << 20

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
	// pushMu guards pending and pushing: one background upload runs per site,
	// and the newest version waiting replaces an older one.
	pushMu  sync.Mutex
	pending map[string]*pushJob
	pushing map[string]bool
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

// Publish renders, adds, and publishes one site. When the host can take only
// what changed, and that is not much (syncPushMax), the version goes to the
// host first and is announced after. If someone posted meanwhile (an agent,
// another machine), that version is taken in and the site is rendered and
// pushed again, at most twice. Otherwise the version is announced first and
// uploaded in the background.
func (p *Publisher) Publish(ctx context.Context, siteID string, force bool) (Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	base := ""
	for attempt := 0; ; attempt++ {
		res, behind, err := p.publishOnce(ctx, siteID, force, base)
		if behind == nil || err != nil {
			return res, err
		}
		if attempt == 2 {
			if site, err := p.Store.Site(siteID); err == nil {
				return Result{}, fmt.Errorf("%s: %w", site.Name, ErrSiteBusy)
			}
			return Result{}, ErrSiteBusy
		}
		site, err := p.Store.Site(siteID)
		if err != nil {
			return Result{}, err
		}
		if err := p.takeIn(ctx, site, behind); err != nil {
			return Result{}, err
		}
		if site, err = p.Store.Site(siteID); err != nil {
			return Result{}, err
		}
		if site.PublishedElsewhere {
			return Result{}, ErrPublishedElsewhere
		}
		base = strings.TrimPrefix(behind.Value, "/ipfs/")
	}
}

// publishOnce makes one attempt. behind is a newer version to take in before
// the next attempt. base is the version this machine builds on: its last
// publish, or a version just taken in.
func (p *Publisher) publishOnce(ctx context.Context, siteID string, force bool, base string) (Result, *ipfs.Record, error) {
	site, err := p.Store.Site(siteID)
	if err != nil {
		return Result{}, nil, err
	}
	if !p.Node.Keystore().Has(site.ID) {
		return Result{}, nil, fmt.Errorf("no IPNS key for %s on this machine; run `croptop key import %s <file.pem>`", site.Name, site.ID)
	}
	p.log("rendering %s", site.Name)
	if err := p.Render.Render(ctx, siteID); err != nil {
		return Result{}, nil, fmt.Errorf("render: %w", err)
	}
	p.log("adding to IPFS")
	cid, err := p.Node.AddDir(ctx, p.Store.PublicDir(siteID))
	if err != nil {
		return Result{}, nil, err
	}
	// render may have changed post files, not the site, but reload anyway
	if site, err = p.Store.Site(siteID); err != nil {
		return Result{}, nil, err
	}
	rec, netErr := p.latestRecord(ctx, site)
	if netErr == nil {
		p.log("network has sequence %d -> %s", rec.Sequence, rec.Value)
	} else {
		p.log("network record: %v", netErr)
	}
	if base == "" {
		base = deref(site.LastPublishedCID)
	}
	seq, err := nextSequence(site.IPNSSequence, base, rec, netErr, force)
	if err == ErrPublishedElsewhere && !force && rec != nil {
		return Result{}, rec, nil // take it in, then try again
	}
	if err != nil {
		if err == ErrPublishedElsewhere {
			p.saveSite(site.ID, func(s *store.Site) { s.PublishedElsewhere = true })
		}
		return Result{}, nil, err
	}
	if !force {
		if res, behind, done, err := p.publishChanges(ctx, site, cid, seq, base); done || behind != nil || err != nil {
			return res, behind, err
		}
	}
	if err := ctx.Err(); err != nil { // the caller gave up: announce nothing
		return Result{}, nil, err
	}
	p.log("publishing %s at sequence %d", cid, seq)
	pctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	if err := p.Node.NamePublish(pctx, site.ID, cid, seq); err != nil {
		return Result{}, nil, err
	}
	if err := p.published(site.ID, cid, seq); err != nil {
		return Result{}, nil, err
	}
	if !p.SkipPrewarm {
		if p.Wait {
			if err := p.pushVersion(ctx, &pushJob{site, cid, seq}); err != nil {
				p.log("push to %s failed: %v", HostOf(site), err)
			} else {
				p.log("pushed %s to %s", site.Name, HostOf(site))
			}
			p.warm(site, cid)
		} else {
			p.queuePush(site, cid, seq)
			go p.warm(site, cid)
		}
	}
	return Result{CID: cid, Sequence: seq}, nil, nil
}

type pushJob struct {
	site *store.Site
	cid  string
	seq  uint64
}

// errSuperseded: another machine published past the version being uploaded,
// an agent's post say. Uploading it would replace that version, so it is taken
// in and the site published again instead.
var errSuperseded = errors.New("the host holds a newer version published elsewhere")

// queuePush uploads a version in the background, one upload per site at a
// time. A version queued while another uploads replaces any older one
// waiting, so after a long upload only the newest goes up, as a changes-only
// push when it can be. A failed upload is retried with growing waits while
// it is still the newest.
func (p *Publisher) queuePush(site *store.Site, cid string, seq uint64) {
	p.pushMu.Lock()
	defer p.pushMu.Unlock()
	if p.pending == nil {
		p.pending, p.pushing = map[string]*pushJob{}, map[string]bool{}
	}
	p.pending[site.ID] = &pushJob{site, cid, seq}
	if !p.pushing[site.ID] {
		p.pushing[site.ID] = true
		go p.runPushes(site.ID)
	}
}

func (p *Publisher) runPushes(siteID string) {
	wait := time.Minute
	for {
		p.pushMu.Lock()
		j := p.pending[siteID]
		delete(p.pending, siteID)
		if j == nil {
			p.pushing[siteID] = false
			p.pushMu.Unlock()
			return
		}
		p.pushMu.Unlock()
		err := p.pushVersion(context.Background(), j)
		switch {
		case err == nil:
			p.log("pushed %s to %s", j.site.Name, HostOf(j.site))
			wait = time.Minute
			continue
		case errors.Is(err, errSuperseded):
			// take that version in and publish on top of it: a new version is
			// queued (or pushed) by the publish, and this loop picks it up
			p.log("%s: %v; taking it in and publishing again", j.site.Name, err)
			if _, err := p.Publish(context.Background(), siteID, false); err != nil {
				p.log("publishing %s again: %v", j.site.Name, err)
			}
			continue
		}
		p.log("push of %s to %s failed: %v; trying again in %s", j.site.Name, HostOf(j.site), err, wait)
		p.pushMu.Lock()
		if p.pending[siteID] == nil {
			p.pending[siteID] = j
		}
		p.pushMu.Unlock()
		time.Sleep(wait)
		if wait < 30*time.Minute {
			wait *= 2
		}
	}
}

// pushVersion sends a version the network already has to the site's host:
// only what changed when the host holds an earlier version, otherwise every
// file. An upload cut short resumes where it stopped. A version the host
// already holds is done; one that another machine published past is
// errSuperseded.
func (p *Publisher) pushVersion(ctx context.Context, j *pushJob) error {
	hostURL := HostOf(j.site)
	settled := func() (bool, error) { // the host has this version, or moved past it
		hctx, cancel := context.WithTimeout(ctx, hostTimeout)
		e, err := hostEntry(hctx, hostURL, j.site.IPNS)
		cancel()
		switch {
		case err != nil:
			return false, nil
		case e.CID == j.cid:
			return true, nil
		case e.Sequence < j.seq:
			return false, nil
		}
		if site, err := p.Store.Site(j.site.ID); err == nil && deref(site.LastPublishedCID) == e.CID {
			return true, nil // this machine's own newer version is there
		}
		return true, fmt.Errorf("%s holds %s at sequence %d: %w", hostURL, e.CID, e.Sequence, errSuperseded)
	}
	if done, err := settled(); done {
		return err
	}
	err := p.pushChangesOrAll(ctx, j)
	var hc *hostConflict
	if errors.As(err, &hc) { // the host moved meanwhile, or the push committed and its answer was lost
		if done, serr := settled(); done {
			return serr
		}
	}
	return err
}

// pushChangesOrAll sends what changed since the version the host holds when
// it can, otherwise the whole version.
func (p *Publisher) pushChangesOrAll(ctx context.Context, j *pushJob) error {
	if eng, ok := p.Node.(changesEngine); ok {
		hostURL := HostOf(j.site)
		hctx, cancel := context.WithTimeout(ctx, hostTimeout)
		e, err := hostEntry(hctx, hostURL, j.site.IPNS)
		cancel()
		if err == nil && e.AcceptsManifest {
			if upload, carry, err := p.changes(ctx, eng, hostURL, j.cid, e.CID); err == nil && eng.CheckManifest(ctx, j.cid, e.CID, upload, carry) == nil {
				return p.pushDir(ctx, j.site, j.site.ID, j.cid, j.seq, pushSpec{Parent: e.CID, Files: upload, Carry: carry})
			}
		}
	}
	return p.Push(ctx, j.site, j.cid, j.seq)
}

// publishChanges sends only what changed since the version the host holds,
// then announces the same record. done is false when that is not possible:
// another engine, an older host, a host holding another version, a diff
// that does not check out, a change over syncPushMax, or a host that cannot
// be reached. The caller then announces first and uploads in the background.
// behind is the version the host has instead, when another push got there
// first: it is taken in before any upload, which would otherwise replace it.
func (p *Publisher) publishChanges(ctx context.Context, site *store.Site, cid string, seq uint64, base string) (res Result, behind *ipfs.Record, done bool, err error) {
	if base == "" {
		return Result{}, nil, false, nil
	}
	hostURL := HostOf(site)
	hctx, cancel := context.WithTimeout(ctx, hostTimeout)
	e, herr := hostEntry(hctx, hostURL, site.IPNS)
	cancel()
	if herr == nil && hostAhead(site, e, base, cid) {
		// published elsewhere since this machine last looked, an agent's post
		// say: whatever this machine uploads next would replace it
		p.log("%s holds %s at sequence %d, published elsewhere; taking it in first", hostURL, e.CID, e.Sequence)
		return Result{}, e.record(), false, nil
	}
	eng, ok := p.Node.(changesEngine)
	if !ok || herr != nil || !e.AcceptsManifest || e.CID != base || e.CID == cid {
		return Result{}, nil, false, nil
	}
	upload, carry, err := p.changes(ctx, eng, hostURL, cid, e.CID)
	if err == nil {
		err = eng.CheckManifest(ctx, cid, e.CID, upload, carry)
	}
	var files []pushFile // what the push would send, with sizes
	if err == nil {
		files, _, err = p.pushFiles(ctx, cid, pushSpec{Files: upload})
	}
	if err != nil {
		p.log("changes since %s: %v; sending the whole site", e.CID, err)
		return Result{}, nil, false, nil
	}
	var size int64
	for _, f := range files {
		size += f.size
	}
	if size > syncPushMax {
		p.log("%.1f MB changed; announcing first, the upload follows", float64(size)/(1<<20))
		return Result{}, nil, false, nil
	}
	rec, err := eng.SignRecord(site.ID, cid, seq)
	if err != nil {
		return Result{}, nil, false, err
	}
	p.log("pushing %d changed files of %s on top of %s", len(upload), cid, e.CID)
	err = p.pushDir(ctx, site, site.ID, cid, seq, pushSpec{Parent: e.CID, Files: upload, Carry: carry})
	var hc *hostConflict
	switch {
	case errors.As(err, &hc):
		hctx, cancel := context.WithTimeout(ctx, hostTimeout)
		cur, herr := hostEntry(hctx, hostURL, site.IPNS)
		cancel()
		switch {
		case herr != nil:
			return Result{}, nil, false, err
		case cur.CID != cid:
			p.log("%v; taking the newer version in", hc)
			return Result{}, cur.record(), false, nil
		}
		// The host holds this very version: the push committed and only its
		// answer was lost, so what came back is the refusal a retry gets. It is
		// no news to take in; it only needs announcing.
		p.log("%s already holds %s; announcing it", hostURL, cid)
	case err != nil && ctx.Err() != nil: // the caller gave up: announce nothing
		return Result{}, nil, true, ctx.Err()
	case err != nil:
		p.log("push to %s failed: %v; announcing first, the upload follows", hostURL, err)
		return Result{}, nil, false, nil
	}
	p.log("publishing %s at sequence %d", cid, seq)
	pctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	if err := eng.AnnounceRecord(pctx, site.ID, cid, rec); err != nil {
		// the host holds this version and announces its record itself: the
		// publish has happened, and the next one builds on it
		p.log("announcing %s: %v; %s has it and announces it", cid, err, hostURL)
	}
	if err := p.published(site.ID, cid, seq); err != nil {
		return Result{}, nil, true, err
	}
	if !p.SkipPrewarm {
		if p.Wait {
			p.warm(site, cid)
		} else {
			go p.warm(site, cid)
		}
	}
	return Result{CID: cid, Sequence: seq}, nil, true, nil
}

// published records a version as this machine's latest.
func (p *Publisher) published(siteID, cid string, seq uint64) error {
	now := store.Now()
	return p.saveSite(siteID, func(s *store.Site) {
		s.IPNSSequence, s.LastPublishedCID, s.LastPublished, s.PublishedElsewhere = seq, &cid, &now, false
	})
}

// warm asks the gateways to fetch a new version.
func (p *Publisher) warm(site *store.Site, cid string) {
	p.log("asking gateways to fetch the new version")
	p.prewarm(context.Background(), site, cid)
	p.prewarmAll(site, cid)
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
	switch {
	case errors.Is(err, errNoVersion), err == nil && e.Sequence < site.IPNSSequence && e.CID != *site.LastPublishedCID:
		// the host is behind this machine: an upload was cut short (the app quit,
		// the laptop slept) or never made; send it, resuming what the host holds
		p.queuePush(site, *site.LastPublishedCID, site.IPNSSequence)
		return nil
	case err != nil || !hostAhead(site, e, *site.LastPublishedCID):
		return nil // our own version is there
	}
	return p.takeIn(ctx, site, e.record())
}

// hostAhead says whether the host holds a version of site that this machine
// has not taken in: none of known (its last publish, a version it is
// publishing), at a sequence no lower than its own. Such a version was
// published elsewhere, by an agent say, and must be taken in before this
// machine uploads, or the upload would replace it.
func hostAhead(site *store.Site, e *hostKey, known ...string) bool {
	for _, c := range known {
		if e.CID == c {
			return false
		}
	}
	return e.Sequence >= site.IPNSSequence
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
		return e.record(), nil
	}
	return rec, err
}

// record is the version a host holds, in the form the network's records come
// in, for the places that weigh the two or take a host's version in.
func (e *hostKey) record() *ipfs.Record {
	return &ipfs.Record{Value: "/ipfs/" + e.CID, Sequence: e.Sequence}
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
