package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	boxoipns "github.com/ipfs/boxo/ipns"
	"github.com/ipfs/go-cid"
	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

var (
	ErrPostExists           = errors.New("a post with this identity is already published")
	ErrAuthorizationExpired = errors.New("publishing authorization expired; sign this operation again")
	ErrParentChanged        = errors.New("the site changed; prepare this operation again on the current version")
)

// PreparedPost is an unsigned update. Its JSON and working directory may be
// retained across service restarts. It contains no private key. Only trusted
// server storage may supply this descriptor to CommitPreparedPost.
type PreparedPost struct {
	Site       *store.Site `json:"site"`
	Parent     string      `json:"parent"`
	CID        string      `json:"cid"`
	Sequence   uint64      `json:"sequence"`
	URL        string      `json:"url"`
	PostID     string      `json:"postId"`
	ChangedDir string      `json:"changedDir"`
	WorkDir    string      `json:"workDir"`
}

// PostAuthorization is created on the author's device. Timestamp and Signature
// authorize the host's existing PushMessage, and Record is its signed IPNS
// record. The service cannot extend either authorization.
type PostAuthorization struct {
	Timestamp int64  `json:"timestamp"`
	Signature []byte `json:"signature"`
	Record    []byte `json:"record"`
}

// SiteSnapshot is CID-verified metadata reached through a valid signed IPNS
// record. Raw fields include the publication's compatibility descriptor.
type SiteSnapshot struct {
	Site          *store.Site `json:"site"`
	CID           string      `json:"cid"`
	Sequence      uint64      `json:"sequence"`
	AcceptsParent bool        `json:"acceptsParent"`
}

// PreparePost uses the same renderer and incremental tree builder as Post,
// without importing a key or publishing anything. The caller retains workDir
// until the operation has completed or expired; failed preparations also leave
// cleanup to the caller. Its published/store/changed children must be unused.
func (p *Publisher) PreparePost(ctx context.Context, hostURL, ipnsName string, np NewPost, workDir string) (PreparedPost, error) {
	eng, ok := p.Node.(postEngine)
	if !ok || p.Render == nil {
		return PreparedPost{}, errors.New("preparing a post needs an embedded engine and renderer")
	}
	if err := validateNewPost(np); err != nil {
		return PreparedPost{}, err
	}
	if workDir == "" {
		return PreparedPost{}, errors.New("a retained working directory is required")
	}
	workDir, err := filepath.Abs(workDir)
	if err != nil {
		return PreparedPost{}, err
	}
	for _, child := range []string{"published", "store", "changed"} {
		if _, err := os.Lstat(filepath.Join(workDir, child)); !os.IsNotExist(err) {
			return PreparedPost{}, fmt.Errorf("working directory already contains %s", child)
		}
	}
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return PreparedPost{}, err
	}
	_, entry, err := p.inspectSite(ctx, hostURL, ipnsName)
	if err != nil {
		return PreparedPost{}, err
	}
	prepared, err := p.preparePost(ctx, eng, hostURL, ipnsName, np, workDir, entry)
	if err != nil {
		return PreparedPost{}, err
	}
	return PreparedPost{Site: prepared.site, Parent: prepared.parent, CID: prepared.CID, Sequence: prepared.Sequence,
		URL: prepared.URL, PostID: prepared.postID, ChangedDir: prepared.changed, WorkDir: prepared.workDir}, nil
}

