// Package gateway is the table of public gateways that serve ENS and IPNS
// names, and the rule for a site's canonical public URL.
package gateway

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mejango/croptop/internal/store"
)

type Gateway struct {
	Key   string // stored in the site as croptopGateway
	Name  string // shown in the console
	TLD   string // appended to an .eth name: yoursite.eth -> yoursite.eth.<TLD>
	Names bool   // resolves raw IPNS names and CIDs as subdomains (<k51…>.eth.<TLD>, <cid>.eth.<TLD>)
	// Domain marks a croptop host (see internal/host): yoursite.eth -> yoursite.<Domain>,
	// a claimed free name -> <Domain>/<name>/, raw names and CIDs -> <k51…>.<Domain>.
	Domain string
}

// Table order is the fallback order. Add a gateway here and nowhere else.
var Table = []Gateway{
	{Key: "sucks", Name: "eth.sucks", TLD: "sucks", Names: true},
	{Key: "shop", Name: "eth.shop", TLD: "shop", Names: true},
	{Key: "limo", Name: "eth.limo", TLD: "limo", Names: false}, // .eth domains only
	{Key: "crop.top", Name: "crop.top", Names: true, Domain: "crop.top"},
}

// NameKey is the site JSON key holding a free name claimed on a croptop host.
const NameKey = "croptopName"

func claimedName(site *store.Site) string {
	if raw, ok := site.Raw[NameKey]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

const Default = "crop.top"

// SettingKey is the site JSON key (a Croptop addition Planet ignores).
const SettingKey = "croptopGateway"

// CustomDomainKey stores a conventional DNS hostname separately from the ENS name.
const CustomDomainKey = "croptopCustomDomain"

func normalizeCustomDomain(domain string) (string, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return "", nil
	}
	invalid := fmt.Errorf("enter a DNS domain such as example.com, without a scheme, path, or port (use Punycode for international names)")
	if len(domain) > 253 {
		return "", invalid
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", invalid
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", invalid
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", invalid
			}
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 || (!strings.HasPrefix(tld, "xn--") && strings.Trim(tld, "abcdefghijklmnopqrstuvwxyz") != "") {
		return "", invalid
	}
	switch tld {
	case "eth", "sol", "bit":
		return "", fmt.Errorf("enter a conventional DNS domain here; use the name field for blockchain names")
	case "localhost", "local", "internal", "onion", "invalid":
		return "", invalid
	}
	if domain == "home.arpa" || strings.HasSuffix(domain, ".home.arpa") {
		return "", invalid
	}
	return domain, nil
}

// CustomDomain returns the valid custom hostname, ignoring malformed saved values.
func CustomDomain(site *store.Site) string {
	var domain string
	if json.Unmarshal(site.Raw[CustomDomainKey], &domain) != nil {
		return ""
	}
	domain, err := normalizeCustomDomain(domain)
	if err != nil {
		return ""
	}
	return domain
}

// SetCustomDomain validates and records a hostname. An empty value restores gateway URLs.
func SetCustomDomain(site *store.Site, domain string) error {
	domain, err := normalizeCustomDomain(domain)
	if err != nil {
		return err
	}
	if domain == "" {
		delete(site.Raw, CustomDomainKey)
		return nil
	}
	if site.Raw == nil {
		site.Raw = map[string]json.RawMessage{}
	}
	b, _ := json.Marshal(domain)
	site.Raw[CustomDomainKey] = b
	return nil
}

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

// URL is the site's canonical public base URL with a trailing slash. A custom
// DNS domain takes precedence; otherwise the site's chosen gateway is used.
func URL(site *store.Site) string {
	if domain := CustomDomain(site); domain != "" {
		return "https://" + domain + "/"
	}
	return urlOn(site, Of(site))
}

func urlOn(site *store.Site, g Gateway) string {
	if g.Domain != "" {
		if site.Domain != nil && strings.HasSuffix(strings.TrimSpace(*site.Domain), ".eth") {
			return "https://" + strings.TrimSuffix(strings.TrimSpace(*site.Domain), ".eth") + "." + g.Domain + "/"
		}
		if n := claimedName(site); n != "" {
			return "https://" + g.Domain + "/" + n + "/"
		}
		return "https://" + site.IPNS + "." + g.Domain + "/"
	}
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
	return "https://" + site.IPNS + ".eth." + namesGateway(g).TLD + "/"
}

// namesGateway returns g when it resolves raw names, else the first that does.
func namesGateway(g Gateway) Gateway {
	if g.Names {
		return g
	}
	for _, t := range Table {
		if t.Names {
			return t
		}
	}
	return g
}

// CIDURL is the address of one exact version of a site. CIDv0 has no
// subdomain form, so it goes through dweb.link like Planet does.
func CIDURL(site *store.Site, cid string) string {
	if strings.HasPrefix(cid, "Qm") {
		return "https://dweb.link/ipfs/" + cid + "/"
	}
	g := namesGateway(Of(site))
	if g.Domain != "" {
		return "https://" + cid + "." + g.Domain + "/"
	}
	return "https://" + cid + ".eth." + g.TLD + "/"
}

// nameBase is the subdomain gateway base for a raw label (CID or IPNS name).
func nameBase(g Gateway, label string) string {
	if g.Domain != "" {
		return "https://" + label + "." + g.Domain + "/"
	}
	return "https://" + label + ".eth." + g.TLD + "/"
}

// FetchURLs lists HTTP bases to read a site's current version from, most
// exact first: the CID on every name-resolving gateway, then the IPNS name.
func FetchURLs(ipns, cid string) []string {
	var out []string
	if cid != "" && !strings.HasPrefix(cid, "Qm") {
		for _, g := range Table {
			if g.Names {
				out = append(out, nameBase(g, cid))
			}
		}
		out = append(out, "https://dweb.link/ipfs/"+cid+"/")
	}
	for _, g := range Table {
		if g.Names {
			out = append(out, nameBase(g, ipns))
		}
	}
	return append(out, "https://dweb.link/ipns/"+ipns+"/")
}

// URLs lists the site's URL on every gateway, canonical first.
func URLs(site *store.Site) []string {
	chosen := Of(site)
	out := []string{urlOn(site, chosen)}
	if domain := CustomDomain(site); domain != "" && URL(site) != out[0] {
		out = append([]string{URL(site)}, out...)
	}
	for _, g := range Table {
		if g.Key != chosen.Key {
			out = append(out, urlOn(site, g))
		}
	}
	return out
}
