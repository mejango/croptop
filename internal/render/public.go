package render

import (
	"encoding/json"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/store"
)

// SiteURL is the site's canonical public base URL (see package gateway).
func SiteURL(site *store.Site) string { return gateway.URL(site) }

var heroExts = []string{".avif", ".jpeg", ".jpg", ".png", ".gif", ".webp"}

// HeroImage mirrors Planet's getHeroImage.
func HeroImage(p *store.Post) string {
	if p.HeroImage != nil && *p.HeroImage != "" {
		return *p.HeroImage
	}
	if p.VideoFilename != nil && *p.VideoFilename != "" {
		return "_videoThumbnail.png"
	}
	for _, a := range p.Attachments {
		lower := strings.ToLower(a)
		for _, e := range heroExts {
			if strings.HasSuffix(lower, e) {
				return a
			}
		}
	}
	return ""
}

func escapePath(name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// PublicPost is what Planet writes to <post>/article.json and inlines into
// planet.json (PublicArticleModel). Key set matches the Mac app's output.
type PublicPost struct {
	ArticleType       int               `json:"articleType"`
	ID                string            `json:"id"`
	Link              string            `json:"link"`
	Slug              string            `json:"slug"`
	ExternalLink      string            `json:"externalLink"`
	Title             string            `json:"title"`
	Content           string            `json:"content"`
	ContentRendered   string            `json:"contentRendered"`
	Created           store.AppleTime   `json:"created"`
	Modified          *store.AppleTime  `json:"modified,omitempty"`
	HasVideo          bool              `json:"hasVideo"`
	VideoFilename     *string           `json:"videoFilename,omitempty"`
	HasAudio          bool              `json:"hasAudio"`
	AudioFilename     *string           `json:"audioFilename,omitempty"`
	Attachments       []string          `json:"attachments"`
	HeroImage         *string           `json:"heroImage,omitempty"`
	HeroImageWidth    *int              `json:"heroImageWidth,omitempty"`
	HeroImageHeight   *int              `json:"heroImageHeight,omitempty"`
	HeroImageURL      *string           `json:"heroImageURL,omitempty"`
	HeroImageFilename *string           `json:"heroImageFilename,omitempty"`
	CIDs              map[string]string `json:"cids"`
	Tags              map[string]string `json:"tags"`
	Pinned            *store.AppleTime  `json:"pinned,omitempty"`
}

func NewPublicPost(site *store.Site, p *store.Post) PublicPost {
	link := "/" + p.ID + "/"
	slug := ""
	if p.Slug != nil {
		slug = *p.Slug
		if slug != "" {
			link = "/" + slug + "/"
		}
	}
	pub := PublicPost{
		ArticleType: p.ArticleType, ID: p.ID, Link: link, Slug: slug,
		Title: p.Title, Content: p.Content, Created: p.Created, Modified: p.Modified,
		Attachments: p.Attachments, CIDs: p.CIDs, Tags: p.Tags, Pinned: p.Pinned,
	}
	if pub.Attachments == nil {
		pub.Attachments = []string{}
	}
	if pub.CIDs == nil {
		pub.CIDs = map[string]string{}
	}
	if pub.Tags == nil {
		pub.Tags = map[string]string{}
	}
	if p.ExternalLink != nil {
		pub.ExternalLink = *p.ExternalLink
	}
	if p.ContentRendered != nil {
		pub.ContentRendered = *p.ContentRendered
	}
	if p.VideoFilename != nil && *p.VideoFilename != "" {
		pub.HasVideo, pub.VideoFilename = true, p.VideoFilename
	}
	if p.AudioFilename != nil && *p.AudioFilename != "" {
		pub.HasAudio, pub.AudioFilename = true, p.AudioFilename
	}
	if hero := HeroImage(p); hero != "" {
		u := SiteURL(site) + p.Dir() + "/" + escapePath(hero)
		name := path.Base(hero)
		pub.HeroImage, pub.HeroImageURL, pub.HeroImageFilename = &u, &u, &name
		pub.HeroImageWidth, pub.HeroImageHeight = p.HeroImageWidth, p.HeroImageHeight
	}
	return pub
}

// BrowserURL is the post's absolute public URL (Planet's article.browserURL).
func BrowserURL(site *store.Site, p *store.Post) string {
	return SiteURL(site) + p.Dir() + "/"
}

// SortForIndex orders posts the way Planet's public planet.json does:
// pinned first (newest pin first), then newest created first.
func SortForIndex(posts []*store.Post) {
	sort.SliceStable(posts, func(i, j int) bool {
		pi, pj := posts[i].Pinned, posts[j].Pinned
		switch {
		case pi != nil && pj == nil:
			return true
		case pi == nil && pj != nil:
			return false
		case pi != nil && pj != nil && *pi != *pj:
			return *pi > *pj
		}
		return posts[i].Created > posts[j].Created
	})
}

// toMap converts any JSON-able value into map/slice form for the template context.
func toMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}
