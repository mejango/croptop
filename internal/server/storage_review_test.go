package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/store"
)

func TestSiteResponseProjectsStorageReviewWithoutSavingIt(t *testing.T) {
	var site store.Site
	if err := json.Unmarshal([]byte(`{"id":"legacy","lastPublishedCID":"bafyexample"}`), &site); err != nil {
		t.Fatal(err)
	}
	response := siteResponse(&site)
	if string(response["croptopStorageNeedsReview"]) != "true" || string(response[store.StorageKey]) != `"p2p"` {
		t.Fatalf("legacy storage projection: %v", response)
	}
	if len(site.Raw["croptopStorageNeedsReview"]) != 0 || len(site.Raw[store.StorageKey]) != 0 {
		t.Fatal("site response mutated the saved choice")
	}
	for _, choice := range []string{store.StorageP2P, store.StorageHosted} {
		if err := site.SetStorage(choice); err != nil {
			t.Fatal(err)
		}
		if got := string(siteResponse(&site)["croptopStorageNeedsReview"]); got != "false" {
			t.Fatalf("explicit %s still requires review: %s", choice, got)
		}
	}
}

func TestViewingAndRenamingLegacySiteDoesNotChooseStorage(t *testing.T) {
	s, ts := testServer(t)
	body, ctype := multipartBody(t, map[string]string{"name": "Legacy site"}, nil)
	code, created := do(t, "POST", ts.URL+"/v0/planets/my", body, ctype)
	if code != 200 {
		t.Fatalf("create fixture: %d %v", code, created)
	}
	id := created["id"].(string)
	site, err := s.Store.Site(id)
	if err != nil {
		t.Fatal(err)
	}
	delete(site.Raw, store.StorageKey)
	now := store.Now()
	site.LastPublished = &now
	if err := s.Store.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "PUT"} {
		endpoint := ts.URL + "/v0/planets/my/" + id
		if method == "PUT" {
			endpoint = ts.URL + "/v0/croptop/sites/" + id
		}
		code, response := do(t, method, endpoint, strings.NewReader(`{"name":"Renamed"}`), "application/json")
		if code != 200 || response["croptopStorageNeedsReview"] != true {
			t.Fatalf("%s silently acknowledged storage: %d %v", method, code, response)
		}
		saved, err := s.Store.Site(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(saved.Raw[store.StorageKey]) != 0 || len(saved.Raw["croptopStorageNeedsReview"]) != 0 || saved.HostingEnabled() {
			t.Fatalf("%s changed private storage consent", method)
		}
	}
}
