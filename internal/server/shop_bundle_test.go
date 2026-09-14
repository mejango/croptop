package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mejango/croptop/internal/shop"
	"testing"
	"time"
)

func TestBundleSessionNetworksAndRecovery(t *testing.T) {
	s, h, _ := newShopTest(t)
	base := "/v0/croptop/sites/" + shopSiteID + "/shop-setup"
	open := func(targets map[string]string, want int) {
		t.Helper()
		code, result := shopReq(t, h, "POST", base, "", map[string]any{"targets": targets, "category": "1"})
		if code != want {
			t.Fatal(code, result)
		}
	}
	open(map[string]string{"baseMainnetCollectionAddress": connectionHook, "baseSepoliaCollectionAddress": connectionHook}, 400)
	open(map[string]string{"unsupportedCollectionAddress": connectionHook}, 400)
	open(map[string]string{"baseMainnetCollectionAddress": "oops"}, 400)
	targets := map[string]string{}
	for _, n := range shop.Networks {
		if !n.Testnet {
			targets[n.Setting+"CollectionAddress"] = connectionHook
		}
	}
	open(targets, 200)
	saved, err := shop.Load(s.bundleDir(), shopSiteID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Bundle.Chains) != 4 {
		t.Fatal("did not include all entered networks")
	}
	if code, _ := shopReq(t, h, "GET", base, "wrong", nil); code != 403 {
		t.Fatal("leaked private session")
	}
	if code, _ := shopReq(t, h, "POST", base+"/wrong", saved.Token, map[string]string{"action": "check"}); code != 403 {
		t.Fatal("accepted wrong session")
	}
	// A reopened button must resume the saved bundle even if the unsaved form changed.
	saved.Bundle.Deadline = 12345
	if err = shop.Save(s.bundleDir(), saved); err != nil {
		t.Fatal(err)
	}
	open(map[string]string{"baseMainnetCollectionAddress": shopWallet}, 200)
	reopened, err := shop.Load(s.bundleDir(), shopSiteID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.ID != saved.ID || len(reopened.Bundle.Chains) != 4 {
		t.Fatal("discarded live authorizations")
	}
	endpoint := base + "/" + saved.ID
	if code, _ := shopReq(t, h, "POST", endpoint, saved.Token, map[string]string{"action": "submitted", "hash": "0x1234"}); code != 409 {
		t.Fatal("accepted payment without signing intent")
	}
	if code, _ := shopReq(t, h, "POST", endpoint, saved.Token, map[string]string{"action": "check"}); code != 200 {
		t.Fatal(code)
	}
	reopened, _ = shop.Load(s.bundleDir(), shopSiteID)
	if reopened.Bundle.Completed {
		t.Fatal("connected shops without reviewed rules")
	}
}

func serverBundleRPC(granted map[int]bool, configured map[int]bool, final map[int]bool) shop.RPC {
	return func(_ context.Context, id int, method string, params any, out any) error {
		var value any
		switch method {
		case "eth_chainId":
			value = fmt.Sprintf("0x%x", id)
		case "eth_getCode":
			value = "0x1234"
		case "eth_estimateGas":
			value = "0x186a0"
		case "eth_getBlockByNumber":
			value = map[string]string{"timestamp": fmt.Sprintf("0x%x", time.Now().Unix()+7200)}
		case "eth_call":
			p := params.([]any)
			q := p[0].(map[string]string)
			data := q["data"]
			isFinal := p[1] == "finalized"
			switch data[:10] {
			case connectSelector("PERMISSIONS()"):
				value = "0x" + connectAddress(shop.Permissions)
			case connectSelector("owner()"):
				value = "0x" + connectAddress(shopWallet)
			case connectSelector("projectId()"):
				value = "0x" + connectWord(42)
			case connectSelector("pricingContext()"):
				value = "0x" + connectWord(1) + connectWord(18)
			case connectSelector("permissionsOf(address,address,uint256)"):
				value = "0x" + connectWord(1<<25)
			case connectSelector("hasPermission(address,address,uint256,uint256,bool,bool)"):
				n := uint64(1)
				if data[10:74] == connectAddress(shop.Publisher) && (!granted[id] || isFinal && !final[id]) {
					n = 0
				}
				value = "0x" + connectWord(n)
			case connectSelector("allowanceFor(address,uint256)"):
				price := uint64(1000000000000000)
				if configured[id] && (!isFinal || final[id]) {
					price = 5000000000000000
				}
				value = "0x" + connectWord(price) + connectWord(2) + connectWord(50) + connectWord(75000000) + connectWord(160) + connectWord(1) + connectAddress(shopWallet)
			case connectSelector("isTrustedForwarder(address)"):
				value = "0x" + connectWord(1)
			case connectSelector("nonces(address)"):
				value = "0x" + connectWord(7)
			case connectSelector("setPermissionsFor(address,(address,uint64,uint8[]))"), connectSelector("configurePostingCriteriaFor((address,uint24,uint104,uint32,uint32,uint32,address[])[])"), connectSelector("executeBatch((address,address,uint256,uint256,uint48,bytes,bytes)[],address)"):
				value = "0x"
			default:
				return fmt.Errorf("unexpected call %s", data)
			}
		default:
			return fmt.Errorf("unexpected method %s", method)
		}
		raw, _ := json.Marshal(value)
		return json.Unmarshal(raw, out)
	}
}

func TestBundleFinalityBeforeConnectingSettings(t *testing.T) {
	s, h, _ := newShopTest(t)
	granted := map[int]bool{1: false, 8453: true}
	configured := map[int]bool{}
	final := map[int]bool{}
	s.shopRPC = serverBundleRPC(granted, configured, final)
	base := "/v0/croptop/sites/" + shopSiteID + "/shop-setup"
	targets := map[string]string{"ethereumMainnetCollectionAddress": connectionHook, "baseMainnetCollectionAddress": connectionHook}
	if code, res := shopReq(t, h, "POST", base, "", map[string]any{"targets": targets, "category": "1"}); code != 200 {
		t.Fatal(code, res)
	}
	saved, _ := shop.Load(s.bundleDir(), shopSiteID)
	endpoint := base + "/" + saved.ID
	action := func(body map[string]any, want int) {
		t.Helper()
		code, res := shopReq(t, h, "POST", endpoint, saved.Token, body)
		if code != want {
			t.Fatal(code, res)
		}
	}
	action(map[string]any{"action": "plan", "from": shopWallet, "price": "0.005"}, 200)
	action(map[string]any{"action": "quote"}, 409)
	action(map[string]any{"action": "check"}, 200)
	for _, id := range []int{1, 8453} {
		granted[id] = true
		configured[id] = true
		final[id] = id == 8453
	}
	action(map[string]any{"action": "check"}, 200)
	pending, _ := shop.Load(s.bundleDir(), shopSiteID)
	if pending.Bundle.Completed || !pending.Bundle.Chains[1].Ready {
		t.Fatal("incorrect partial completion")
	}
	before, _ := s.Store.TemplateSettings(shopSiteID)
	if before["ethereumMainnetCollectionAddress"] == connectionHook {
		t.Fatal("applied unfinalized setup")
	}
	final[1] = true
	action(map[string]any{"action": "check"}, 200)
	after, _ := s.Store.TemplateSettings(shopSiteID)
	if after["ethereumMainnetCollectionAddress"] != connectionHook || after["baseMainnetCollectionAddress"] != connectionHook || after["collectionCategory"] != "1" {
		t.Fatal(after)
	}
	complete, _ := shop.Load(s.bundleDir(), shopSiteID)
	if !complete.Bundle.Completed {
		t.Fatal("completion was not saved")
	}
}

func TestProjectLookupCanOpenWithoutShopAddresses(t *testing.T) {
	s, h, _ := newShopTest(t)
	base := "/v0/croptop/sites/" + shopSiteID + "/shop-setup"
	if code, res := shopReq(t, h, "POST", base, "", map[string]any{"category": "1"}); code != 200 {
		t.Fatal(code, res)
	}
	saved, e := shop.Load(s.bundleDir(), shopSiteID)
	if e != nil {
		t.Fatal(e)
	}
	if saved.Bundle.Resolved || len(saved.Bundle.Chains) != 0 {
		t.Fatal("must start with project selection")
	}
	saved.Bundle.Deadline = 12345
	if e = shop.Save(s.bundleDir(), saved); e != nil {
		t.Fatal(e)
	}
	if code, _ := shopReq(t, h, "POST", base+"/"+saved.ID, saved.Token, map[string]any{"action": "resolve", "chain": 1, "projectID": "2"}); code != 409 {
		t.Fatal("replaced a live signing session")
	}
}
