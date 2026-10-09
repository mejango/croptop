package mobile

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestComposerOriginNeverServesPublishedContent(t *testing.T) {
	assets := fstest.MapFS{"index.html": {Data: []byte("trusted composer")}, "protocol.test.mjs": {Data: []byte("not an asset")}}
	for _, enabled := range []bool{true, false} {
		calls := 0
		h := Gateway(&Server{Origin: "https://app.crop.test", Enabled: enabled}, assets, nil, "crop.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte("author script")) }))
		for _, path := range []string{"/ipfs/bafy/index.html", "/someone/", "/protocol.test.mjs", "/v0/host/keys/example", "/unknown"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "https://app.crop.test"+path, nil))
			want := http.StatusNotFound
			if !enabled {
				want = http.StatusServiceUnavailable
			}
			if w.Code != want || calls != 0 {
				t.Fatalf("%s enabled=%v: status %d fallback %d", path, enabled, w.Code, calls)
			}
			if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Fatal("missing trusted origin policy")
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "https://app.crop.test/", nil))
		if enabled && (w.Code != 200 || w.Body.String() != "trusted composer") {
			t.Fatalf("composer: %d %s", w.Code, w.Body.String())
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "https://crop.test/site/", nil))
		if calls != 1 || w.Body.String() != "author script" {
			t.Fatal("ordinary host routing changed")
		}
	}
}

func TestComposerOriginMigrationKeepsLegacyOriginReserved(t *testing.T) {
	calls := 0
	h := Gateway(&Server{Origin: "https://composer.example", Enabled: true}, fstest.MapFS{"index.html": {Data: []byte("trusted")}}, nil, "crop.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	for _, host := range []string{"app.crop.test", "app.crop.test:444", "composer.example:444"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "https://"+host+"/", nil))
		if w.Code != 503 || calls != 0 {
			t.Fatalf("%s released key origin: status=%d fallback=%d", host, w.Code, calls)
		}
	}
	for _, origin := range []string{"https://crop.test", "https://custom.crop.test", "https://nested.app.crop.test"} {
		if ValidateComposerOrigin(origin, "crop.test") == nil {
			t.Fatalf("unsafe wildcard composer accepted: %s", origin)
		}
	}
	for _, origin := range []string{"https://app.crop.test", "https://composer.example", "http://127.0.0.1:8080"} {
		if err := ValidateComposerOrigin(origin, "crop.test"); err != nil {
			t.Fatal(err)
		}
	}
}
