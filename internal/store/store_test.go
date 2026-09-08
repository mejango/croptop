package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const fixtureID = "FF5F456D-904F-4EE6-8BB5-AD175C65319A"

func fixtureStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, "sites", fixtureID), os.DirFS("testdata/site")); err != nil {
		t.Fatal(err)
	}
	return &Store{Root: root}
}

func asMap(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRoundTripPreservesEveryKey(t *testing.T) {
	s := fixtureStore(t)
	site, err := s.Site(fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if site.Name != "CocoPay 🥥" || site.TemplateName != "Croptop" || *site.Domain != "cocopay.eth.shop" {
		t.Fatalf("bad decode: %+v", site)
	}
	if y := site.Created.Time().Year(); y != 2025 {
		t.Fatalf("apple epoch not applied: %v", site.Created.Time())
	}
	if err := s.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	before := asMap(t, "testdata/site/planet.json")
	after := asMap(t, filepath.Join(s.SiteDir(fixtureID), "planet.json"))
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("round trip changed planet.json\nbefore %v\nafter  %v", before, after)
	}

	posts, err := s.Posts(fixtureID)
	if err != nil || len(posts) != 18 {
		t.Fatalf("posts: %v %d", err, len(posts))
	}
	for _, p := range posts {
		if err := s.SavePost(fixtureID, p); err != nil {
			t.Fatal(err)
		}
		before := asMap(t, filepath.Join("testdata/site/Articles", p.ID+".json"))
		after := asMap(t, filepath.Join(s.ArticlesDir(fixtureID), p.ID+".json"))
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("round trip changed %s\nbefore %v\nafter  %v", p.ID, before, after)
		}
	}
	if posts[0].Created < posts[len(posts)-1].Created {
		t.Fatal("posts not sorted newest first")
	}
}

func TestCroptopFieldsAreOptional(t *testing.T) {
	s := fixtureStore(t)
	site, _ := s.Site(fixtureID)
	site.IPNSSequence = 9
	site.PublishedElsewhere = true
	s.SaveSite(site)
	m := asMap(t, filepath.Join(s.SiteDir(fixtureID), "planet.json"))
	if m["ipnsSequence"] != float64(9) || m["publishedElsewhere"] != true {
		t.Fatalf("croptop fields missing: %v", m)
	}
	site, _ = s.Site(fixtureID)
	site.IPNSSequence, site.PublishedElsewhere = 0, false
	s.SaveSite(site)
	m = asMap(t, filepath.Join(s.SiteDir(fixtureID), "planet.json"))
	if _, ok := m["ipnsSequence"]; ok {
		t.Fatal("zero sequence should not be written")
	}
	if _, ok := m["publishedElsewhere"]; ok {
		t.Fatal("false publishedElsewhere should not be written")
	}
}

func TestPublicKeySet(t *testing.T) {
	s := fixtureStore(t)
	site, _ := s.Site(fixtureID)
	pub := site.Public()
	for _, k := range []string{"id", "name", "ipns", "twitterUsername", "tags", "plausibleEnabled"} {
		if _, ok := pub[k]; !ok {
			t.Errorf("public planet.json missing %s", k)
		}
	}
	for _, k := range []string{"filebaseAPIToken", "lastPublishedCID", "templateName", "domain"} {
		if _, ok := pub[k]; ok {
			t.Errorf("public planet.json must not include %s", k)
		}
	}
}

func TestAppleTime(t *testing.T) {
	a := AppleTime(783657837.704158)
	if a.Time().Format("2006-01-02") != "2025-11-01" {
		t.Fatalf("got %v", a.Time())
	}
	if FromTime(a.Time()) < a-0.001 || FromTime(a.Time()) > a+0.001 {
		t.Fatalf("not reversible: %v vs %v", FromTime(a.Time()), a)
	}
}
