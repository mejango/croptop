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

	Contributors []Contributor
	Aggregation  []string

	Raw doc
}

// Contributor identifies a publishing site; it never grants access to the owner’s key.
type Contributor struct {
	IPNS string `json:"ipns"`
	Name string `json:"name"`
	Mode string `json:"mode"` // submissions or all (curated source)
}

type siteJSON struct {
	Contributors       []Contributor     `json:"contributors,omitempty"`
	Aggregation        []string          `json:"aggregation,omitempty"`
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
		IPNSSequence: j.IPNSSequence, PublishedElsewhere: j.PublishedElsewhere, Raw: raw, Contributors: j.Contributors, Aggregation: j.Aggregation,
	}
	return nil
}

func (s Site) MarshalJSON() ([]byte, error) {
	d := s.Raw.clone()
	if d == nil {
		d = doc{}
	}
	if s.Contributors != nil {
		d.put("contributors", s.Contributors)
	}
	d.putIfNotNil("aggregation", s.Aggregation)
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
	if contributors := s.ContributorSites(); len(contributors) > 0 {
		out.put("contributors", contributors)
	}
	return out
}

var publicSiteKeys = []string{
	"id", "name", "about", "ipns", "created", "updated", "contributors",
	"plausibleEnabled", "plausibleDomain", "plausibleAPIServer",
	"juiceboxEnabled", "juiceboxProjectID", "juiceboxProjectIDGoerli",
	"acceptsDonation", "acceptsDonationMessage", "acceptsDonationETHAddress",
	"twitterUsername", "githubUsername", "telegramUsername", "mastodonUsername", "discordLink",
	"farcasterEnabled",
	"podcastCategories", "podcastLanguage", "podcastExplicit",
	"tags",
}

// ContributorSites includes sources from Planet's composite-site format.
func (s Site) ContributorSites() []Contributor {
	out := append([]Contributor{}, s.Contributors...)
	for _, name := range s.Aggregation {
		found := false
		for _, c := range out {
			if c.IPNS == name {
				found = true
				break
			}
		}
		if !found {
			out = append(out, Contributor{IPNS: name, Name: name, Mode: "all"})
		}
	}
	return out
}
