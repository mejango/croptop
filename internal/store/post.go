package store

import (
	"encoding/json"
	"path"
	"strings"
)

// Post mirrors Planet's MyArticleModel (My/<uuid>/Articles/<uuid>.json).
type Post struct {
	ID          string
	Title       string
	Content     string
	Created     AppleTime
	ArticleType int // 0 blog, 1 page
	Link        string

	Attachments     []string
	CIDs            map[string]string
	Tags            map[string]string
	Slug            *string
	Summary         *string
	ContentRendered *string
	HeroImage       *string
	HeroImageWidth  *int
	HeroImageHeight *int
	ExternalLink    *string
	VideoFilename   *string
	AudioFilename   *string
	Modified        *AppleTime
	Pinned          *AppleTime

	IsIncludedInNavigation *bool
	NavigationWeight       *int

	Raw doc
}

type postJSON struct {
	ID              string            `json:"id"`
	Title           string            `json:"title"`
	Content         string            `json:"content"`
	Created         AppleTime         `json:"created"`
	ArticleType     int               `json:"articleType"`
	Link            string            `json:"link"`
	Attachments     []string          `json:"attachments"`
	CIDs            map[string]string `json:"cids"`
	Tags            map[string]string `json:"tags"`
	Slug            *string           `json:"slug"`
	Summary         *string           `json:"summary"`
	ContentRendered *string           `json:"contentRendered"`
	HeroImage       *string           `json:"heroImage"`
	HeroImageWidth  *int              `json:"heroImageWidth"`
	HeroImageHeight *int              `json:"heroImageHeight"`
	ExternalLink    *string           `json:"externalLink"`
	VideoFilename   *string           `json:"videoFilename"`
	AudioFilename   *string           `json:"audioFilename"`
	Modified        *AppleTime        `json:"modified"`
	Pinned          *AppleTime        `json:"pinned"`

	IsIncludedInNavigation *bool `json:"isIncludedInNavigation"`
	NavigationWeight       *int  `json:"navigationWeight"`
}

func (p *Post) UnmarshalJSON(b []byte) error {
	var j postJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	var raw doc
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*p = Post{
		ID: j.ID, Title: j.Title, Content: j.Content, Created: j.Created, ArticleType: j.ArticleType,
		Link: j.Link, Attachments: j.Attachments, CIDs: j.CIDs, Tags: j.Tags, Slug: j.Slug,
		Summary: j.Summary, ContentRendered: j.ContentRendered, HeroImage: j.HeroImage,
		HeroImageWidth: j.HeroImageWidth, HeroImageHeight: j.HeroImageHeight, ExternalLink: j.ExternalLink,
		VideoFilename: j.VideoFilename, AudioFilename: j.AudioFilename, Modified: j.Modified,
		Pinned: j.Pinned, IsIncludedInNavigation: j.IsIncludedInNavigation,
		NavigationWeight: j.NavigationWeight, Raw: raw,
	}
	return nil
}

func (p Post) MarshalJSON() ([]byte, error) {
	d := p.Raw.clone()
	if d == nil {
		d = doc{}
	}
	d.put("id", p.ID)
	d.put("title", p.Title)
	d.put("content", p.Content)
	d.put("created", p.Created)
	d.put("articleType", p.ArticleType)
	d.put("link", p.Link)
	d.putIfNotNil("attachments", p.Attachments)
	d.putIfNotNil("cids", p.CIDs)
	d.putIfNotNil("tags", p.Tags)
	d.putIfNotNil("slug", p.Slug)
	d.putIfNotNil("summary", p.Summary)
	d.putIfNotNil("contentRendered", p.ContentRendered)
	d.putIfNotNil("heroImage", p.HeroImage)
	d.putIfNotNil("heroImageWidth", p.HeroImageWidth)
	d.putIfNotNil("heroImageHeight", p.HeroImageHeight)
	d.putIfNotNil("externalLink", p.ExternalLink)
	d.putIfNotNil("videoFilename", p.VideoFilename)
	d.putIfNotNil("audioFilename", p.AudioFilename)
	d.putIfNotNil("modified", p.Modified)
	d.putIfNotNil("pinned", p.Pinned)
	d.putIfNotNil("isIncludedInNavigation", p.IsIncludedInNavigation)
	d.putIfNotNil("navigationWeight", p.NavigationWeight)
	return json.Marshal(d)
}

// Dir is the folder name under the public tree: the slug when set, else the id.
func (p *Post) Dir() string {
	if p.Slug != nil && *p.Slug != "" {
		return *p.Slug
	}
	return p.ID
}

func (p *Post) LastChanged() AppleTime {
	if p.Modified != nil {
		return *p.Modified
	}
	return p.Created
}

var mediaExts = []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic", ".mp4", ".mov", ".m4v", ".mp3", ".m4a", ".wav", ".aac"}

func IsMedia(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	for _, e := range mediaExts {
		if e == ext {
			return true
		}
	}
	return false
}

var imageExts = []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}

func IsImage(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	for _, e := range imageExts {
		if e == ext {
			return true
		}
	}
	return false
}

// TextOnly reports whether the post has no media attachment, which is when
// Planet draws a _cover.png for the Croptop grid.
func (p *Post) TextOnly() bool {
	for _, a := range p.Attachments {
		if IsMedia(a) {
			return false
		}
	}
	return true
}
