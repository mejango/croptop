package shop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func bundleRPC(granted map[int]bool, configured map[int]bool, final map[int]bool) RPC {
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
			case abiCall("PERMISSIONS()"):
				value = "0x" + abiAddress(Permissions)
			case abiCall("owner()"):
				value = "0x" + abiAddress(setupWallet)
			case abiCall("projectId()"):
				value = "0x" + abiN(42)
			case abiCall("pricingContext()"):
				value = "0x" + abiN(1) + abiN(18)
			case abiCall("permissionsOf(address,address,uint256)"):
				value = "0x" + abiN(1<<25)
			case abiCall("hasPermission(address,address,uint256,uint256,bool,bool)"):
				n := uint64(1)
				if data[10:74] == abiAddress(Publisher) && (!granted[id] || isFinal && !final[id]) {
					n = 0
				}
				value = "0x" + abiN(n)
			case abiCall("allowanceFor(address,uint256)"):
				price := uint64(1000000000000000)
				if configured[id] && (!isFinal || final[id]) {
					price = 5000000000000000
				}
				value = "0x" + abiN(price) + abiN(2) + abiN(50) + abiN(75000000) + abiN(160) + abiN(1) + abiAddress(setupWallet)
			case abiCall("isTrustedForwarder(address)"):
				value = "0x" + abiN(1)
			case abiCall("nonces(address)"):
				value = "0x" + abiN(7)
			case abiCall("setPermissionsFor(address,(address,uint64,uint8[]))"), abiCall("configurePostingCriteriaFor((address,uint24,uint104,uint32,uint32,uint32,address[])[])"), abiCall("executeBatch((address,address,uint256,uint256,uint48,bytes,bytes)[],address)"):
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
func TestBundlePlansGrantAndCriteriaAcrossChains(t *testing.T) {
	granted := map[int]bool{1: false, 10: true, 42161: true, 8453: true}
	configured := map[int]bool{}
	final := map[int]bool{}
	rpc := bundleRPC(granted, configured, final)
	b := &SetupBundle{}
	for _, id := range []int{1, 10, 42161, 8453} {
		b.Chains = append(b.Chains, &BundleChain{Connection: &Connection{ChainID: id, Hook: setupHook, Category: 1}})
	}
	if err := PlanBundle(t.Context(), rpc, b, setupWallet, "0.005"); err != nil {
		t.Fatal(err)
	}
	if len(b.Chains[0].Requests) != 2 || b.Chains[0].Requests[0].Kind != "permission" || b.Chains[0].Requests[1].Kind != "criteria" || b.Chains[0].Requests[1].Nonce != "8" {
		t.Fatal(b.Chains[0])
	}
	for _, chain := range b.Chains {
		if chain.Connection.ChainID != 1 && len(chain.Requests) != 1 {
			t.Fatal("revnet unnecessarily grants again")
		}
		want := PostingCriteria{MinimumPrice: "5000000000000000", MinimumTotalSupply: 2, MaximumTotalSupply: 50, MaximumSplitPercent: 75000000, AllowedAddresses: []string{setupWallet}}
		if !reflect.DeepEqual(*chain.Connection.Expected, want) {
			t.Fatal("changed existing rules")
		}
	}
	if _, err := BundleEntries(t.Context(), rpc, b); err == nil {
		t.Fatal("quoted unsigned calls")
	}
	for _, chain := range b.Chains {
		for i := range chain.Requests {
			chain.Requests[i].Signature = "0x" + strings.Repeat("1", 130)
		}
	}
	entries, err := BundleEntries(t.Context(), rpc, b)
	if err != nil || len(entries) != 4 {
		t.Fatal(entries, err)
	}
	for _, entry := range entries {
		if entry.Target != Forwarder || entry.Value != "0" {
			t.Fatal(entry)
		}
	}
	if CheckBundle(t.Context(), rpc, b) {
		t.Fatal("connected from signatures alone")
	}
	for id := range granted {
		granted[id] = true
		configured[id] = true
		final[id] = id != 1
	}
	if CheckBundle(t.Context(), rpc, b) || !b.Chains[1].Ready || b.Chains[0].Ready {
		t.Fatal("lost partial progress or ignored finality")
	}
	final[1] = true
	if !CheckBundle(t.Context(), rpc, b) {
		t.Fatal("did not recognize finalized setup")
	}
	configured[8453] = false
	if CheckBundle(t.Context(), rpc, b) {
		t.Fatal("ignored failed inner call")
	}
	// Expired authorizations can be reviewed, but a funding attempt of unknown
	// outcome cannot be discarded and paid for again.
	b.Attempt = &Attempt{State: "signing"}
	if err := PlanBundle(t.Context(), rpc, b, setupWallet, "0.006"); err == nil {
		t.Fatal("discarded unknown payment")
	}
	b.Attempt = nil
	if err := PlanBundle(t.Context(), rpc, b, setupWallet, "0.005"); err != nil {
		t.Fatal(err)
	}
	for _, chain := range b.Chains {
		want := 0
		if chain.Connection.ChainID == 8453 {
			want = 1
		}
		if len(chain.Requests) != want {
			t.Fatal("repeated already completed setup")
		}
	}
}
func TestForwardBatchMatchesEthersABI(t *testing.T) {
	var fixture struct {
		Requests []ForwardRequest `json:"requests"`
		Data     string           `json:"data"`
	}
	raw, err := os.ReadFile("testdata/forward-batch.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if got := ForwardBatch(fixture.Requests); got != fixture.Data {
		t.Fatalf("batch ABI mismatch\ngot %s\nwant %s", got, fixture.Data)
	}
}
func TestRelayrPaymentValidation(t *testing.T) {
	uuid := "12345678-1234-1234-1234-123456789abc"
	deadline := time.Now().Add(time.Minute).Unix()
	p := RelayrPayment{Chain: 8453, Amount: "12345", Target: RelayrPaymentAddress, Token: "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Calldata: "0x103903a7" + strings.ReplaceAll(uuid, "-", "") + strings.Repeat("0", 32) + abiN(uint64(deadline)), PaymentDeadline: json.RawMessage(fmt.Sprint(deadline))}
	if _, err := PaymentDetails(p, uuid, false, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	hexPayment := p
	hexPayment.Amount = "0x42d7605338"
	if _, err := PaymentDetails(hexPayment, uuid, false, time.Now().Unix()); err != nil {
		t.Fatal("rejected live API amount format", err)
	}
	n, ok := relayrAmount(hexPayment.Amount)
	if !ok || n.String() != "287081255736" {
		t.Fatal(n, ok)
	}
	for _, mutate := range []func(*RelayrPayment){func(p *RelayrPayment) { p.Target = setupWallet }, func(p *RelayrPayment) { p.Token = setupWallet }, func(p *RelayrPayment) { p.Amount = "-1" }, func(p *RelayrPayment) { p.Calldata = p.Calldata[:10] + strings.Repeat("0", 128) }, func(p *RelayrPayment) { p.Chain = 84532 }, func(p *RelayrPayment) { p.PaymentDeadline = json.RawMessage("1") }, func(p *RelayrPayment) { p.Calldata += "00" }} {
		bad := p
		mutate(&bad)
		if _, err := PaymentDetails(bad, uuid, false, time.Now().Unix()); err == nil {
			t.Fatal("accepted malformed payment", bad)
		}
	}
	if _, err := PaymentDetails(p, "aaaaaaaa-1234-1234-1234-123456789abc", false, time.Now().Unix()); err == nil {
		t.Fatal("accepted another bundle")
	}
	if _, err := PaymentDetails(p, uuid, false, deadline); err == nil {
		t.Fatal("accepted expired quote")
	}
}

func TestFundingQuoteCannotOutliveSignatures(t *testing.T) {
	uuid := "12345678-1234-1234-1234-123456789abc"
	deadline := time.Now().Add(15 * time.Minute).Unix()
	p := RelayrPayment{Chain: 8453, Amount: "12345", Target: RelayrPaymentAddress, Token: "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Calldata: "0x103903a7" + strings.ReplaceAll(uuid, "-", "") + strings.Repeat("0", 32) + abiN(uint64(deadline)), PaymentDeadline: json.RawMessage(fmt.Sprint(deadline))}
	b := &SetupBundle{From: setupWallet, Deadline: deadline - 30, Quote: &RelayrQuote{UUID: uuid}, Chains: []*BundleChain{{Connection: &Connection{ChainID: 8453}}}}
	rpc := func(context.Context, int, string, any, any) error {
		t.Fatal("must reject before simulating or funding")
		return nil
	}
	if err := ValidatePayment(t.Context(), rpc, b, p); err == nil {
		t.Fatal("payment could outlive its requests")
	}
}
