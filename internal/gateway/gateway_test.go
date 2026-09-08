package gateway

import (
	"testing"

	"github.com/mejango/croptop/internal/store"
)

func TestURL(t *testing.T) {
	site := &store.Site{IPNS: "k51abc"}
	if got := URL(site); got != "https://k51abc.eth.sucks/" {
		t.Fatalf("default: %s", got)
	}
	d := "follo.eth"
	site.Domain = &d
	Set(site, "limo")
	if got := URL(site); got != "https://follo.eth.limo/" {
		t.Fatalf("limo: %s", got)
	}
	Set(site, "nonsense")
	if got := URL(site); got != "https://follo.eth.sucks/" {
		t.Fatalf("unknown falls back to default: %s", got)
	}
	other := "follo.eth.sucks" // Planet treats non-.eth domains as plain names
	site.Domain = &other
	if got := URL(site); got != "https://k51abc.eth.sucks/" {
		t.Fatalf("non-eth domain: %s", got)
	}
	site.Domain = &d
	Set(site, "shop")
	urls := URLs(site)
	if len(urls) != len(Table) || urls[0] != "https://follo.eth.shop/" {
		t.Fatalf("urls: %v", urls)
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
