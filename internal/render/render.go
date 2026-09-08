// Package render turns a site's source files into the public tree the
// Croptop template's JavaScript expects, matching the Mac app's output.
package render

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/mejango/croptop/internal/store"
)

type CIDer interface {
	FileCIDv0(ctx context.Context, path string) (string, error)
}

type Renderer struct {
	Store     *store.Store
	Templates fs.FS // template.json, templates/, assets/
	CIDs      CIDer
	FFmpeg    string // path to ffmpeg, "" when unavailable
	Log       func(string)
}

func (r *Renderer) log(format string, a ...any) {
	if r.Log != nil {
		r.Log(fmt.Sprintf(format, a...))
	}
}

// Render writes the whole site into Store.PublicDir(siteID).
func (r *Renderer) Render(ctx context.Context, siteID string) error {
	site, err := r.Store.Site(siteID)
	if err != nil {
		return err
	}
	posts, err := r.Store.Posts(siteID)
	if err != nil {
		return err
	}
	meta, err := LoadMeta(r.Templates)
	if err != nil {
		return err
	}
	pub := r.Store.PublicDir(siteID)
	if err := os.MkdirAll(pub, 0o755); err != nil {
		return err
	}
	if err := copyFS(r.Templates, "assets", filepath.Join(pub, "assets")); err != nil {
		return err
	}
	for _, f := range []string{"avatar.png", "favicon.ico"} {
		src := filepath.Join(r.Store.SiteDir(siteID), f)
		if _, err := os.Stat(src); err == nil {
			if err := copyFile(src, filepath.Join(pub, f)); err != nil {
				return err
			}
		}
	}
	stored, err := r.Store.TemplateSettings(siteID)
	if err != nil {
		return err
	}
	settings := meta.SettingsWithDefaults(stored)
	if err := writeSwiftJSON(filepath.Join(pub, "templateSettings.json"), settings); err != nil {
		return err
	}

	eng := newEngine(r.Templates)
	base := r.baseContext(site, posts, meta, settings, pub)

	SortForIndex(posts)
	articles := make([]map[string]any, 0, len(posts))
	for _, p := range posts {
		pp, err := r.renderPost(ctx, eng, site, p, meta, base)
		if err != nil {
			return fmt.Errorf("post %s: %w", p.ID, err)
		}
		articles = append(articles, contextArticle(pp))
	}

	// planet.json with articles inline
	planet := site.Public()
	pubPosts := make([]PublicPost, 0, len(posts))
	for _, p := range posts {
		pubPosts = append(pubPosts, NewPublicPost(site, p))
	}
	planetMap := map[string]any{}
	for k, v := range planet {
		planetMap[k] = v
	}
	planetMap["articles"] = pubPosts
	if err := writeSwiftJSON(filepath.Join(pub, "planet.json"), planetMap); err != nil {
		return err
	}

	// index and tag pages
	base["articles"] = articles
	idx := merge(base, map[string]any{
		"assets_prefix":     "./",
		"page_title":        site.Name,
		"current_item_type": "index",
		"current_page":      1,
		"total_pages":       1,
	})
	html, err := eng.render("index.html", idx)
	if err != nil {
		return err
	}
	for _, name := range []string{"index.html", "page1.html"} {
		if err := os.WriteFile(filepath.Join(pub, name), []byte(html), 0o644); err != nil {
			return err
		}
	}
	if meta.GenerateTagPages {
		byTag := map[string][]map[string]any{}
		for _, a := range articles {
			if tags, ok := a["tags"].(map[string]any); ok {
				for k := range tags {
					byTag[k] = append(byTag[k], a)
				}
			}
		}
		for key, list := range byTag {
			value := key
			if site.Tags != nil && site.Tags[key] != "" {
				value = site.Tags[key]
			}
			tctx := merge(idx, map[string]any{
				"tag_key": key, "tag_value": value, "current_item_type": "tags",
				"articles": list, "page_title": site.Name + " - " + value,
			})
			html, err := eng.render("index.html", tctx)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(pub, key+".html"), []byte(html), 0o644); err != nil {
				return err
			}
		}
		if eng.has("tags.html") {
			html, err := eng.render("tags.html", merge(idx, map[string]any{"current_item_type": "tags", "tags": site.Tags}))
			if err != nil {
				return err
			}
			os.WriteFile(filepath.Join(pub, "tags.html"), []byte(html), 0o644)
		}
	}
	robots := ""
	if v, ok := site.Raw["doNotIndex"]; ok && string(v) == "true" {
		robots = "User-agent: *\nDisallow: /"
	}
	return os.WriteFile(filepath.Join(pub, "robots.txt"), []byte(robots), 0o644)
}

