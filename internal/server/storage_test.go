package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/follow"
	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

func TestStorageSettingsDefaultOptInAndOptOut(t *testing.T) {
	s, ts := testServer(t)
	body, ctype := multipartBody(t, map[string]string{"name": "Storage site"}, nil)
	code, created := do(t, "POST", ts.URL+"/v0/planets/my", body, ctype)
	if code != 200 || created[store.StorageKey] != store.StorageP2P || strings.Contains(created["croptopURL"].(string), "crop.top") {
		t.Fatalf("new site default: %d %v", code, created)
	}
	id := created["id"].(string)
	site, err := s.Store.Site(id)
	if err != nil {
		t.Fatal(err)
	}
	// A pre-existing host, gateway and name are preferences, not consent.
	delete(site.Raw, store.StorageKey)
	publish.SetHost(site, "https://host.example")
	gateway.Set(site, "crop.top")
	site.Raw[publish.NameKey] = []byte(`"storage"`)
	if err := s.Store.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	readURL := ts.URL + "/v0/planets/my/" + id
	endpoint := ts.URL + "/v0/croptop/sites/" + id
	code, legacy := do(t, "GET", readURL, nil, "")
	if code != 200 || legacy[store.StorageKey] != store.StorageP2P || strings.Contains(legacy["croptopURL"].(string), "crop.top") {
		t.Fatalf("legacy site default: %d %v", code, legacy)
	}
	for _, mode := range []string{store.StorageHosted, store.StorageP2P, store.StorageHosted} {
		code, saved := do(t, "PUT", endpoint, strings.NewReader(`{"storage":"`+mode+`"}`), "application/json")
		if code != 200 || saved[store.StorageKey] != mode {
			t.Fatalf("save %s: %d %v", mode, code, saved)
		}
		code, read := do(t, "GET", readURL, nil, "")
		if code != 200 || read[store.StorageKey] != mode || strings.Contains(read["croptopURL"].(string), "crop.top") != (mode == store.StorageHosted) {
			t.Fatalf("read %s: %d %v", mode, code, read)
		}
		code, unchanged := do(t, "PUT", endpoint, strings.NewReader(`{"about":"Still sharing"}`), "application/json")
		if code != 200 || unchanged[store.StorageKey] != mode || unchanged["croptopHost"] != "https://host.example" || unchanged["croptopGateway"] != "crop.top" || unchanged["croptopName"] != "storage" {
			t.Fatalf("partial update discarded preferences: %d %v", code, unchanged)
		}
		data, err := os.ReadFile(filepath.Join(s.Store.PublicDir(id), "planet.json"))
		if err != nil {
			t.Fatal(err)
		}
		var public map[string]any
		if err := json.Unmarshal(data, &public); err != nil || public[store.StorageKey] != mode {
			t.Fatalf("published policy: %s %v", data, err)
		}
	}
	for _, payload := range []string{`{"storage":"unknown","name":"Changed"}`, `{"storage":true}`, `{"storage":12}`} {
		code, rejected := do(t, "PUT", endpoint, strings.NewReader(payload), "application/json")
		if code != 400 {
			t.Fatalf("accepted invalid storage %s: %d %v", payload, code, rejected)
		}
		site, err := s.Store.Site(id)
		if err != nil || !site.HostingEnabled() || site.Name != "Storage site" {
			t.Fatal("invalid update changed saved site")
		}
	}
}

func TestFollowingLinksRespectPublishedStorageChoice(t *testing.T) {
	s, handler := feedTestServer(t)
	entry := follow.Entry{IPNS: "k51followed", Name: "followed.eth"}
	saveFeedFollowing(t, s, entry, "Followed", render.PublicPost{ID: "post", Title: "Read this"})
	for _, mode := range []string{store.StorageP2P, store.StorageHosted} {
		data, err := json.Marshal(map[string]any{
			"ipns": "k51untrusted", "name": "Followed", "croptopGateway": "crop.top", store.StorageKey: mode,
			"articles": []render.PublicPost{{ID: "post", Title: "Read this"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.Follow.SiteDir(entry.IPNS), "planet.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		items := getFeed(t, handler, "")
		want := "https://followed.eth.sucks/post/"
		if mode == store.StorageHosted {
			want = "https://followed.crop.top/post/"
		}
		if len(items) != 1 || items[0].URL != want || items[0].IPNS != entry.IPNS {
			t.Fatalf("%s followed link: %+v", mode, items)
		}
	}
}
