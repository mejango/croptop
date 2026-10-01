package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

// NewPost is what `croptop post --key` adds to a site.
type NewPost struct {
	Title, Content string
	Tags           string   // comma separated, as in the console
	Files          []string // attachments
}

// Posted is where a new post went.
type Posted struct {
	Result
	URL string
}

// postEngine is what Post needs beyond ipfs.Engine; the embedded engine has it.
type postEngine interface {
	blockLister
	AddOver(ctx context.Context, base, dir string) (string, error)
	SignRecord(key, c string, seq uint64) ([]byte, error)
	FileCID(ctx context.Context, path string) (string, error)
}

// Post adds one post to a site from nothing but its key, for machines that
// keep no copy of the site, such as an agent's short-lived worker. It builds
// on the version hostURL holds, renders only the new post, and pushes only
// what changed: the post's folder, planet.json, rss.xml, and pages for new
// tags. Every other file stays as the owner's machine rendered it. The host
// refuses the push if the site changed in the meantime, so no post is lost,
// and it announces the new version, so this machine can exit right after.
func (p *Publisher) Post(ctx context.Context, hostURL string, key []byte, np NewPost) (Posted, error) {
	eng, ok := p.Node.(postEngine)
	if !ok {
		return Posted{}, errors.New("posting with a key needs the embedded engine (croptop --engine embedded)")
	}
	if strings.TrimSpace(np.Title+np.Content) == "" && len(np.Files) == 0 {
		return Posted{}, errors.New("nothing to post: give a title, content, or files")
	}
	ks := p.Node.Keystore()
	keyName := "post-" + store.NewID() // not the site's id: a planet.json must not choose which key gets replaced
	if err := importKey(ks, keyName, key); err != nil {
		return Posted{}, fmt.Errorf("key: %w", err)
	}
	defer ks.Delete(keyName)
	ipnsName, err := ks.Name(keyName)
	if err != nil {
		return Posted{}, err
	}

	entry, err := hostEntry(ctx, hostURL, ipnsName)
	if err != nil {
		return Posted{}, err
	}
	if !entry.AcceptsParent {
		// an older host would take the post for the whole site and lose the rest
		return Posted{}, fmt.Errorf("%s cannot add a post to a site yet; it needs updating", hostURL)
	}
	seq := entry.Sequence + 1
	nctx, cancel := context.WithTimeout(ctx, networkTimeout)
	rec, netErr := p.Node.NetworkRecord(nctx, ipnsName)
	cancel()
	if netErr == nil {
		if rec.Sequence > entry.Sequence && rec.Value != "/ipfs/"+entry.CID {
			return Posted{}, fmt.Errorf("the network has a newer version of this site (sequence %d) than %s (sequence %d); publish it from the machine that made it, then post again", rec.Sequence, hostURL, entry.Sequence)
		}
		seq = max(seq, rec.Sequence+1)
	}

	tmp, err := os.MkdirTemp(p.Store.Root, "post-*")
	if err != nil {
		return Posted{}, err
	}
	defer os.RemoveAll(tmp)
	published := filepath.Join(tmp, "published")
	p.log("reading %s", entry.CID)
	links, err := p.readVersion(ctx, eng, hostURL, entry.CID, published)
	if err != nil {
		return Posted{}, err
	}
	var head struct {
		ID   string `json:"id"`
		IPNS string `json:"ipns"`
	}
	if b, err := os.ReadFile(filepath.Join(published, "planet.json")); err != nil || json.Unmarshal(b, &head) != nil || head.ID == "" {
		return Posted{}, fmt.Errorf("%s is not a Croptop site (no planet.json with an id)", entry.CID)
	}
	if head.IPNS != ipnsName {
		return Posted{}, fmt.Errorf("that version is the site %s, not %s", head.IPNS, ipnsName)
	}
	st := &store.Store{Root: filepath.Join(tmp, "store")}
	if err := rebuildSource(st, head.ID, published); err != nil {
		return Posted{}, err
	}
	site, err := st.Site(head.ID)
	if err != nil {
		return Posted{}, err
	}
	if NameOf(site) == "" && entry.Name != "" { // sites published before planet.json carried it
		setRaw(site, NameKey, entry.Name)
	}
	SetHost(site, hostURL)
	if err := st.SaveSite(site); err != nil {
		return Posted{}, err
	}
	if err := navigationPages(st, site.ID, published); err != nil {
		return Posted{}, err
	}
	post, err := addPost(st, site.ID, np)
	if err != nil {
		return Posted{}, err
	}
	r := &render.Renderer{Store: st, Templates: p.Render.Templates, TemplateFor: p.Render.TemplateFor, CIDs: p.Render.CIDs, FFmpeg: p.Render.FFmpeg, Log: p.Render.Log, Only: post.ID}
	if err := r.Render(ctx, site.ID); err != nil {
		return Posted{}, fmt.Errorf("render: %w", err)
	}

	changed := filepath.Join(tmp, "changed")
	if err := os.MkdirAll(changed, 0o755); err != nil {
		return Posted{}, err
	}
	names := []string{post.ID, "planet.json", "rss.xml"}
	for t := range post.Tags {
		if page := render.TagPage(t); page != "" && links[page] == "" {
			names = append(names, page)
		}
	}
	for _, n := range names {
		src := filepath.Join(st.PublicDir(site.ID), n)
		if _, err := os.Stat(src); err != nil {
			continue // a template without tag pages
		}
		if err := os.Rename(src, filepath.Join(changed, n)); err != nil {
			return Posted{}, err
		}
	}
	cid, err := eng.AddOver(ctx, entry.CID, changed)
	if err != nil {
		return Posted{}, err
	}
	if _, err := eng.SignRecord(keyName, cid, seq); err != nil {
		return Posted{}, err
	}
	p.log("pushing %s at sequence %d", cid, seq)
	if err := p.pushDir(ctx, site, keyName, cid, seq, pushSpec{Parent: entry.CID, Dir: changed}); err != nil {
		return Posted{}, fmt.Errorf("push to %s: %w", hostURL, err)
	}
	return Posted{Result: Result{CID: cid, Sequence: seq}, URL: render.BrowserURL(site, post)}, nil
}

