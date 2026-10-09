package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/store"
)

type SyncResult struct {
	Added, Updated int
	Result         Result
}

// Sync takes in the newest version of the site the network or its host has,
// then publishes the result. This is how two machines take turns on one site.
func (p *Publisher) Sync(ctx context.Context, siteID string) (SyncResult, error) {
	site, err := p.Store.Site(siteID)
	if err != nil {
		return SyncResult{}, err
	}
	rec, err := p.latestRecord(ctx, site)
	if err != nil {
		return SyncResult{}, fmt.Errorf("network record: %w", err)
	}
	var added, updated int
	if site.LastPublishedCID == nil || "/ipfs/"+*site.LastPublishedCID != rec.Value {
		if added, updated, err = p.pull(ctx, site, rec); err != nil {
			return SyncResult{}, err
		}
		p.log("merged %d new and %d updated posts", added, updated)
	}
	res, err := p.Publish(ctx, siteID, false)
	return SyncResult{Added: added, Updated: updated, Result: res}, err
}

// pull merges version rec's posts into this machine's copy of the site
// (newest edit per post wins, and posts deleted here stay deleted) and makes
// rec the baseline for the next publish. With the embedded engine it
// downloads only the posts that are new or changed there.
func (p *Publisher) pull(ctx context.Context, site *store.Site, rec *ipfs.Record) (added, updated int, err error) {
	cid := strings.TrimPrefix(rec.Value, "/ipfs/")
	tmp, err := os.MkdirTemp(p.Store.Root, "pull-*")
	if err != nil {
		return 0, 0, err
	}
	defer os.RemoveAll(tmp)
	pub := filepath.Join(tmp, "site")
	host := ""
	if current, err := p.hostedSite(site); err == nil {
		host = HostOf(current)
	}
	eng, sparse := p.Node.(postEngine)
	var root map[string]string
	p.log("fetching %s", cid)
	if sparse {
		root, err = p.readListing(ctx, eng, host, cid, pub, site)
	} else {
		err = p.fetchSite(ctx, site.IPNS, cid, pub, site)
	}
	if err != nil {
		return 0, 0, err
	}
	remote := &store.Store{Root: filepath.Join(tmp, "store")}
	if err := rebuildSource(remote, site.ID, pub); err != nil {
		return 0, 0, fmt.Errorf("remote site: %w", err)
	}
	local, err := p.Store.Posts(site.ID)
	if err != nil {
		return 0, 0, err
	}
	remotePosts, err := remote.Posts(site.ID)
	if err != nil {
		return 0, 0, err
	}
	deleted, err := p.Store.Deleted(site.ID)
	if err != nil {
		return 0, 0, err
	}
	_, changed, added, updated := mergePosts(local, remotePosts, deleted)
	for _, rp := range changed {
		dir := filepath.Join(pub, rp.ID)
		if sparse && root[rp.ID] != "" {
			if err := p.readPost(ctx, eng, host, cid, root[rp.ID], rp, dir, site); err != nil {
				return 0, 0, fmt.Errorf("post %s: %w", rp.ID, err)
			}
		}
		if err := p.Store.SavePost(site.ID, rp); err != nil {
			return 0, 0, err
		}
		for _, name := range append(append([]string{}, rp.Attachments...), "_cover.png", "_videoThumbnail.png") {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				if err := copyFile(filepath.Join(dir, name), filepath.Join(p.Store.PostDir(site.ID, rp.ID), name)); err != nil {
					return 0, 0, err
				}
			}
		}
		// generated files travel with the post so CIDs stay stable
		for _, f := range []string{"nft.json", "nft.json.cid.txt", "_cover.png", "_videoThumbnail.png"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				copyFile(filepath.Join(dir, f), filepath.Join(p.Store.PublicDir(site.ID), rp.ID, f))
			}
		}
	}
	// we have absorbed that version; publishing on top of it is legitimate. The
	// sequence never goes down: this machine may have announced higher already,
	// and the next publish must go above both.
	return added, updated, p.saveSite(site.ID, func(s *store.Site) {
		rememberVersion(s, deref(s.LastPublishedCID))
		rememberVersion(s, cid)
		s.LastPublishedCID, s.IPNSSequence, s.PublishedElsewhere = &cid, max(s.IPNSSequence, rec.Sequence), false
	})
}

// readListing reads what version c says about its posts into dest:
// planet.json for the feed, each page's article.json (pages are folders
// planet.json leaves out), and index.html for the navigation order.
func (p *Publisher) readListing(ctx context.Context, eng postEngine, hostURL, c, dest string, policy ...*store.Site) (map[string]string, error) {
	root, err := p.links(ctx, eng, hostURL, c, policy...)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", c, err)
	}
	for _, name := range []string{"planet.json", "index.html"} {
		if want, ok := root[name]; ok {
			if err := p.readFile(ctx, eng, hostURL, c, name, want, filepath.Join(dest, name), policy...); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	var planet struct {
		Articles []struct {
			ID string `json:"id"`
		} `json:"articles"`
	}
	b, err := os.ReadFile(filepath.Join(dest, "planet.json"))
	if err != nil || json.Unmarshal(b, &planet) != nil {
		return nil, fmt.Errorf("%s is not a Croptop site (no planet.json)", c)
	}
	listed := map[string]bool{"assets": true}
	for _, a := range planet.Articles {
		listed[a.ID] = true
	}
	for name, dc := range root {
		if listed[name] || strings.Contains(name, ".") {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second) // a probe: most of these are files
		dir, err := p.links(pctx, eng, hostURL, dc, policy...)
		cancel()
		if err != nil {
			continue // a file, not a folder
		}
		if want, ok := dir["article.json"]; ok {
			if err := p.readFile(ctx, eng, hostURL, c, name+"/article.json", want, filepath.Join(dest, name, "article.json"), policy...); err != nil {
				return nil, fmt.Errorf("%s/article.json: %w", name, err)
			}
		}
	}
	return root, nil
}

// readPost downloads a changed post's own files into dest: its attachments
// and the generated files its CIDs depend on.
func (p *Publisher) readPost(ctx context.Context, eng postEngine, hostURL, root, dirCID string, rp *store.Post, dest string, policy ...*store.Site) error {
	files, err := p.links(ctx, eng, hostURL, dirCID, policy...)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, name := range append(append([]string{}, rp.Attachments...), "_cover.png", "_videoThumbnail.png", "nft.json", "nft.json.cid.txt") {
		if seen[name] {
			continue
		}
		seen[name] = true
		if want, ok := files[name]; ok {
			if err := p.readFile(ctx, eng, hostURL, root, rp.ID+"/"+name, want, filepath.Join(dest, name), policy...); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return nil
}

// mergePosts returns the merged list and the remote posts that replaced or
// added to local ones. A post deleted here is left out unless the remote
// copy changed after the deletion.
func mergePosts(local, remote []*store.Post, deleted map[string]store.AppleTime) (merged []*store.Post, changed []*store.Post, added, updated int) {
	byID := map[string]*store.Post{}
	for _, p := range local {
		byID[p.ID] = p
	}
	for _, r := range remote {
		l, ok := byID[r.ID]
		switch {
		case !ok:
			if at, gone := deleted[r.ID]; gone && r.LastChanged() <= at {
				continue
			}
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
