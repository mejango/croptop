package publish

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/store"
)

func TestHostDefaultsAndCustomHost(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"new or imported", `{}`, DefaultHost},
		{"saved empty", `{"croptopHost":""}`, DefaultHost},
		{"whitespace", `{"croptopHost":"  "}`, DefaultHost},
		{"null", `{"croptopHost":null}`, DefaultHost},
		{"custom", `{"croptopHost":" https://host.example/ "}`, "https://host.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var site store.Site
			if err := json.Unmarshal([]byte(tc.raw), &site); err != nil {
				t.Fatal(err)
			}
			if got := HostOf(&site); got != tc.want {
				t.Fatalf("HostOf = %q, want %q", got, tc.want)
			}
		})
	}
	var site store.Site
	SetHost(&site, "https://host.example/")
	if got := HostOf(&site); got != "https://host.example" {
		t.Fatalf("custom host = %q", got)
	}
	SetHost(&site, "")
	if got := HostOf(&site); got != DefaultHost {
		t.Fatalf("clearing host = %q, want %q", got, DefaultHost)
	}
}

type hostTransport func(*http.Request) (*http.Response, error)

func (f hostTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPublishPushesDefaultAndCustomHost(t *testing.T) {
	empty, custom := "", "https://host.example/"
	for _, tc := range []struct {
		name string
		host *string
		want string
	}{
		{name: "missing host", want: DefaultHost},
		{name: "saved empty host", host: &empty, want: DefaultHost},
		{name: "custom host", host: &custom, want: "https://host.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, s, _ := fakePublisher(t)
			p.SkipPrewarm, p.Wait = false, true
			site, err := s.Site(fixtureID)
			if err != nil {
				t.Fatal(err)
			}
			delete(site.Raw, HostKey)
			if tc.host != nil {
				SetHost(site, *tc.host)
			}
			cid := "bafyOLD"
			site.LastPublishedCID, site.IPNSSequence = &cid, 2
			if err := s.SaveSite(site); err != nil {
				t.Fatal(err)
			}
			const privateMarker = "private-source-file-must-stay-local"
			if err := os.WriteFile(filepath.Join(s.SiteDir(fixtureID), "private-note.txt"), []byte(privateMarker), 0o600); err != nil {
				t.Fatal(err)
			}
			transport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = transport })
			pushes := 0
			http.DefaultTransport = hostTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPost && r.URL.Path == "/v0/host/push" {
					pushes++
					if got := r.URL.Scheme + "://" + r.URL.Host; got != tc.want {
						t.Errorf("push host = %q, want %q", got, tc.want)
					}
					if r.Header.Get("X-Croptop-Cid") != "bafyFAKE" || r.Header.Get("X-Croptop-Seq") != "8" || r.Header.Get("X-Croptop-Sig") == "" {
						t.Error("push did not carry the newly published version and signature")
					}
					defer r.Body.Close()
					body, err := io.ReadAll(r.Body)
					if err != nil {
						return nil, err
					}
					if !strings.Contains(string(body), `name="file:index.html"`) {
						t.Error("push did not include the rendered site")
					}
					if strings.Contains(string(body), privateMarker) {
						t.Error("push included a private source file")
					}
				}
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
			})
			if _, err := p.Publish(context.Background(), fixtureID, false); err != nil {
				t.Fatal(err)
			}
			if pushes == 0 {
				t.Fatal("publishing did not push to its host")
			}
		})
	}
}
