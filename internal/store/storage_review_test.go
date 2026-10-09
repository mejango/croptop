package store

import (
	"encoding/json"
	"testing"
)

func TestStorageReviewIsOnlyForUnchosenPublishedSites(t *testing.T) {
	for _, published := range []string{`"lastPublished":0`, `"lastPublishedCID":"bafyexample"`, `"ipnsSequence":1`} {
		for _, choice := range []string{"", `,"croptopStorage":null`, `,"croptopStorage":true`, `,"croptopStorage":"unknown"`, `,"croptopStorage":"p2p"`, `,"croptopStorage":"hosted"`} {
			var site Site
			if err := json.Unmarshal([]byte("{"+published+choice+"}"), &site); err != nil {
				t.Fatal(err)
			}
			want := choice != `,"croptopStorage":"p2p"` && choice != `,"croptopStorage":"hosted"`
			if site.StorageNeedsReview() != want {
				t.Errorf("%s%s: needs review = %v, want %v", published, choice, site.StorageNeedsReview(), want)
			}
		}
	}
	for _, raw := range []string{`{}`, `{"lastPublished":null,"lastPublishedCID":"","ipnsSequence":0}`, `{"croptopHost":"https://crop.top"}`} {
		var site Site
		if err := json.Unmarshal([]byte(raw), &site); err != nil {
			t.Fatal(err)
		}
		if site.StorageNeedsReview() {
			t.Fatalf("unpublished site should not show an upgrade notice: %s", raw)
		}
	}
}

func TestStorageReviewDoesNotPersistOrPublishPrivateProjection(t *testing.T) {
	var site Site
	if err := json.Unmarshal([]byte(`{"id":"legacy","lastPublished":1,"croptopHost":"https://crop.top"}`), &site); err != nil {
		t.Fatal(err)
	}
	if !site.StorageNeedsReview() || site.HostingEnabled() {
		t.Fatal("review must not grant hosting consent")
	}
	data, err := json.Marshal(site)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["croptopStorageNeedsReview"]; ok {
		t.Fatal("review projection was saved")
	}
	if _, ok := raw[StorageKey]; ok {
		t.Fatal("review silently saved a storage choice")
	}
	// Even an injected private projection is excluded from public metadata.
	site.Raw["croptopStorageNeedsReview"] = json.RawMessage("true")
	if _, ok := site.Public()["croptopStorageNeedsReview"]; ok {
		t.Fatal("review projection leaked into public metadata")
	}
}
