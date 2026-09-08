package publish

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mejango/croptop/internal/store"
)

type SyncResult struct {
	Added, Updated int
	Result         Result
}

// Sync pulls the version of the site the network currently serves, merges
// its posts into ours (newest edit per post wins), and publishes the result.
// This is how two machines take turns on one site.
func (p *Publisher) Sync(ctx context.Context, siteID string) (SyncResult, error) {
	site, err := p.Store.Site(siteID)
	if err != nil {
		return SyncResult{}, err
	}
	rec, err := p.Node.NetworkRecord(ctx, site.IPNS)
	if err != nil {
		return SyncResult{}, fmt.Errorf("network record: %w", err)
	}
	cid := strings.TrimPrefix(rec.Value, "/ipfs/")
	var added, updated int
	if site.LastPublishedCID == nil || *site.LastPublishedCID != cid {
		tmp, err := os.MkdirTemp(p.Store.Root, "sync-*")
		if err != nil {
			return SyncResult{}, err
		}
		defer os.RemoveAll(tmp)
		p.log("fetching %s", cid)
		if err := p.fetchSite(ctx, site.IPNS, cid, filepath.Join(tmp, "site")); err != nil {
			return SyncResult{}, err
		}
		remote := &store.Store{Root: filepath.Join(tmp, "store")}
		if err := rebuildSource(remote, siteID, filepath.Join(tmp, "site")); err != nil {
			return SyncResult{}, fmt.Errorf("remote site: %w", err)
		}
		local, err := p.Store.Posts(siteID)
		if err != nil {
			return SyncResult{}, err
		}
		remotePosts, err := remote.Posts(siteID)
		if err != nil {
			return SyncResult{}, err
		}
		merged, changed, a, u := mergePosts(local, remotePosts)
		added, updated = a, u
		_ = merged
		for _, rp := range changed {
			if err := p.Store.SavePost(siteID, rp); err != nil {
				return SyncResult{}, err
			}
			src := remote.PostDir(siteID, rp.ID)
			if entries, err := os.ReadDir(src); err == nil {
				for _, e := range entries {
					if err := copyFile(filepath.Join(src, e.Name()), filepath.Join(p.Store.PostDir(siteID, rp.ID), e.Name())); err != nil {
						return SyncResult{}, err
					}
				}
			}
			// generated files travel with the post so CIDs stay stable
			for _, f := range []string{"nft.json", "nft.json.cid.txt", "_cover.png", "_videoThumbnail.png"} {
				s := filepath.Join(tmp, "site", rp.ID, f)
				if _, err := os.Stat(s); err == nil {
					copyFile(s, filepath.Join(p.Store.PublicDir(siteID), rp.ID, f))
				}
			}
		}
		// we have absorbed the network's version; publishing on top of it is legitimate
		site.LastPublishedCID = &cid
		site.IPNSSequence = rec.Sequence
		site.PublishedElsewhere = false
		if err := p.Store.SaveSite(site); err != nil {
			return SyncResult{}, err
		}
		p.log("merged %d new and %d updated posts", added, updated)
	}
	res, err := p.Publish(ctx, siteID, false)
	return SyncResult{Added: added, Updated: updated, Result: res}, err
}

// mergePosts returns the merged list and the remote posts that replaced or
// added to local ones.
func mergePosts(local, remote []*store.Post) (merged []*store.Post, changed []*store.Post, added, updated int) {
	byID := map[string]*store.Post{}
	for _, p := range local {
		byID[p.ID] = p
	}
	for _, r := range remote {
		l, ok := byID[r.ID]
		switch {
		case !ok:
			byID[r.ID] = r
			changed = append(changed, r)
			added++
		case r.LastChanged() > l.LastChanged():
			byID[r.ID] = r
			changed = append(changed, r)
			updated++
		}
	}
	for _, p := range byID {
		merged = append(merged, p)
	}
	return merged, changed, added, updated
}
