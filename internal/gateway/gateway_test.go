package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/store"
)

func TestURL(t *testing.T) {
	site := &store.Site{IPNS: "k51abc"}
	if got := URL(site); got != "https://k51abc.crop.top/" {
		t.Fatalf("default: %s", got)
	}
	d := "follo.eth"
	site.Domain = &d
	Set(site, "limo")
	if got := URL(site); got != "https://follo.eth.limo/" {
		t.Fatalf("limo: %s", got)
	}
	Set(site, "nonsense")
	if got := URL(site); got != "https://follo.crop.top/" {
		t.Fatalf("unknown falls back to default: %s", got)
	}
	other := "follo.eth.sucks" // Planet treats non-.eth domains as plain names
	site.Domain = &other
	if got := URL(site); got != "https://k51abc.crop.top/" {
		t.Fatalf("non-eth domain: %s", got)
	}
	site.Domain = &d
	Set(site, "shop")
	urls := URLs(site)
	if len(urls) != len(Table) || urls[0] != "https://follo.eth.shop/" {
		t.Fatalf("urls: %v", urls)
	}
}

func TestCustomDomainURLsAndClearing(t *testing.T) {
	ens := "follo.eth"
	site := &store.Site{IPNS: "k51abc", Domain: &ens}
	Set(site, "shop")
	if err := SetCustomDomain(site, " WWW.Example.com "); err != nil {
		t.Fatal(err)
	}
	if got := URL(site); got != "https://www.example.com/" {
		t.Fatalf("custom canonical URL: %s", got)
	}
	if got := URLs(site); len(got) != len(Table)+1 || got[0] != URL(site) || got[1] != "https://follo.eth.shop/" {
		t.Fatalf("custom domain and gateway alternatives: %v", got)
	}
	if got := CIDURL(site, "bafyX"); got != "https://bafyX.eth.shop/" {
		t.Fatalf("custom domain changed content-specific URL: %s", got)
	}
	if err := SetCustomDomain(site, " "); err != nil {
		t.Fatal(err)
	}
	if _, exists := site.Raw[CustomDomainKey]; exists || URL(site) != "https://follo.eth.shop/" || *site.Domain != ens {
		t.Fatal("clearing custom domain did not preserve ENS and restore gateway URL")
	}
}

func TestCustomDomainValidation(t *testing.T) {
	for _, input := range []string{"example.com", "www.example.co.uk", "xn--bcher-kva.de", "example.xn--p1ai", strings.Repeat("a", 63) + ".com"} {
		t.Run(input, func(t *testing.T) {
			site := &store.Site{}
			if err := SetCustomDomain(site, input); err != nil || CustomDomain(site) != input {
				t.Fatalf("valid domain %q: %v", input, err)
			}
		})
	}
	for _, input := range []string{
		"https://example.com", "example.com/path", "example.com:443", "me@example.com", "example.com?x=y", "example.com#x",
		"*.example.com", "example..com", "-example.com", "example-.com", "_dnslink.example.com", ".example.com", "example.com.", "example.com..", ".",
		"localhost", "app.localhost", "app.local", "app.internal", "home.arpa", "app.home.arpa", "app.onion",
		"name.eth", "name.sol", "name.bit", "127.0.0.1", "0x7f.0x1", "[::1]", "bücher.de", "ex ample.com",
		strings.Repeat("a", 64) + ".com", strings.Repeat(strings.Repeat("a", 63)+".", 4) + "com",
	} {
		t.Run(input, func(t *testing.T) {
			site := &store.Site{}
			if err := SetCustomDomain(site, "existing.example.com"); err != nil {
				t.Fatal(err)
			}
			if err := SetCustomDomain(site, input); err == nil {
				t.Fatalf("accepted invalid custom domain %q", input)
			}
			if CustomDomain(site) != "existing.example.com" {
				t.Fatal("invalid input changed the saved domain")
			}
		})
	}
}

func TestMalformedCustomDomainFallsBack(t *testing.T) {
	for _, raw := range []json.RawMessage{[]byte(`"https://example.com"`), []byte(`"app.localhost"`), []byte(`12`), []byte(`null`), []byte(`{`)} {
		site := &store.Site{IPNS: "k51abc", Raw: map[string]json.RawMessage{CustomDomainKey: raw}}
		if CustomDomain(site) != "" || URL(site) != "https://k51abc.crop.top/" {
			t.Fatalf("malformed saved value did not fall back: %s", raw)
		}
	}
}

func TestLimoHasNoNameResolution(t *testing.T) {
	site := &store.Site{IPNS: "k51abc"}
	Set(site, "limo")
	if got := URL(site); got != "https://k51abc.eth.sucks/" {
		t.Fatalf("ipns on limo must fall back: %s", got)
	}
	d := "x.eth"
	site.Domain = &d
	if got := URL(site); got != "https://x.eth.limo/" {
		t.Fatalf("eth domain on limo: %s", got)
	}
	if got := CIDURL(site, "bafyX"); got != "https://bafyX.eth.sucks/" {
		t.Fatalf("cid url: %s", got)
	}
	if got := CIDURL(site, "QmX"); got != "https://dweb.link/ipfs/QmX/" {
		t.Fatalf("cidv0 url: %s", got)
	}
	urls := FetchURLs("k51abc", "bafyX")
	if urls[0] != "https://bafyX.eth.sucks/" || urls[len(urls)-1] != "https://dweb.link/ipns/k51abc/" {
		t.Fatalf("fetch urls: %v", urls)
	}
}
