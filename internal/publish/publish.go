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

// Variables so that tests can shorten them.
var (
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

// syncPushWithin bounds that push in time too: a link slower than planned, or
// a host that answers lookups but takes uploads slowly, must not hold the
// publish. A push still going then is left to the background queue, which
// resumes it. A variable so that tests can lower it.
var syncPushWithin = 2 * time.Minute

// syncPushBudget is how long the push inside Publish may run: syncPushWithin,
// or half of what is left of the caller's deadline, so announcing still fits.
func syncPushBudget(ctx context.Context) time.Duration {
	d := syncPushWithin
	if dl, ok := ctx.Deadline(); ok {
		if half := time.Until(dl) / 2; half < d {
			d = half
		}
	}
	return d
}

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
	// pushMu guards pending, pushing and wake: one background upload runs per
	// site, the newest version waiting replaces an older one, and a newer one
	// cuts a retry's wait short.
	pushMu  sync.Mutex
	pending map[string]*pushJob
	pushing map[string]bool
	wake    map[string]chan struct{}
	// Gate, if set, is the console's lock. A background publish holds it so it
	// never renders while the console does; it is always taken before mu.
	Gate sync.Locker
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
	if netErr == nil && ownVersion(site, strings.TrimPrefix(rec.Value, "/ipfs/")) {
		// one of this machine's own versions (a host still holding an older one
		// while a newer one uploads) is no news from elsewhere: only its
		// sequence counts, and the next goes above it
		rec = &ipfs.Record{Value: "/ipfs/" + base, Sequence: rec.Sequence}
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
	if force {
		// a forced publish replaces whatever the host holds: that version is one
		// this machine has seen and overrides, not news for its upload to stop at
		hctx, cancel := context.WithTimeout(ctx, hostTimeout)
		e, err := hostEntry(hctx, HostOf(site), site.IPNS)
		cancel()
		if err == nil {
			if err := p.saveSite(site.ID, func(s *store.Site) { rememberVersion(s, e.CID) }); err != nil {
				return Result{}, nil, err
			}
		}
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
		j := &pushJob{site: site, cid: cid, seq: seq, rec: recordOf(p.Node, site.ID)}
		if p.Wait {
			if err := p.pushVersion(ctx, j); err != nil {
				p.log("push to %s failed: %v", HostOf(site), err)
			} else {
				p.log("pushed %s to %s", site.Name, HostOf(site))
			}
			p.warm(site, cid)
		} else {
			p.queuePush(site, cid, seq, j.rec)
			go p.warm(site, cid)
		}
	}
	return Result{CID: cid, Sequence: seq}, nil, nil
}

// pushJob is a version to upload in the background, with the signed record
// the host is to serve for it.
type pushJob struct {
	site *store.Site
	cid  string
	seq  uint64
	rec  []byte
}

// errSuperseded: another machine published past the version being uploaded,
// an agent's post say. Uploading it would replace that version, so it is taken
// in instead.
var errSuperseded = errors.New("the host holds a version published elsewhere")

// recordOf is the record node last signed for key, or nil.
func recordOf(node ipfs.Engine, key string) []byte {
	if rs, ok := node.(interface{ Record(string) []byte }); ok {
		return rs.Record(key)
	}
	return nil
}

// signFor signs the record a host is to serve for version c at seq, or nil
// with an engine that does not sign records itself.
func (p *Publisher) signFor(key, c string, seq uint64) []byte {
	if s, ok := p.Node.(interface {
		SignRecord(key, c string, seq uint64) ([]byte, error)
	}); ok {
		if rec, err := s.SignRecord(key, c, seq); err == nil {
			return rec
		}
	}
	return nil
}

// holds says whether this machine has version c's root block. An engine that
// cannot say is taken to.
func (p *Publisher) holds(ctx context.Context, c string) bool {
	if b, ok := p.Node.(interface {
		Block(context.Context, string) ([]byte, error)
	}); ok {
		_, err := b.Block(ctx, c)
		return err == nil
	}
	return true
}

// queuePush uploads a version in the background, one upload per site at a
// time. A version queued while another uploads replaces any older one
// waiting, so after a long upload only the newest goes up, as a changes-only
// push when it can be. A failed upload is retried with growing waits while
// it is still the newest; a newer one queued meanwhile goes at once.
func (p *Publisher) queuePush(site *store.Site, cid string, seq uint64, rec []byte) {
	p.pushMu.Lock()
	defer p.pushMu.Unlock()
	if p.pending == nil {
		p.pending, p.pushing, p.wake = map[string]*pushJob{}, map[string]bool{}, map[string]chan struct{}{}
	}
	old := p.pending[site.ID]
	p.pending[site.ID] = &pushJob{site: site, cid: cid, seq: seq, rec: rec}
	if !p.pushing[site.ID] {
		p.pushing[site.ID] = true
		p.wake[site.ID] = make(chan struct{}, 1)
		go p.runPushes(site.ID, p.wake[site.ID])
		return
	}
	if old != nil && old.cid == cid {
		return // the same version again (the minute's catch-up): the retry keeps its wait
	}
	select { // a newer version cuts a retry's wait short
	case p.wake[site.ID] <- struct{}{}:
	default:
	}
}

func (p *Publisher) runPushes(siteID string, wake chan struct{}) {
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
			p.overtaken(j, err)
			continue
		}
		p.log("push of %s to %s failed: %v; trying again in %s", j.site.Name, HostOf(j.site), err, wait)
		p.pushMu.Lock()
		if p.pending[siteID] == nil {
			p.pending[siteID] = j
		}
		p.pushMu.Unlock()
		select {
		case <-time.After(wait):
		case <-wake:
		}
		if wait < 30*time.Minute {
			wait *= 2
		}
	}
}

