package publish

import (
	"context"
	"errors"

	"github.com/mejango/croptop/internal/ctxlock"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/store"
)

// HostedStage describes work in progress, not a percentage or a success claim.
type HostedStage string

const (
	HostedRendering HostedStage = "rendering"
	HostedChecking  HostedStage = "checking_current_version"
	HostedUploading HostedStage = "uploading"
	HostedVerifying HostedStage = "verifying_host"
)

var (
	// ErrHostNotFound means the registry answered that it has no published
	// version. Network failures and invalid signed metadata are not absence.
	ErrHostNotFound = errNoVersion
	// A full upload without a known parent cannot safely replace an existing
	// site's missing host head: the current protocol has no empty-head CAS.
	ErrHostedBootstrapNeedsPublish = errors.New("this previously published site has no hosted version; publish it from the existing publisher, wait for hosting to finish, then connect the phone")
	ErrHostedParentUnsupported     = errors.New("the host cannot safely accept this update; update its parent-checked manifest support before connecting a phone")
	// A request may already have committed when its answer or verification is
	// interrupted. Retrying setup inspects the hosted head before publishing.
	ErrHostedOutcomeUnknown = errors.New("the hosted publication could not be confirmed; it may already be published; check the hosted version before retrying")
)

func hostedProgress(progress func(HostedStage), stage HostedStage) {
	if progress != nil {
		progress(stage)
	}
}

// PublishHosted publishes an explicitly approved local version to the saved
// host and verifies its signed, content-addressed head before returning. It
// does not change storage policy, force past another writer, enqueue uploads,
// or wait for a desktop DHT announcement. The host announces the record it
// commits; normal Keepalive subsequently renews it from this machine.
//
// Existing ready phone connections should not call this method: connecting a
// phone is not permission to publish pending local edits. The caller supplies
// a bounded context and a fast, non-blocking progress observer.
func (p *Publisher) PublishHosted(ctx context.Context, siteID string, progress func(HostedStage)) (Result, error) {
	if err := ctxlock.Lock(ctx, &p.mu); err != nil {
		return Result{}, err
	}
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if _, ok := p.Node.(changesEngine); !ok {
		return Result{}, errors.New("hosted preparation needs the embedded publishing engine")
	}
	return p.publishRetry(ctx, siteID, false, func(ctx context.Context, id string, _ bool, base string) (Result, *ipfs.Record, error) {
		return p.publishHostedOnce(ctx, id, base, progress)
	})
}

func (p *Publisher) publishHostedOnce(ctx context.Context, siteID, base string, progress func(HostedStage)) (Result, *ipfs.Record, error) {
	site, err := p.Store.Site(siteID)
	if err != nil {
		return Result{}, nil, err
	}
	site, err = p.hostedSite(site)
	if err != nil {
		return Result{}, nil, err
	}
	hostURL := HostOf(site)
	v, behind, err := p.prepareVersion(ctx, siteID, false, base, progress)
	if behind != nil || err != nil {
		return Result{}, behind, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, nil, err
	}
	current, entry, err := p.inspectHosted(ctx, site, hostURL)
	if err != nil && !errors.Is(err, ErrHostNotFound) {
		return Result{}, nil, err
	}
	spec := pushSpec{}
	if errors.Is(err, ErrHostNotFound) {
		if !v.fresh || v.seq != 1 {
			return Result{}, nil, ErrHostedBootstrapNeedsPublish
		}
	} else {
		if hostAhead(v.site, entry, v.base, v.cid) {
			return Result{}, entry.record(), nil
		}
		if current.CID == v.cid {
			return p.recordHosted(v, current)
		}
		if !entry.AcceptsParent || !entry.AcceptsManifest {
			return Result{}, nil, ErrHostedParentUnsupported
		}
		// Even a whole fallback uses a parent and an explicitly empty carry
		// list. Omitting either could overwrite unseen work or retain deleted
		// paths. No size threshold: hosted success requires the actual commit.
		spec.Parent, spec.Carry = entry.CID, []string{}
		eng := p.Node.(changesEngine)
		upload, carry, diffErr := p.changes(ctx, eng, hostURL, v.cid, entry.CID, site)
		if diffErr == nil {
			diffErr = eng.CheckManifest(ctx, v.cid, entry.CID, upload, carry)
		}
		if diffErr == nil {
			spec.Files, spec.Carry = upload, carry
		}
		v.seq = max(v.seq, entry.Sequence+1)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, nil, err
	}
	if err := p.checkHost(site, hostURL); err != nil {
		return Result{}, nil, err
	}
	eng := p.Node.(changesEngine)
	spec.Record, err = eng.SignRecord(siteID, v.cid, v.seq)
	if err != nil {
		return Result{}, nil, err
	}
	hostedProgress(progress, HostedUploading)
	pushErr := p.pushDir(ctx, site, siteID, v.cid, v.seq, spec)
	if errors.Is(pushErr, errNoCommitFile) && spec.Files != nil && ctx.Err() == nil {
		// Changes made only of large files/deletions still need a small final
		// commit part. The full manifest preserves the same parent guard.
		spec.Files, spec.Carry = nil, []string{}
		pushErr = p.pushDir(ctx, site, siteID, v.cid, v.seq, spec)
	}
	hostedProgress(progress, HostedVerifying)
	if err := ctx.Err(); err != nil {
		return Result{}, nil, errors.Join(ErrHostedOutcomeUnknown, err)
	}
	current, entry, err = p.inspectHosted(ctx, site, hostURL)
	if err == nil && current.CID == v.cid {
		return p.recordHosted(v, current)
	}
	var conflict *hostConflict
	if err == nil && errors.As(pushErr, &conflict) {
		return Result{}, entry.record(), nil
	}
	if pushErr != nil {
		return Result{}, nil, errors.Join(ErrHostedOutcomeUnknown, pushErr, err)
	}
	if err == nil {
		err = errors.New("the host still serves a different version")
	}
	return Result{}, nil, errors.Join(ErrHostedOutcomeUnknown, err)
}

func (p *Publisher) inspectHosted(ctx context.Context, site *store.Site, hostURL string) (SiteSnapshot, *hostKey, error) {
	if err := p.checkHost(site, hostURL); err != nil {
		return SiteSnapshot{}, nil, err
	}
	hctx, cancel := context.WithTimeout(ctx, hostTimeout)
	defer cancel()
	snapshot, entry, err := p.inspectSite(hctx, hostURL, site.IPNS)
	if err == nil {
		err = p.checkHost(site, hostURL)
	}
	return snapshot, entry, err
}

func (p *Publisher) recordHosted(v publicationVersion, snapshot SiteSnapshot) (Result, *ipfs.Record, error) {
	// An unchanged CID may be served at an older sequence. Report the actual
	// host sequence, while retaining this machine's higher renewal counter.
	if err := p.published(v.site.ID, snapshot.CID, max(v.site.IPNSSequence, snapshot.Sequence)); err != nil {
		return Result{}, nil, err
	}
	return Result{CID: snapshot.CID, Sequence: snapshot.Sequence}, nil, nil
}
