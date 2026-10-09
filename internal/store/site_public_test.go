package store

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPublicHostEndpointOmitsCredentialsAndPreservesPrivateSetting(t *testing.T) {
	for _, test := range []struct {
		name, endpoint, want string
	}{
		{"default host", "https://crop.top", "https://crop.top"},
		{"custom origin", "https://custom.example", "https://custom.example"},
		{"custom port and prefix", "https://custom.example:8443/croptop/v1/", "https://custom.example:8443/croptop/v1"},
		{"loopback test host", "http://127.0.0.1:8090", "http://127.0.0.1:8090"},
		{"ipv6 origin", "http://[::1]:8090/", "http://[::1]:8090"},
		{"legacy whitespace", " https://custom.example/ ", "https://custom.example"},
		{"http custom host", "http://custom.example/prefix", "http://custom.example/prefix"},
		{"userinfo", "https://private-user:private-password@custom.example", ""},
		{"username only", "https://private-token@custom.example/prefix", ""},
		{"encoded userinfo", "https://private%2Duser:private%40password@custom.example", ""},
		{"query credential", "https://custom.example?token=private-token", ""},
		{"query on prefix", "https://custom.example/prefix?key=private-key", ""},
		{"empty query", "https://custom.example?", ""},
		{"fragment credential", "https://custom.example#private-token", ""},
		{"empty fragment", "https://custom.example#", ""},
		{"relative URL", "custom.example", ""},
		{"missing host", "https:///prefix", ""},
		{"missing hostname", "https://:443", ""},
		{"unsupported scheme", "ftp://custom.example", ""},
		{"invalid port", "https://custom.example:invalid", ""},
		{"empty endpoint", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var site Site
			input, _ := json.Marshal(map[string]string{"croptopHost": test.endpoint, StorageKey: StorageHosted})
			if err := json.Unmarshal(input, &site); err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(site)
			if err != nil {
				t.Fatal(err)
			}
			public := site.Public()
			raw, exists := public["croptopHost"]
			if test.want == "" {
				if exists {
					t.Fatalf("private or invalid endpoint became public: %s", raw)
				}
			} else {
				var got string
				if json.Unmarshal(raw, &got) != nil || got != test.want {
					t.Fatalf("public endpoint %s, want %q", raw, test.want)
				}
			}
			published, _ := json.Marshal(public)
			if bytes.Contains(published, []byte("private-")) || bytes.Contains(published, []byte("private%")) {
				t.Fatalf("credential bytes leaked in public metadata: %s", published)
			}
			after, err := json.Marshal(site)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("public projection changed the saved private site")
			}
			var reloaded Site
			if err := json.Unmarshal(after, &reloaded); err != nil {
				t.Fatal(err)
			}
			var saved string
			if json.Unmarshal(reloaded.Raw["croptopHost"], &saved) != nil || saved != test.endpoint {
				t.Fatal("private endpoint did not survive save/load unchanged")
			}
		})
	}
}

func TestPublicHostEndpointRequiresExplicitHostedStorage(t *testing.T) {
	for _, mode := range []string{"", StorageP2P, "invalid"} {
		for _, endpoint := range []string{"https://crop.top", "https://custom.example/prefix", "https://private:secret@custom.example?token=secret#secret"} {
			var site Site
			input, _ := json.Marshal(map[string]string{"croptopHost": endpoint, StorageKey: mode})
			if err := json.Unmarshal(input, &site); err != nil {
				t.Fatal(err)
			}
			if raw, exists := site.Public()["croptopHost"]; exists {
				t.Fatalf("p2p/default mode %q exposed remembered endpoint %s", mode, raw)
			}
		}
	}
}

func TestPublicHostEndpointRejectsNonStringSettings(t *testing.T) {
	for _, raw := range []string{"null", "42", "true", `{"token":"private-secret"}`, `["https://crop.top"]`} {
		var site Site
		if err := json.Unmarshal([]byte(`{"croptopStorage":"hosted","croptopHost":`+raw+`}`), &site); err != nil {
			t.Fatal(err)
		}
		if value, exists := site.Public()["croptopHost"]; exists {
			t.Fatalf("non-string private value became public: %s", value)
		}
	}
}