// overtaken handles an upload whose version another machine published past.
// That version is taken in. When nothing was saved here since the overtaken
// version was published, the site is published again on top of it, which shows
// what was published plus the other machine's posts. Edits saved since are
// never published by themselves: then the version is only taken in, and the
// next publish carries it all. The console's lock (Gate) is held throughout,
// so this never renders while the console does.
func (p *Publisher) overtaken(j *pushJob, why error) {
	ctx := context.Background()
	if p.Gate != nil {
		p.Gate.Lock()
		defer p.Gate.Unlock()
	}
	same, err := p.rendersTo(ctx, j.site.ID, j.cid)
	switch {
	case err != nil:
		p.log("%s: %v; reading this machine's copy: %v", j.site.Name, why, err)
	case same:
		p.log("%s: %v; taking it in and publishing again", j.site.Name, why)
		if _, err := p.Publish(ctx, j.site.ID, false); err != nil {
			p.log("publishing %s again: %v", j.site.Name, err)
		}
		return
	default:
		p.log("%s: %v; taking it in; edits saved since the last publish go up with the next one", j.site.Name, why)
	}
	if err := p.CatchUp(ctx, j.site.ID); err != nil {
		p.log("taking in %s: %v", j.site.Name, err)
	}
}

// rendersTo says whether the site as saved here renders to version c: whether
// anything was saved since c was published.
func (p *Publisher) rendersTo(ctx context.Context, siteID, c string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.Render.Render(ctx, siteID); err != nil {
		return false, err
	}
	got, err := p.Node.AddDir(ctx, p.Store.PublicDir(siteID))
	return got == c, err
}