// RenderPost re-renders one post (and planet.json) without touching the others.
func (r *Renderer) RenderPost(ctx context.Context, siteID, postID string) error {
	// ponytail: full render is fast enough for a few hundred posts; per-post rendering later if needed
	return r.Render(ctx, siteID)
}

func (r *Renderer) baseContext(site *store.Site, posts []*store.Post, meta *Meta, settings map[string]any, pub string) map[string]any {
	planet := toMap(site.Public())
	tagKeys := []string{}
	for k := range site.Tags {
		tagKeys = append(tagKeys, k)
	}
	sort.Strings(tagKeys)
	planet["tags"] = tagKeys // {% for tag in planet.tags %} prints keys

	nav := []map[string]any{}
	for _, p := range posts {
		if p.IsIncludedInNavigation == nil || !*p.IsIncludedInNavigation {
			continue
		}
		weight := 1
		if p.NavigationWeight != nil {
			weight = *p.NavigationWeight
		}
		ext := ""
		if p.ExternalLink != nil {
			ext = *p.ExternalLink
		}
		nav = append(nav, map[string]any{"id": p.ID, "title": p.Title, "slug": p.Dir(), "externalLink": ext, "weight": weight})
	}
	sort.SliceStable(nav, func(i, j int) bool { return nav[i]["weight"].(int) < nav[j]["weight"].(int) })

	hasAvatar := false
	if _, err := os.Stat(filepath.Join(pub, "avatar.png")); err == nil {
		hasAvatar = true
	}
	hasPodcast := false
	for _, p := range posts {
		if p.AudioFilename != nil && *p.AudioFilename != "" {
			hasPodcast = true
		}
	}
	styleHash := ""
	if css, err := fs.ReadFile(r.Templates, "assets/style.css"); err == nil {
		sum := sha256.Sum256(css)
		styleHash = hex.EncodeToString(sum[:])
	}
	tmplSettings := map[string]any{}
	for k, s := range meta.Settings {
		tmplSettings[k] = toMap(s)
	}
	userSettings := map[string]any{}
	for k, v := range settings {
		userSettings[k] = v
		if strings.HasSuffix(k, "Color") {
			userSettings[k+"Filter"] = "" // ponytail: Planet solves a CSS filter for colors; the Croptop template never reads it
		}
	}
	ctx := map[string]any{
		"planet":                planet,
		"planet_ipns":           site.IPNS,
		"site_navigation":       nav,
		"has_avatar":            hasAvatar,
		"has_podcast":           hasPodcast,
		"og_image_url":          SiteURL(site) + "avatar.png",
		"page_description":      site.About,
		"page_description_html": Markdown(site.About),
		"template_settings":     tmplSettings,
		"user_settings":         userSettings,
		"build_timestamp":       time.Now().Unix(),
		"style_css_sha256":      styleHash,
	}
	for _, slot := range []string{"Head", "BodyStart", "BodyEnd"} {
		key := "custom_code_" + strings.ToLower(strings.ReplaceAll(slot, "Body", "body_"))
		ctx[key] = ""
		if en, ok := site.Raw["customCode"+slot+"Enabled"]; ok && string(en) == "true" {
			if v, ok := site.Raw["customCode"+slot]; ok {
				var s string
				if err := jsonUnmarshal(v, &s); err == nil {
					ctx[key] = s
				}
			}
		}
	}
	return ctx
}

