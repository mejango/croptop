package store

import (
	"encoding/json"
	"testing"
)

func TestStorageRequiresExplicitOptIn(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"croptopHost":"https://crop.top","croptopGateway":"crop.top","croptopName":"old"}`,
		`{"croptopHost":"https://custom.example"}`, `{"croptopStorage":null}`,
		`{"croptopStorage":true}`, `{"croptopStorage":""}`, `{"croptopStorage":"unknown"}`,
		`{"croptopStorage":"p2p"}`, `{"croptopStorage":"HOSTED"}`,
	} {
		var site Site
		if err := json.Unmarshal([]byte(raw), &site); err != nil {
			t.Fatal(err)
		}
		if site.HostingEnabled() || site.StorageMode() != StorageP2P {
			t.Fatalf("implicit hosting for %s", raw)
		}
	}
}

func TestStoragePersistsAndRejectsInvalidChanges(t *testing.T) {
	site := &Site{ID: "storage"}
	for _, mode := range []string{StorageHosted, StorageP2P} {
		if err := site.SetStorage(mode); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(site)
		if err != nil {
			t.Fatal(err)
		}
		var loaded Site
		if err := json.Unmarshal(data, &loaded); err != nil {
			t.Fatal(err)
		}
		if loaded.StorageMode() != mode || loaded.HostingEnabled() != (mode == StorageHosted) {
			t.Fatalf("storage choice did not round trip: %s", data)
		}
		if err := loaded.SetStorage("invalid"); err == nil || loaded.StorageMode() != mode {
			t.Fatal("invalid change accepted or changed saved choice")
		}
	}
}
