package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

const (
	ipfsFetchTimeout = 4 * time.Minute
	httpFetchTimeout = 5 * time.Minute
)

// fetchSite downloads a published site into dest. It tries IPFS first (the
// exact CID), then reads the files rebuildSource needs over HTTP from the
// gateway list. HTTP is what keeps adopt and sync working when the only
// IPFS provider of the new version has gone offline.
func (p *Publisher) fetchSite(ctx context.Context, ipns, cid, dest string) error {
	ictx, cancel := context.WithTimeout(ctx, ipfsFetchTimeout)
	err := p.Node.Get(ictx, "/ipfs/"+cid, dest)
	cancel()
	if err == nil {
		if _, statErr := os.Stat(filepath.Join(dest, "planet.json")); statErr == nil {
			return nil
		}
		err = errors.New("fetched tree has no planet.json")
	}
	p.log("ipfs fetch failed (%v); trying gateways", err)
	os.RemoveAll(dest)
	var last error
	for _, base := range gateway.FetchURLs(ipns, cid) {
		hctx, cancel := context.WithTimeout(ctx, httpFetchTimeout)
		last = fetchTreeHTTP(hctx, base, dest)
		cancel()
		if last == nil {
			p.log("fetched from %s", base)
			return nil
		}
		p.log("%s: %v", base, last)
		os.RemoveAll(dest)
	}
	return fmt.Errorf("could not fetch %s from IPFS or any gateway: %w", cid, last)
}

// fetchTreeHTTP reads the published files adopt and sync need from base.
func fetchTreeHTTP(ctx context.Context, base, dest string) error {
	client := &http.Client{Timeout: 90 * time.Second}
	get := func(rel string, required bool) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", base+rel, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			if required {
				return nil, fmt.Errorf("%s: %s", rel, resp.Status)
			}
			return nil, nil
		}
		return io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	}
	save := func(rel string, b []byte) error {
		path := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, b, 0o644)
	}
	planetJSON, err := get("planet.json", true)
	if err != nil {
		return err
	}
	var planet struct {
		Articles []render.PublicPost `json:"articles"`
	}
	if err := json.Unmarshal(planetJSON, &planet); err != nil {
		return fmt.Errorf("planet.json: %w", err)
	}
	if err := save("planet.json", planetJSON); err != nil {
		return err
	}
	for _, f := range []string{"templateSettings.json", "avatar.png", "favicon.ico"} {
		if b, err := get(f, false); err == nil && b != nil {
			if err := save(f, b); err != nil {
				return err
			}
		}
	}
	// posts in parallel, a few at a time
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, a := range planet.Articles {
		a := a
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			names := []string{"article.json", "nft.json", "nft.json.cid.txt", "_cover.png", "_videoThumbnail.png"}
			names = append(names, a.Attachments...)
			for i, name := range names {
				b, err := get(a.ID+"/"+url.PathEscape(name), false)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("%s/%s: %w", a.ID, name, err)
					}
					mu.Unlock()
					return
				}
				if b == nil {
					if i == 0 { // no article.json on the gateway: use the inline copy
						b, _ = json.Marshal(a)
					} else {
						continue
					}
				}
				if err := save(a.ID+"/"+name, b); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	return firstErr
}

// prewarm asks the public gateways for the site so they fetch and cache it
// while this node is still online, as Planet does after every publish. It
// returns once every request has finished or the deadline passes.
func (p *Publisher) prewarm(ctx context.Context, site *store.Site, cid string) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	root := gateway.URL(site)
	urls := []string{root, root + "planet.json", root + "avatar.png", root + "favicon.ico", root + "rss.xml", gateway.CIDURL(site, cid), gateway.CIDURL(site, cid) + "planet.json"}
	client := &http.Client{Timeout: 80 * time.Second}
	var wg sync.WaitGroup
	for _, u := range urls {
		u := u
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
			if err != nil {
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				p.log("prewarm %s: %v", strings.TrimPrefix(u, "https://"), err)
				return
			}
			io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<20))
			resp.Body.Close()
			p.log("prewarm %s: %s", strings.TrimPrefix(u, "https://"), resp.Status)
		}()
	}
	wg.Wait()
}
