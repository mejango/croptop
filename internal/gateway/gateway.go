// Package gateway is the table of public gateways that serve ENS and IPNS
// names, and the rule for a site's canonical public URL.
package gateway

import (
	"encoding/json"
	"strings"

	"github.com/mejango/croptop/internal/store"
)

type Gateway struct {
	Key  string // stored in the site as croptopGateway
	Name string // shown in the console
	TLD  string // appended to an .eth name: yoursite.eth -> yoursite.eth.<TLD>
}

// Table order is the fallback order. Add a gateway here and nowhere else.
var Table = []Gateway{
	{Key: "sucks", Name: "eth.sucks", TLD: "sucks"},
	{Key: "shop", Name: "eth.shop", TLD: "shop"},
	{Key: "limo", Name: "eth.limo", TLD: "limo"},
}

const Default = "sucks"

// SettingKey is the site JSON key (a Croptop addition Planet ignores).
const SettingKey = "croptopGateway"

func Get(key string) Gateway {
	for _, g := range Table {
		if g.Key == key {
			return g
		}
	}
	return Get(Default)
}

// Of returns the gateway a site has chosen.
func Of(site *store.Site) Gateway {
	if raw, ok := site.Raw[SettingKey]; ok {
		var key string
		if json.Unmarshal(raw, &key) == nil && key != "" {
			return Get(key)
		}
	}
	return Get(Default)
}

// Set records the site's gateway choice.
func Set(site *store.Site, key string) {
	if site.Raw == nil {
		site.Raw = map[string]json.RawMessage{}
	}
	b, _ := json.Marshal(Get(key).Key)
	site.Raw[SettingKey] = b
}

// URL is the site's canonical public base URL with a trailing slash, the
// way Planet builds it: an .eth domain on the gateway, else the IPNS name.
// Other name systems keep Planet's fixed mappings.
func URL(site *store.Site) string { return urlOn(site, Of(site)) }

func urlOn(site *store.Site, g Gateway) string {
	if site.Domain != nil {
		d := strings.TrimSpace(*site.Domain)
		switch {
		case strings.HasSuffix(d, ".eth"):
			return "https://" + d + "." + g.TLD + "/"
		case strings.HasSuffix(d, ".sol"):
			return "https://" + d + ".build/"
		case strings.HasSuffix(d, ".bit"):
			return "https://" + d + ".site/"
		}
	}
	return "https://" + site.IPNS + ".eth." + g.TLD + "/"
}

// URLs lists the site's URL on every gateway, canonical first.
func URLs(site *store.Site) []string {
	chosen := Of(site)
	out := []string{urlOn(site, chosen)}
	for _, g := range Table {
		if g.Key != chosen.Key {
			out = append(out, urlOn(site, g))
		}
	}
	return out
}
