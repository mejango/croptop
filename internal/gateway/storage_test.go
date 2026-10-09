package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/store"
)

func TestStorageControlsEffectiveGatewayWithoutLosingPreference(t *testing.T) {
	ens := "studio.eth"
	site := &store.Site{IPNS: "k51studio", Domain: &ens, Raw: map[string]json.RawMessage{
		SettingKey: []byte(`"crop.top"`), NameKey: []byte(`"studio"`),
	}}
	for _, mode := range []string{store.StorageP2P, store.StorageHosted, store.StorageP2P, store.StorageHosted} {
		if err := site.SetStorage(mode); err != nil {
			t.Fatal(err)
		}
		hosted := mode == store.StorageHosted
		want, wantCID := "https://studio.eth.sucks/", "https://bafyVersion.eth.sucks/"
		if hosted {
			want, wantCID = "https://studio.crop.top/", "https://bafyVersion.crop.top/"
		}
		if URL(site) != want || CIDURL(site, "bafyVersion") != wantCID {
			t.Fatalf("%s: canonical=%s CID=%s", mode, URL(site), CIDURL(site, "bafyVersion"))
		}
		for _, urls := range [][]string{URLs(site), FetchURLsForSite(site, "bafyVersion")} {
			containsHost := false
			for _, u := range urls {
				containsHost = containsHost || strings.Contains(u, "crop.top")
			}
			if containsHost != hosted {
				t.Fatalf("%s: host eligibility mismatch: %v", mode, urls)
			}
		}
		if string(site.Raw[SettingKey]) != `"crop.top"` || claimedName(site) != "studio" {
			t.Fatal("storage change discarded gateway or claimed-name preference")
		}
	}
}
