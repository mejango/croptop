package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

// Adopt takes over a published site on this machine from just its IPNS name
// (or ENS domain) and private key: the published tree is fetched from IPFS
// and the source files rebuilt from it.
func (p *Publisher) Adopt(ctx context.Context, nameOrENS string, pemBytes []byte) (string, error) {
	name := strings.TrimPrefix(strings.TrimSpace(nameOrENS), "/ipns/")
	if strings.HasSuffix(name, ".eth") {
		p.log("resolving %s", name)
		resolved, err := p.Node.Resolve(ctx, "/ipns/"+name)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", name, err)
		}
		name = strings.TrimPrefix(resolved, "/ipns/")
		if strings.HasPrefix(name, "/ipfs/") {
			return "", fmt.Errorf("%s points at a fixed CID, not an IPNS name; Croptop cannot update it", nameOrENS)
		}
	}
	p.log("resolving IPNS %s", name)
	path, err := p.Node.Resolve(ctx, "/ipns/"+name)
	if err != nil {
		return "", fmt.Errorf("resolve /ipns/%s: %w", name, err)
	}
	cid := strings.TrimPrefix(path, "/ipfs/")
	tmp, err := os.MkdirTemp(p.Store.Root, "adopt-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	p.log("fetching %s", cid)
	if err := p.fetchSite(ctx, name, cid, filepath.Join(tmp, "site")); err != nil {
		return "", err
	}
	pubDir := filepath.Join(tmp, "site")
	var head struct {
		ID string `json:"id"`
	}
	b, err := os.ReadFile(filepath.Join(pubDir, "planet.json"))
	if err != nil {
		return "", fmt.Errorf("%s is not a Planet/Croptop site (no planet.json)", name)
	}
	if err := json.Unmarshal(b, &head); err != nil || head.ID == "" {
		return "", fmt.Errorf("planet.json has no id")
	}
	ks := p.Node.Keystore()
	if ks.Has(head.ID) {
		return "", fmt.Errorf("a key for %s already exists on this machine", head.ID)
	}
	// Planet's .site export ships the key as kubo's raw keystore bytes; the
	// console and croptop's own export use PEM
	importKey := ks.ImportPEM
	if !bytes.Contains(pemBytes, []byte("-----BEGIN")) {
		importKey = ks.ImportRaw
	}
	if err := importKey(head.ID, pemBytes); err != nil {
		return "", err
	}
	derived, err := ks.Name(head.ID)
	if err != nil {
		return "", err
	}
	if derived != name {
		ks.Delete(head.ID)
		return "", fmt.Errorf("that key belongs to %s, not %s", derived, name)
	}
	if _, err := os.Stat(p.Store.SiteDir(head.ID)); err == nil {
		return "", fmt.Errorf("site %s already exists here", head.ID)
	}
	os.RemoveAll(p.Store.PublicDir(head.ID))
	if err := os.MkdirAll(filepath.Dir(p.Store.PublicDir(head.ID)), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(pubDir, p.Store.PublicDir(head.ID)); err != nil {
		return "", err
	}
	if err := rebuildSource(p.Store, head.ID, p.Store.PublicDir(head.ID)); err != nil {
		return "", err
	}
	site, err := p.Store.Site(head.ID)
	if err != nil {
		return "", err
	}
	site.LastPublishedCID = &cid
	if rec, err := p.Node.NetworkRecord(ctx, name); err == nil {
		site.IPNSSequence = rec.Sequence
	}
	return head.ID, p.Store.SaveSite(site)
}

// rebuildSource reconstructs sites/<id>/ from a published tree. Everything
// the template needs is in the public files, so a site is recoverable from
// the network plus its key.
func rebuildSource(st *store.Store, siteID, pubDir string) error {
	b, err := os.ReadFile(filepath.Join(pubDir, "planet.json"))
	if err != nil {
		return err
	}
	var pub map[string]json.RawMessage
	if err := json.Unmarshal(b, &pub); err != nil {
		return err
	}
	var articles []render.PublicPost
	if raw, ok := pub["articles"]; ok {
		if err := json.Unmarshal(raw, &articles); err != nil {
			return err
		}
	}
	delete(pub, "articles")
	pub["templateName"] = json.RawMessage(`"Croptop"`)
	siteJSON, _ := json.Marshal(pub)
	var site store.Site
	if err := json.Unmarshal(siteJSON, &site); err != nil {
		return err
	}
	if site.ID != siteID {
		return fmt.Errorf("planet.json id %s does not match %s", site.ID, siteID)
	}
	if err := st.SaveSite(&site); err != nil {
		return err
	}
	for _, f := range []string{"avatar.png", "favicon.ico", "templateSettings.json"} {
		src := filepath.Join(pubDir, f)
		if _, err := os.Stat(src); err == nil {
			if err := copyFile(src, filepath.Join(st.SiteDir(siteID), f)); err != nil {
				return err
			}
		}
	}
	for _, a := range articles {
		postDir := filepath.Join(pubDir, a.ID)
		// prefer the per-post article.json; it is the same shape
		if pb, err := os.ReadFile(filepath.Join(postDir, "article.json")); err == nil {
			var full render.PublicPost
			if json.Unmarshal(pb, &full) == nil && full.ID == a.ID {
				a = full
			}
		}
		post := postFromPublic(a) // article.json carries the exact content; article.md is title + content
		if err := st.SavePost(siteID, post); err != nil {
			return err
		}
		names := append([]string{}, post.Attachments...)
		names = append(names, "_cover.png", "_videoThumbnail.png")
		for _, name := range names {
			src := filepath.Join(postDir, name)
			if _, err := os.Stat(src); err != nil {
				continue
			}
			if err := copyFile(src, filepath.Join(st.PostDir(siteID, post.ID), name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func postFromPublic(a render.PublicPost) *store.Post {
	p := &store.Post{
		ID: a.ID, Title: a.Title, Content: a.Content, Created: a.Created, ArticleType: a.ArticleType,
		Link: "/" + a.ID + "/", Attachments: a.Attachments, CIDs: a.CIDs, Tags: a.Tags,
		HeroImageWidth: a.HeroImageWidth, HeroImageHeight: a.HeroImageHeight,
		VideoFilename: a.VideoFilename, AudioFilename: a.AudioFilename, Modified: a.Modified, Pinned: a.Pinned,
	}
	if a.ContentRendered != "" {
		s := a.ContentRendered
		p.ContentRendered = &s
	}
	if a.Slug != "" {
		s := a.Slug
		p.Slug = &s
	}
	if a.ExternalLink != "" {
		s := a.ExternalLink
		p.ExternalLink = &s
	}
	if a.HeroImageFilename != nil && *a.HeroImageFilename != "" && *a.HeroImageFilename != "_videoThumbnail.png" {
		if render.HeroImage(p) != *a.HeroImageFilename { // only when it was chosen explicitly
			p.HeroImage = a.HeroImageFilename
		}
	}
	if p.Attachments == nil {
		p.Attachments = []string{}
	}
	empty := "" // Planet posts carry an empty summary; nft.json's description depends on it
	p.Summary = &empty
	return p
}
