package store

import "encoding/json"

// Site mirrors Planet's MyPlanetModel (My/<uuid>/planet.json). Only the
// fields this program reads are typed; everything else rides along in Raw.
type Site struct {
	ID           string
	Name         string
	About        string
	IPNS         string
	TemplateName string
	Created      AppleTime
	Updated      AppleTime

	Domain           *string
	LastPublished    *AppleTime
	LastPublishedCID *string
	Archived         *bool
	Tags             map[string]string

	// Croptop additions, ignored by Planet.
	IPNSSequence       uint64
	PublishedElsewhere bool

	Raw doc
}

type siteJSON struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	About              string            `json:"about"`
	IPNS               string            `json:"ipns"`
	TemplateName       string            `json:"templateName"`
	Created            AppleTime         `json:"created"`
	Updated            AppleTime         `json:"updated"`
	Domain             *string           `json:"domain"`
	LastPublished      *AppleTime        `json:"lastPublished"`
	LastPublishedCID   *string           `json:"lastPublishedCID"`
	Archived           *bool             `json:"archived"`
	Tags               map[string]string `json:"tags"`
	IPNSSequence       uint64            `json:"ipnsSequence"`
	PublishedElsewhere bool              `json:"publishedElsewhere"`
}

func (s *Site) UnmarshalJSON(b []byte) error {
	var j siteJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	var raw doc
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*s = Site{
		ID: j.ID, Name: j.Name, About: j.About, IPNS: j.IPNS, TemplateName: j.TemplateName,
		Created: j.Created, Updated: j.Updated, Domain: j.Domain, LastPublished: j.LastPublished,
		LastPublishedCID: j.LastPublishedCID, Archived: j.Archived, Tags: j.Tags,
		IPNSSequence: j.IPNSSequence, PublishedElsewhere: j.PublishedElsewhere, Raw: raw,
	}
	return nil
}

func (s Site) MarshalJSON() ([]byte, error) {
	d := s.Raw.clone()
	if d == nil {
		d = doc{}
	}
	d.put("id", s.ID)
	d.put("name", s.Name)
	d.put("about", s.About)
	d.put("ipns", s.IPNS)
	d.put("templateName", s.TemplateName)
	d.put("created", s.Created)
	d.put("updated", s.Updated)
	d.putIfNotNil("domain", s.Domain)
	d.putIfNotNil("lastPublished", s.LastPublished)
	d.putIfNotNil("lastPublishedCID", s.LastPublishedCID)
	d.putIfNotNil("archived", s.Archived)
	d.putIfNotNil("tags", s.Tags)
	if s.IPNSSequence > 0 {
		d.put("ipnsSequence", s.IPNSSequence)
	} else {
		delete(d, "ipnsSequence")
	}
	if s.PublishedElsewhere {
		d.put("publishedElsewhere", true)
	} else {
		delete(d, "publishedElsewhere")
	}
	return json.Marshal(d)
}

func (s *Site) IsArchived() bool { return s.Archived != nil && *s.Archived }

// Public returns the key set Planet writes to Public/<uuid>/planet.json
// (PublicPlanetModel), without the articles array.
func (s Site) Public() map[string]json.RawMessage {
	full, _ := json.Marshal(s)
	var m doc
	json.Unmarshal(full, &m)
	out := doc{}
	for _, k := range publicSiteKeys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

var publicSiteKeys = []string{
	"id", "name", "about", "ipns", "created", "updated",
	"plausibleEnabled", "plausibleDomain", "plausibleAPIServer",
	"juiceboxEnabled", "juiceboxProjectID", "juiceboxProjectIDGoerli",
	"acceptsDonation", "acceptsDonationMessage", "acceptsDonationETHAddress",
	"twitterUsername", "githubUsername", "telegramUsername", "mastodonUsername", "discordLink",
	"farcasterEnabled",
	"podcastCategories", "podcastLanguage", "podcastExplicit",
	"tags",
}