// CommitPreparedPost checks device authorization and the current published
// storage policy, then uses the existing parent-checked host transport. The
// externally supplied signature is rechecked for expiry for every batch.
func (p *Publisher) CommitPreparedPost(ctx context.Context, prepared PreparedPost, auth PostAuthorization) (Posted, error) {
	if err := validatePostAuthorization(prepared, auth); err != nil {
		return Posted{}, err
	}
	if !safeIdentity(prepared.PostID) || prepared.WorkDir == "" || prepared.ChangedDir != filepath.Join(prepared.WorkDir, "changed") {
		return Posted{}, errors.New("invalid prepared post directory or identity")
	}
	base := HostOf(prepared.Site)
	snapshot, err := p.InspectSite(ctx, base, prepared.Site.IPNS)
	if err != nil {
		return Posted{}, err
	}
	if posted, err := snapshotPost(snapshot, prepared.PostID); err != nil || posted != nil {
		if err != nil {
			return Posted{}, err
		}
		return *posted, nil
	}
	if !snapshot.Site.HostingEnabled() {
		return Posted{}, ErrHostingDisabled
	}
	if HostOf(snapshot.Site) != base {
		return Posted{}, errHostChanged
	}
	if snapshot.CID != prepared.Parent || snapshot.Sequence >= prepared.Sequence {
		return Posted{}, ErrParentChanged
	}
	eng, ok := p.Node.(postEngine)
	if !ok {
		return Posted{}, errors.New("committing a prepared post needs the embedded engine")
	}
	// Rebuild from the retained files. This restores directory blocks after a
	// restart and prevents a changed staging directory from sending an update
	// other than the exact one the device authorized.
	got, err := eng.AddOver(ctx, prepared.Parent, prepared.ChangedDir)
	if err != nil {
		return Posted{}, err
	}
	if got != prepared.CID {
		return Posted{}, errors.New("prepared files no longer match the authorized CID")
	}
	staged := &Publisher{Node: p.Node, Log: p.Log}
	if err := staged.pushDir(ctx, snapshot.Site, "", prepared.CID, prepared.Sequence,
		pushSpec{Parent: prepared.Parent, Dir: prepared.ChangedDir, Record: auth.Record, Authorization: &auth}); err != nil {
		var conflict *hostConflict
		if errors.As(err, &conflict) {
			return Posted{}, fmt.Errorf("%w: %v", ErrParentChanged, err)
		}
		return Posted{}, err
	}
	return Posted{Result: Result{CID: prepared.CID, Sequence: prepared.Sequence}, URL: prepared.URL}, nil
}

func validatePostAuthorization(prepared PreparedPost, auth PostAuthorization) error {
	if prepared.Site == nil || prepared.Site.IPNS == "" {
		return errors.New("prepared post has no site")
	}
	if !host.FreshTimestamp(auth.Timestamp) {
		return ErrAuthorizationExpired
	}
	if _, err := cid.Decode(prepared.CID); err != nil {
		return errors.New("prepared post has an invalid CID")
	}
	if _, err := cid.Decode(prepared.Parent); err != nil {
		return errors.New("prepared post has an invalid parent CID")
	}
	domain, err := hostDomain(HostOf(prepared.Site))
	if err != nil {
		return err
	}
	if !ipfs.VerifyIPNS(prepared.Site.IPNS, host.PushMessage(domain, prepared.Site.IPNS, prepared.CID, prepared.Sequence, auth.Timestamp), auth.Signature) {
		return errors.New("invalid device publishing signature")
	}
	return validateHeadRecord(prepared.Site.IPNS, prepared.CID, prepared.Sequence, auth.Record)
}

func validateHeadRecord(ipnsName, c string, sequence uint64, b []byte) error {
	if len(b) == 0 || len(b) > boxoipns.MaxRecordSize {
		return errors.New("missing or oversized signed IPNS record")
	}
	name, err := boxoipns.NameFromString(ipnsName)
	if err != nil {
		return fmt.Errorf("IPNS name: %w", err)
	}
	record, err := boxoipns.UnmarshalRecord(b)
	if err != nil {
		return fmt.Errorf("IPNS record: %w", err)
	}
	if err := boxoipns.ValidateWithName(record, name); err != nil {
		return fmt.Errorf("IPNS record: %w", err)
	}
	value, err := record.Value()
	if err != nil || value.String() != "/ipfs/"+c {
		return errors.New("IPNS record does not authorize the proposed CID")
	}
	seq, err := record.Sequence()
	if err != nil || seq != sequence {
		return errors.New("IPNS record does not authorize the proposed sequence")
	}
	return nil
}

// InspectSite verifies the host's current IPNS record and metadata. It does not
// require hosting to remain enabled, so committed operations can be recovered
// after an author suspends mobile posting or changes their storage policy.
func (p *Publisher) InspectSite(ctx context.Context, hostURL, ipnsName string) (SiteSnapshot, error) {
	snapshot, _, err := p.inspectSite(ctx, hostURL, ipnsName)
	return snapshot, err
}