// pushVersion sends a version the network already has to the site's host:
// only what changed when the host holds one of this machine's versions,
// otherwise every file. An upload cut short resumes where it stopped. A
// version the host already holds is done, and so is one this machine has
// published past since; one that the host holds a version published elsewhere
// instead of is errSuperseded, whatever the sequences.
func (p *Publisher) pushVersion(ctx context.Context, j *pushJob) error {
	hostURL := HostOf(j.site)
	settled := func() (bool, error) {
		site, err := p.Store.Site(j.site.ID)
		if err != nil {
			return false, nil
		}
		if j.cid != deref(site.LastPublishedCID) && j.seq < site.IPNSSequence {
			return true, nil // this machine published past it: that version's upload goes instead
		}
		hctx, cancel := context.WithTimeout(ctx, hostTimeout)
		e, err := hostEntry(hctx, hostURL, j.site.IPNS)
		cancel()
		switch {
		case err != nil:
			return false, nil
		case e.CID == j.cid:
			return true, nil
		case !ownVersion(site, e.CID), rememberedAfter(site, e.CID, j.cid):
			// published elsewhere, or taken in after this version was made: going
			// over it would drop what it added
			return true, fmt.Errorf("%s holds %s at sequence %d: %w", hostURL, e.CID, e.Sequence, errSuperseded)
		case e.Sequence >= j.seq:
			return true, nil // this machine's own newer version is there
		}
		return false, nil
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

// pushChangesOrAll sends what changed since the version the host holds, which
// must be one of this machine's, when it can; otherwise the whole version. A
// host that refuses the changes is sent the whole version; one that cannot be
// asked now is tried again later, not sent everything.
func (p *Publisher) pushChangesOrAll(ctx context.Context, j *pushJob) error {
	eng, ok := p.Node.(changesEngine)
	if !ok {
		return p.pushWhole(ctx, j)
	}
	hostURL := HostOf(j.site)
	hctx, cancel := context.WithTimeout(ctx, hostTimeout)
	e, err := hostEntry(hctx, hostURL, j.site.IPNS)
	cancel()
	switch {
	case errors.Is(err, errNoVersion):
		return p.pushWhole(ctx, j)
	case err != nil:
		return err
	case !e.AcceptsManifest:
		return p.pushWhole(ctx, j)
	}
	site, err := p.Store.Site(j.site.ID)
	if err != nil {
		return err
	}
	if !ownVersion(site, e.CID) || rememberedAfter(site, e.CID, j.cid) {
		return fmt.Errorf("%s holds %s at sequence %d: %w", hostURL, e.CID, e.Sequence, errSuperseded)
	}
	upload, carry, err := p.changes(ctx, eng, hostURL, j.cid, e.CID)
	if err == nil {
		err = eng.CheckManifest(ctx, j.cid, e.CID, upload, carry)
	}
	if err == nil {
		err = p.pushDir(ctx, j.site, j.site.ID, j.cid, j.seq, pushSpec{Parent: e.CID, Files: upload, Carry: carry, Record: j.rec})
		var refused *hostRefusal
		if !errors.As(err, &refused) && !errors.Is(err, errNoCommitFile) {
			return err // done, a conflict to look at, or a break to resume
		}
	}
	p.log("pushing only what changed of %s: %v; sending the whole version", j.cid, err)
	return p.pushWhole(ctx, j)
}

// pushWhole sends every file of the job's version.
func (p *Publisher) pushWhole(ctx context.Context, j *pushJob) error {
	spec := pushSpec{Record: j.rec}
	if _, ok := p.Node.(versionReader); !ok {
		spec.Dir = p.Store.PublicDir(j.site.ID)
	}
	return p.pushDir(ctx, j.site, j.site.ID, j.cid, j.seq, spec)
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
	sctx, cancel := context.WithTimeout(ctx, syncPushBudget(ctx))
	err = p.pushDir(sctx, site, site.ID, cid, seq, pushSpec{Parent: e.CID, Files: upload, Carry: carry, Record: rec})
	cancel()
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

// published records a version as this machine's latest. The version it
// replaces stays known as this machine's, which its host may still hold.
func (p *Publisher) published(siteID, cid string, seq uint64) error {
	now := store.Now()
	return p.saveSite(siteID, func(s *store.Site) {
		rememberVersion(s, deref(s.LastPublishedCID))
		rememberVersion(s, cid)
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
	if c := strings.TrimPrefix(rec.Value, "/ipfs/"); c != *site.LastPublishedCID {
		switch {
		case site.IPNSSequence == 0:
			// never published from here: the network's record is the baseline
			return p.saveSite(site.ID, func(s *store.Site) {
				rememberVersion(s, c)
				s.LastPublishedCID, s.IPNSSequence, s.PublishedElsewhere = &c, rec.Sequence, false
			})
		case ownVersion(site, c):
			// one of this machine's older versions (an earlier renewal's, before a
			// version was taken in): the network is behind, so the last publish is
			// renewed above it, below. Taking it in would put the older version
			// back, over whatever was taken in since.
		case rec.Sequence >= site.IPNSSequence:
			return p.takeIn(ctx, site, rec)
		default:
			return nil // network is behind us; the DHT will catch up
		}
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
	last := *site.LastPublishedCID
	switch {
	case errors.Is(err, errNoVersion), err == nil && e.CID != last && ownVersion(site, e.CID):
		// the host is behind this machine: an upload was cut short (the app quit,
		// the laptop slept) or never made; send it, resuming what the host holds.
		// A version this machine does not hold (a Planet import's) cannot be sent.
		if !p.holds(ctx, last) {
			return nil
		}
		p.queuePush(site, last, site.IPNSSequence, p.signFor(site.ID, last, site.IPNSSequence))
		return nil
	case err != nil || !hostAhead(site, e, last):
		return nil // our own version is there
	}
	return p.takeIn(ctx, site, e.record())
}

// hostAhead says whether the host holds a version of site published
// elsewhere, an agent's post say: none of this machine's versions
// (ownVersion), nor any of known (a version being published). Such a version
// must be taken in before this machine uploads, or the upload would replace
// it. Sequences cannot tell: renewals raise this machine's without the host,
// so an agent's version can sit below it.
func hostAhead(site *store.Site, e *hostKey, known ...string) bool {
	for _, c := range known {
		if e.CID == c {
			return false
		}
	}
	return !ownVersion(site, e.CID)
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
