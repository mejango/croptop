package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/publish"
)

func TestCustomDomainSettingsRoundTrip(t *testing.T) {
	s, ts := testServer(t)
	body, ctype := multipartBody(t, map[string]string{"name": "Domain test"}, nil)
	code, created := do(t, "POST", ts.URL+"/v0/planets/my", body, ctype)
	if code != http.StatusOK {
		t.Fatalf("create: %d %v", code, created)
	}
	id := created["id"].(string)
	body, ctype = multipartBody(t, map[string]string{"title": "Published post"}, map[string][]byte{"photo.png": pngBytes()})
	code, post := do(t, "POST", ts.URL+"/v0/planets/my/"+id+"/articles", body, ctype)
	if code != http.StatusOK {
		t.Fatalf("create post: %d %v", code, post)
	}
	postID := post["id"].(string)
	endpoint := ts.URL + "/v0/croptop/sites/" + id
	code, result := do(t, "PUT", endpoint, strings.NewReader(`{"domain":"existing.eth","gateway":"shop","host":"https://my-host.example.com","customDomain":" WWW.Example.com ","about":"Keep this introduction","custom":{"twitterUsername":"existing"}}`), "application/json")
	if code != http.StatusOK || result[gateway.CustomDomainKey] != "www.example.com" || result["domain"] != "existing.eth" {
		t.Fatalf("save: %d %v", code, result)
	}
	site, err := s.Store.Site(id)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.CustomDomain(site) != "www.example.com" || gateway.URL(site) != "https://www.example.com/" || publish.HostOf(site) != "https://my-host.example.com" {
		t.Fatalf("saved domain and publishing host: %s %s", gateway.URL(site), publish.HostOf(site))
	}
	article, err := os.ReadFile(filepath.Join(s.Store.PublicDir(id), postID, "article.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(article), "https://www.example.com/"+postID+"/photo.png") {
		t.Fatal("published article does not use custom domain for its image")
	}
	code, result = do(t, "GET", endpoint+"/url", nil, "")
	if code != http.StatusOK || result["url"] != "https://www.example.com/" {
		t.Fatalf("website button URL: %d %v", code, result)
	}
	code, result = do(t, "GET", ts.URL+"/v0/planets/my/"+id, nil, "")
	if code != http.StatusOK || result[gateway.CustomDomainKey] != "www.example.com" {
		t.Fatalf("read back: %d %v", code, result)
	}
	code, result = do(t, "PUT", endpoint, strings.NewReader(`{"name":"Renamed"}`), "application/json")
	if code != http.StatusOK || result[gateway.CustomDomainKey] != "www.example.com" {
		t.Fatalf("omitted custom domain changed setting: %d %v", code, result)
	}
	for _, invalid := range []string{"https://bad.example.com", ".example.com", "example.com.", "existing.eth", "localhost", "foo@example.com", "example.com:443"} {
		payload, _ := json.Marshal(map[string]string{"customDomain": invalid, "domain": "changed.eth", "about": "Changed"})
		code, result = do(t, "PUT", endpoint, strings.NewReader(string(payload)), "application/json")
		if code != http.StatusBadRequest {
			t.Fatalf("accepted %q: %d %v", invalid, code, result)
		}
	}
	site, err = s.Store.Site(id)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.CustomDomain(site) != "www.example.com" || *site.Domain != "existing.eth" || site.About != "Keep this introduction" {
		t.Fatal("invalid request changed saved settings")
	}
	code, result = do(t, "PUT", endpoint, strings.NewReader(`{"customDomain":" "}`), "application/json")
	if code != http.StatusOK {
		t.Fatalf("clear: %d %v", code, result)
	}
	site, err = s.Store.Site(id)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.CustomDomain(site) != "" || gateway.URL(site) != "https://existing.eth.shop/" || *site.Domain != "existing.eth" || site.Name != "Renamed" || site.About != "Keep this introduction" || string(site.Raw["twitterUsername"]) != `"existing"` || publish.HostOf(site) != "https://my-host.example.com" {
		t.Fatal("clearing custom domain did not preserve other site settings")
	}
	if _, exists := result[gateway.CustomDomainKey]; exists {
		t.Fatal("cleared custom domain still appears in API JSON")
	}
}