type hostKey struct {
	CID             string `json:"cid"`
	Sequence        uint64 `json:"sequence"`
	Name            string `json:"name"`
	AcceptsParent   bool   `json:"acceptsParent"`
	AcceptsManifest bool   `json:"acceptsManifest"`
}

// errNoVersion is what hostEntry's error wraps when the host holds no version
// of the site.
var errNoVersion = errors.New("no version there")

// hostEntry is the version of a site the host holds.
func hostEntry(ctx context.Context, hostURL, ipnsName string) (*hostKey, error) {
	b, status, err := httpGet(ctx, hostURL+"/v0/host/keys/"+ipnsName)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", hostURL, err)
	}
	var e hostKey
	if status == 404 || (status == 200 && json.Unmarshal(b, &e) == nil && e.CID == "") {
		return nil, fmt.Errorf("%s holds no version of %s; publish the site once from the console, which pushes it there (%w)", hostURL, ipnsName, errNoVersion)
	}
	if status != 200 || e.CID == "" {
		return nil, fmt.Errorf("%s: %d %s", hostURL, status, strings.TrimSpace(string(b)))
	}
	return &e, nil
}

// readVersion puts the root listing of version c and the few files a new
// post needs into dest.
func (p *Publisher) readVersion(ctx context.Context, eng postEngine, hostURL, c, dest string) (map[string]string, error) {
	links, err := p.links(ctx, eng, hostURL, c)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", c, err)
	}
	for _, name := range []string{"planet.json", "templateSettings.json", "avatar.png", "index.html"} {
		if want, ok := links[name]; ok {
			if err := p.readFile(ctx, eng, hostURL, c, name, want, filepath.Join(dest, name)); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return links, nil
}

// blockLister lists folders, storing blocks fetched from a host first.
type blockLister interface {
	Links(ctx context.Context, c string) (map[string]string, error)
	PutBlock(ctx context.Context, c string, data []byte) error
	Block(ctx context.Context, c string) ([]byte, error) // only what this node holds
}

// links lists the directory c. Its block comes from the host first, which
// has a version the moment it is pushed, and is checked against c; the IPFS
// network is the fallback. A listing cut short is an error, never a part of
// one: the engine answers a sharded folder whose other blocks never came with
// what it had read when the time ran out, and no error.
func (p *Publisher) links(ctx context.Context, eng blockLister, hostURL, c string) (map[string]string, error) {
	if _, err := eng.Block(ctx, c); err != nil { // not here: the host has it the moment it is pushed
		if b, status, err := httpGet(ctx, hostURL+"/v0/host/blocks/"+c); err == nil && status == 200 {
			if err := eng.PutBlock(ctx, c, b); err != nil {
				p.log("block %s from %s: %v", c, hostURL, err)
			}
		}
	}
	lctx, cancel := context.WithTimeout(ctx, ipfsFetchTimeout)
	defer cancel()
	links, err := eng.Links(lctx, c)
	if err == nil && lctx.Err() != nil {
		return nil, fmt.Errorf("%s: %w", c, lctx.Err())
	}
	return links, err
}

// readFile puts the file at rel in version root into dest. want is its CID
// from its folder's listing: bytes from the host are kept only if they hash
// to it, otherwise the file comes over IPFS.
func (p *Publisher) readFile(ctx context.Context, eng postEngine, hostURL, root, rel, want, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	segs := strings.Split(rel, "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	if b, status, err := httpGet(ctx, hostURL+"/ipfs/"+root+"/"+strings.Join(segs, "/")); err == nil && status == 200 && os.WriteFile(dest, b, 0o644) == nil {
		if got, _ := eng.FileCID(ctx, dest); got == want {
			return nil
		}
		p.log("%s from %s does not match %s; reading it over IPFS", rel, hostURL, root)
		os.Remove(dest)
	}
	gctx, cancel := context.WithTimeout(ctx, ipfsFetchTimeout)
	defer cancel()
	return p.Node.Get(gctx, "/ipfs/"+want, dest)
}

func httpGet(ctx context.Context, u string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return b, resp.StatusCode, err
}

// navigationPages recreates the site's navigation as pages that hold only
// what the navigation shows, read from the rendered index.html. The new post's
// page then shows the same navigation without the site's pages at hand.
func navigationPages(st *store.Store, siteID, pubDir string) error {
	yes, empty := true, ""
	for n, m := range navItems(pubDir) {
		href := strings.TrimSpace(m[1])
		w := n
		page := &store.Post{
			ID: fmt.Sprintf("NAV-%d", n), ArticleType: 1, Title: m[2], // written unescaped, as Stencil does
			Attachments: []string{}, IsIncludedInNavigation: &yes, NavigationWeight: &w, Summary: &empty,
		}
		if slug, ok := strings.CutPrefix(href, "./"); ok {
			slug = strings.Trim(slug, "/")
			page.Slug = &slug
		} else {
			page.ExternalLink = &href
		}
		page.Link = "/" + page.Dir() + "/"
		if err := st.SavePost(siteID, page); err != nil {
			return err
		}
	}
	return nil
}

// addPost saves np in st the way the console's new-post form does.
func addPost(st *store.Store, siteID string, np NewPost) (*store.Post, error) {
	id, empty := store.NewID(), ""
	post := &store.Post{
		ID: id, Title: np.Title, Content: np.Content, Created: store.Now(), Link: "/" + id + "/",
		Attachments: []string{}, CIDs: map[string]string{}, Tags: map[string]string{}, Summary: &empty, Slug: &empty,
	}
	for _, t := range strings.Split(np.Tags, ",") {
		if t = strings.TrimSpace(t); t == "" {
			continue
		}
		if render.TagPage(t) == "" {
			return nil, fmt.Errorf("tag %q cannot name a page: no slashes, quotes, or angle brackets, and not index, page1, or tags", t)
		}
		post.Tags[t] = t
	}
	for _, f := range np.Files {
		name := filepath.Base(f)
		if strings.HasPrefix(name, "_") {
			return nil, fmt.Errorf("%s: names starting with _ are kept for generated files", name)
		}
		for _, a := range post.Attachments {
			if a == name {
				return nil, fmt.Errorf("two attachments named %s", name)
			}
		}
		if err := copyFile(f, filepath.Join(st.PostDir(siteID, id), name)); err != nil {
			return nil, err
		}
		post.Attachments = append(post.Attachments, name)
	}
	sort.Strings(post.Attachments)
	return post, st.SavePost(siteID, post)
}

// importKey installs a site key from croptop's PEM export or from the raw
// keystore bytes in Planet's .site export.
func importKey(ks *ipfs.Keystore, name string, b []byte) error {
	if bytes.Contains(b, []byte("-----BEGIN")) {
		return ks.ImportPEM(name, b)
	}
	return ks.ImportRaw(name, b)
}