func (p *Publisher) inspectSite(ctx context.Context, hostURL, ipnsName string) (SiteSnapshot, *hostKey, error) {
	if _, err := boxoipns.NameFromString(ipnsName); err != nil {
		return SiteSnapshot{}, nil, errors.New("invalid IPNS name")
	}
	eng, ok := p.Node.(postEngine)
	if !ok {
		return SiteSnapshot{}, nil, errors.New("inspecting a site needs the embedded engine")
	}
	entry, err := hostEntry(ctx, hostURL, ipnsName)
	if err != nil {
		return SiteSnapshot{}, nil, err
	}
	// The query avoids a cached record from the previous head when a host's
	// routing endpoint caches records but its registry has already advanced.
	recordURL := hostURL + "/routing/v1/ipns/" + url.PathEscape(ipnsName) + "?sequence=" + strconv.FormatUint(entry.Sequence, 10) + "&cid=" + url.QueryEscape(entry.CID)
	b, status, err := httpGet(ctx, recordURL)
	if err != nil || status != 200 {
		return SiteSnapshot{}, nil, fmt.Errorf("cannot verify the host's signed IPNS record (status %d): %v", status, err)
	}
	if err := validateHeadRecord(ipnsName, entry.CID, entry.Sequence, b); err != nil {
		return SiteSnapshot{}, nil, err
	}
	links, err := p.links(ctx, eng, hostURL, entry.CID)
	if err != nil {
		return SiteSnapshot{}, nil, err
	}
	want := links["planet.json"]
	if want == "" {
		return SiteSnapshot{}, nil, errors.New("the published version has no planet.json")
	}
	tmp, err := os.MkdirTemp("", "croptop-inspect-*")
	if err != nil {
		return SiteSnapshot{}, nil, err
	}
	defer os.RemoveAll(tmp)
	file := filepath.Join(tmp, "planet.json")
	if err := p.readFile(ctx, eng, hostURL, entry.CID, "planet.json", want, file); err != nil {
		return SiteSnapshot{}, nil, err
	}
	b, err = os.ReadFile(file)
	if err != nil {
		return SiteSnapshot{}, nil, err
	}
	var site store.Site
	if err := json.Unmarshal(b, &site); err != nil || !safeIdentity(site.ID) || site.IPNS != ipnsName {
		return SiteSnapshot{}, nil, errors.New("published metadata does not identify the requested Croptop site")
	}
	if err := validatePublishedPaths(&site); err != nil {
		return SiteSnapshot{}, nil, err
	}
	if NameOf(&site) == "" && entry.Name != "" {
		setRaw(&site, NameKey, entry.Name)
	}
	return SiteSnapshot{Site: &site, CID: entry.CID, Sequence: entry.Sequence, AcceptsParent: entry.AcceptsParent}, entry, nil
}

// InspectPost finds an operation's stable identity in the current verified
// head, including when another publication has advanced it since the commit.
// A nil result with no error means the post is absent from that head.
func (p *Publisher) InspectPost(ctx context.Context, hostURL, ipnsName, postID string) (*Posted, error) {
	if !safeIdentity(postID) {
		return nil, errors.New("invalid post ID")
	}
	snapshot, err := p.InspectSite(ctx, hostURL, ipnsName)
	if err != nil {
		return nil, err
	}
	return snapshotPost(snapshot, postID)
}

func snapshotPost(snapshot SiteSnapshot, postID string) (*Posted, error) {
	var articles []render.PublicPost
	if err := json.Unmarshal(snapshot.Site.Raw["articles"], &articles); err != nil {
		return nil, fmt.Errorf("published posts: %w", err)
	}
	for _, post := range articles {
		if post.ID == postID {
			return &Posted{Result: Result{CID: snapshot.CID, Sequence: snapshot.Sequence}, URL: render.BrowserURL(snapshot.Site, postFromPublic(post))}, nil
		}
	}
	return nil, nil
}

func safeIdentity(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// Rebuilding a signed site still must not let author-controlled metadata
// escape this operation's staging directory.
func validatePublishedPaths(site *store.Site) error {
	var articles []render.PublicPost
	if err := json.Unmarshal(site.Raw["articles"], &articles); err != nil {
		return fmt.Errorf("published posts: %w", err)
	}
	for _, article := range articles {
		if !safeIdentity(article.ID) || article.Slug != "" && !safeIdentity(article.Slug) {
			return errors.New("published post has an unsafe identity or slug")
		}
		for _, name := range article.Attachments {
			if !filepath.IsLocal(name) || filepath.Base(name) != name || strings.ContainsAny(name, "/\\") {
				return errors.New("published post has an unsafe attachment name")
			}
		}
	}
	return nil
}
