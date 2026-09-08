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