func (r *Renderer) renderPost(ctx context.Context, eng *engine, site *store.Site, p *store.Post, meta *Meta, base map[string]any) (PublicPost, error) {
	dir := filepath.Join(r.Store.PublicDir(site.ID), p.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return PublicPost{}, err
	}
	dirty := false
	src := r.Store.PostDir(site.ID, p.ID)
	for _, a := range p.Attachments {
		if _, err := os.Stat(filepath.Join(src, a)); err == nil {
			if err := copyFile(filepath.Join(src, a), filepath.Join(dir, a)); err != nil {
				return PublicPost{}, err
			}
		}
	}
	ops, err := r.Store.Ops(site.ID)
	if err != nil {
		return PublicPost{}, err
	}

	// cover image for text-only and audio posts
	cover := filepath.Join(dir, "_cover.png")
	needsCover := p.TextOnly() || (p.AudioFilename != nil && *p.AudioFilename != "")
	if needsCover {
		text := p.Title
		if strings.TrimSpace(p.Content) != "" {
			text = p.Content
		}
		if p.AudioFilename != nil && *p.AudioFilename != "" {
			text = p.Title + "\n\n(audio)"
		}
		if p.VideoFilename != nil && *p.VideoFilename != "" {
			text = p.Title + "\n\n(video)"
		}
		opKey := p.ID + "-cover-" + shortHash(text)
		_, exists := os.Stat(cover)
		if exists != nil || (!hasOp(ops, opKey) && hasOpPrefix(ops, p.ID+"-cover-")) {
			if err := WriteCover(cover, text); err != nil {
				return PublicPost{}, err
			}
			r.Store.RecordOp(site.ID, opKey)
			delete(p.CIDs, "_cover.png")
		} else if !hasOpPrefix(ops, p.ID+"-cover-") {
			r.Store.RecordOp(site.ID, opKey) // imported cover: keep as is
		}
		if p.TextOnly() && !contains(p.Attachments, "_cover.png") {
			p.Attachments = append(p.Attachments, "_cover.png")
			dirty = true
		}
	}

	// video thumbnail
	if p.VideoFilename != nil && *p.VideoFilename != "" {
		thumb := filepath.Join(dir, "_videoThumbnail.png")
		if _, err := os.Stat(thumb); err != nil && r.FFmpeg != "" {
			cmd := exec.CommandContext(ctx, r.FFmpeg, "-y", "-i", filepath.Join(dir, *p.VideoFilename), "-frames:v", "1", thumb)
			if out, err := cmd.CombinedOutput(); err != nil {
				r.log("ffmpeg thumbnail failed for %s: %s", p.ID, strings.TrimSpace(string(out)))
			}
		}
	}

	// attachment CIDs (CIDv0, like Planet)
	if p.CIDs == nil {
		p.CIDs = map[string]string{}
	}
	for _, a := range p.Attachments {
		path := filepath.Join(dir, a)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if _, ok := p.CIDs[a]; ok {
			continue
		}
		cid, err := r.CIDs.FileCIDv0(ctx, path)
		if err != nil {
			return PublicPost{}, err
		}
		p.CIDs[a] = cid
		dirty = true
	}

	// hero image dimensions
	if hero := HeroImage(p); hero != "" && p.HeroImageWidth == nil {
		if f, err := os.Open(filepath.Join(dir, hero)); err == nil {
			if cfg, _, err := image.DecodeConfig(f); err == nil {
				w, h := cfg.Width, cfg.Height
				p.HeroImageWidth, p.HeroImageHeight = &w, &h
				dirty = true
			}
			f.Close()
		}
	}

	// markdown
	html := Markdown(p.Content)
	if p.ContentRendered == nil || *p.ContentRendered != html {
		p.ContentRendered = &html
		dirty = true
	}

	pp := NewPublicPost(site, p)
	if err := writeSwiftJSON(filepath.Join(dir, "article.json"), pp); err != nil {
		return pp, err
	}
	if err := os.WriteFile(filepath.Join(dir, "article.md"), []byte(p.Content), 0o644); err != nil {
		return pp, err
	}

	if meta.GenerateNFTMetadata && len(p.CIDs) > 0 {
		if err := r.writeNFT(ctx, dir, site, p, pp, ops); err != nil {
			return pp, err
		}
	}

	article := contextArticle(pp)
	pctx := merge(base, map[string]any{
		"assets_prefix":     "../",
		"article":           article,
		"article_id":        p.ID,
		"article_type":      p.ArticleType,
		"article_title":     p.Title,
		"article_summary":   deref(p.Summary),
		"page_title":        p.Title,
		"content_html":      html,
		"current_item_type": "blog",
		"social_image_url":  firstNonEmpty(deref(pp.HeroImageURL), SiteURL(site)+"avatar.png"),
	})
	for tmpl, out := range map[string]string{"blog.html": "index.html", "simple.html": "simple.html"} {
		if !eng.has(tmpl) {
			continue
		}
		s, err := eng.render(tmpl, pctx)
		if err != nil {
			return pp, err
		}
		if err := os.WriteFile(filepath.Join(dir, out), []byte(s), 0o644); err != nil {
			return pp, err
		}
	}
	if p.Dir() != p.ID {
		slugDir := filepath.Join(r.Store.PublicDir(site.ID), p.Dir())
		os.RemoveAll(slugDir)
		if err := os.CopyFS(slugDir, os.DirFS(dir)); err != nil {
			return pp, err
		}
	}
	if dirty {
		if err := r.Store.SavePost(site.ID, p); err != nil {
			return pp, err
		}
	}
	return pp, nil
}

type nftAttribute struct {
	TraitType string `json:"trait_type"`
	Value     string `json:"value"`
}

