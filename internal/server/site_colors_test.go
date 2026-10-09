package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSiteColorsPersistRenderAndReset(t *testing.T) {
	s, ts := testServer(t)
	ids := []string{}
	for _, name := range []string{"Custom colors", "Automatic colors"} {
		body, ctype := multipartBody(t, map[string]string{"name": name}, nil)
		code, site := do(t, "POST", ts.URL+"/v0/planets/my", body, ctype)
		if code != 200 {
			t.Fatalf("create: %d %v", code, site)
		}
		ids = append(ids, site["id"].(string))
	}
	id := ids[0]
	body, ctype := multipartBody(t, map[string]string{"title": "A post", "content": "Color preview"}, nil)
	code, post := do(t, "POST", ts.URL+"/v0/planets/my/"+id+"/articles", body, ctype)
	if code != 200 {
		t.Fatalf("post: %d %v", code, post)
	}
	endpoint := ts.URL + "/v0/croptop/sites/" + id + "/settings"
	code, settings := do(t, "GET", endpoint, nil, "")
	if code != 200 || settings["backgroundColor"] != "" || settings["foregroundColor"] != "" {
		t.Fatalf("defaults: %d %v", code, settings)
	}
	settings["backgroundColor"] = "#FFF8ED"
	settings["foregroundColor"] = "#24211D"
	for _, reset := range []bool{false, true} {
		if reset {
			settings["backgroundColor"] = ""
			settings["foregroundColor"] = ""
		}
		payload, _ := json.Marshal(settings)
		code, saved := do(t, "PUT", endpoint, bytes.NewReader(payload), "application/json")
		if code != 200 {
			t.Fatalf("save: %d %v", code, saved)
		}
		code, readback := do(t, "GET", endpoint, nil, "")
		if code != 200 {
			t.Fatalf("read: %d", code)
		}
		stored, err := s.Store.TemplateSettings(id)
		if err != nil {
			t.Fatal(err)
		}
		var published map[string]any
		data, err := os.ReadFile(filepath.Join(s.Store.PublicDir(id), "templateSettings.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &published); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"backgroundColor", "foregroundColor", "highlightColor"} {
			if stored[key] != settings[key] || readback[key] != settings[key] || published[key] != settings[key] {
				t.Fatalf("%s did not round trip: stored=%v read=%v published=%v", key, stored[key], readback[key], published[key])
			}
		}
		postID := post["id"].(string)
		for _, name := range []string{"index.html", filepath.Join(postID, "index.html"), filepath.Join(postID, "simple.html")} {
			data, err := os.ReadFile(filepath.Join(s.Store.PublicDir(id), name))
			if err != nil {
				t.Fatal(err)
			}
			html := string(data)
			for _, color := range []string{"background", "foreground"} {
				expected := `name="croptop-` + color + `-color" content="` + settings[color+"Color"].(string) + `"`
				if !strings.Contains(html, expected) {
					t.Fatalf("%s missing %s", name, expected)
				}
			}
			if !strings.Contains(html, "assets/scripts/theme.js") {
				t.Fatalf("%s missing theme asset", name)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(s.Store.PublicDir(id), "assets", "scripts", "theme.js")); err != nil {
		t.Fatal(err)
	}
	code, other := do(t, "GET", ts.URL+"/v0/croptop/sites/"+ids[1]+"/settings", nil, "")
	if code != 200 || other["backgroundColor"] != "" || other["foregroundColor"] != "" {
		t.Fatalf("colors leaked to other site: %d %v", code, other)
	}
}