type nftMetadata struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Image        string         `json:"image"`
	ExternalURL  string         `json:"external_url"`
	MimeType     string         `json:"mimeType"`
	AnimationURL *string        `json:"animation_url,omitempty"`
	Attributes   []nftAttribute `json:"attributes"`
}

// writeNFT mirrors Planet's processNFTMetadata. The CID of nft.json keys the
// onchain tier, so an existing nft.json is only rewritten when the inputs
// changed, and imported ones are adopted as they are.
func (r *Renderer) writeNFT(ctx context.Context, dir string, site *store.Site, p *store.Post, pp PublicPost, ops map[string]store.AppleTime) error {
	first := ""
	for _, a := range p.Attachments {
		if _, ok := p.CIDs[a]; ok {
			first = a
			break
		}
	}
	if first == "" {
		for k := range p.CIDs { // Planet takes an arbitrary "first" here
			first = k
			break
		}
	}
	imageCID := p.CIDs[first]
	var animation *string
	if pp.HasVideo {
		if cid, err := r.CIDs.FileCIDv0(ctx, filepath.Join(dir, "_videoThumbnail.png")); err == nil {
			imageCID = cid
		}
		if cid, ok := p.CIDs[*p.VideoFilename]; ok {
			u := "https://ipfs.io/ipfs/" + cid
			animation = &u
		}
	}
	if pp.HasAudio {
		if cid, err := r.CIDs.FileCIDv0(ctx, filepath.Join(dir, "_cover.png")); err == nil {
			imageCID = cid
		}
		if cid, ok := p.CIDs[*p.AudioFilename]; ok {
			u := "https://ipfs.io/ipfs/" + cid
			animation = &u
		}
	}
	nftPath := filepath.Join(dir, "nft.json")
	cidPath := filepath.Join(dir, "nft.json.cid.txt")
	opKey := p.ID + "-nft-" + shortHash(p.Title+"\x00"+p.Content+"\x00"+imageCID+"\x00"+deref(animation)+"\x00"+fmt.Sprint(p.Created.Unix()))
	_, haveNFT := os.Stat(nftPath)
	_, haveCID := os.Stat(cidPath)
	if haveNFT == nil && haveCID == nil {
		if hasOp(ops, opKey) {
			return nil
		}
		if !hasOpPrefix(ops, p.ID+"-nft-") {
			return r.Store.RecordOp(site.ID, opKey) // imported: adopt as is
		}
	}
	attrs := []nftAttribute{
		{"title", p.Title},
		{"title_sha256", sha256hex(p.Title)},
	}
	if p.Content != "" {
		attrs = append(attrs, nftAttribute{"content_sha256", sha256hex(p.Content)})
	}
	attrs = append(attrs, nftAttribute{"created_at", fmt.Sprint(p.Created.Unix())})
	external := BrowserURL(site, p)
	if p.ExternalLink != nil && *p.ExternalLink != "" {
		external = *p.ExternalLink
	}
	description := first
	if p.Summary != nil {
		description = *p.Summary
	}
	nft := nftMetadata{
		Name: p.Title, Description: description, Image: "https://ipfs.io/ipfs/" + imageCID,
		ExternalURL: external, MimeType: mimeOf(first), AnimationURL: animation, Attributes: attrs,
	}
	if err := writeSwiftJSON(nftPath, nft); err != nil {
		return err
	}
	cid, err := r.CIDs.FileCIDv0(ctx, nftPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(cidPath, []byte(cid), 0o644); err != nil {
		return err
	}
	return r.Store.RecordOp(site.ID, opKey)
}

func mimeOf(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext { // match macOS UTType for the common cases
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".m4a":
		return "audio/mp4"
	case ".mov":
		return "video/quicktime"
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return strings.Split(t, ";")[0]
	}
	return "application/octet-stream"
}

// contextArticle is a PublicPost as the template sees it, with `created`
// expanded so `article.created.timeIntervalSince1970` resolves.
func contextArticle(pp PublicPost) map[string]any {
	m := toMap(pp)
	m["created"] = dateValue(pp.Created.Unix())
	if pp.Modified != nil {
		m["modified"] = dateValue(pp.Modified.Unix())
	}
	return m
}

func merge(base map[string]any, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

func writeSwiftJSON(path string, v any) error {
	b, err := SwiftJSON(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func copyFS(fsys fs.FS, dir, dest string) error {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return err
	}
	return fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func shortHash(s string) string { return sha256hex(s)[:16] }

func hasOp(ops map[string]store.AppleTime, key string) bool { _, ok := ops[key]; return ok }

func hasOpPrefix(ops map[string]store.AppleTime, prefix string) bool {
	for k := range ops {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
