package shop

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"
)

const setupHook = "0x2222222222222222222222222222222222222222"
const setupWallet = "0x1111111111111111111111111111111111111111"

func TestConnectionPlanPreservesPermissionsAndRules(t *testing.T) {
	existing := new(big.Int).Lsh(big.NewInt(1), 25)
	s := &ConnectionState{Owner: setupWallet, ProjectID: "42", CanGrant: true, CanConfigure: true, Decimals: 18, Permissions: "0x" + existing.Text(16), Criteria: PostingCriteria{MinimumPrice: "3", MinimumTotalSupply: 2, MaximumTotalSupply: 50, MaximumSplitPercent: 75000000, AllowedAddresses: []string{setupWallet}}}
	c := &Connection{Hook: setupHook, ChainID: 8453, Category: 9}
	if e := PlanConnection(c, s, setupWallet, "0.002"); e != nil {
		t.Fatal(e)
	}
	tx := c.Transaction
	if tx.Kind != "permission" || tx.To != Permissions {
		t.Fatal(tx)
	}
	// Decode the dynamic array: existing permission 25 must survive, and only 24 is added.
	data := tx.Data[10:]
	ids := data[6*64:]
	if ids != abiN(24)+abiN(25) {
		t.Fatalf("permission list %s", ids)
	}
	before := s.Criteria
	expected := *c.Expected
	expected.MinimumPrice = before.MinimumPrice
	if !reflect.DeepEqual(before, expected) {
		t.Fatal("changed supply, split, or allowlist")
	}
	s.PublisherGranted = true
	next, e := NextSetup(c, s, setupWallet)
	if e != nil || next.Kind != "criteria" || next.To != Publisher {
		t.Fatal(next, e)
	}
	s.Criteria = *c.Expected
	next, e = NextSetup(c, s, setupWallet)
	if e != nil || next != nil {
		t.Fatal("repeated already configured setup", next, e)
	}
}
func TestRevnetSetupChecksActualPermissions(t *testing.T) {
	s := &ConnectionState{Owner: RevnetOwner, ProjectID: "7", PublisherGranted: true, CanConfigure: true, Decimals: 6, Permissions: "0x1000000", Criteria: PostingCriteria{MinimumPrice: "0", AllowedAddresses: []string{}}}
	c := &Connection{Hook: setupHook, ChainID: 8453, Category: 1}
	if e := PlanConnection(c, s, setupWallet, "1.25"); e != nil {
		t.Fatal(e)
	}
	if c.Transaction.Kind != "criteria" || c.Expected.MinimumPrice != "1250000" || c.Expected.MinimumTotalSupply != 1 || c.Expected.MaximumSplitPercent != 100000000 {
		t.Fatal(c)
	}
	s.PublisherGranted = false
	s.CanGrant = false
	if _, e := NextSetup(c, s, setupWallet); e == nil {
		t.Fatal("assumed every revnet already granted permission")
	}
	s.PublisherGranted = true
	s.CanConfigure = false
	if _, e := NextSetup(c, s, setupWallet); e == nil {
		t.Fatal("allowed an unauthorized operator")
	}
}
func TestConnectionPriceLimits(t *testing.T) {
	for _, v := range []string{"-1", "1e18", "1.0000001", "0.0000000000000000001", "99999999999999999999999999999999"} {
		if _, e := PriceUnits(v, 6); e == nil {
			t.Fatal(v)
		}
	}
	if p, e := PriceUnits("1.25", 6); e != nil || p != "1250000" {
		t.Fatal(p, e)
	}
	if p, e := PriceUnits("0", 0); e != nil || p != "0" {
		t.Fatal(p, e)
	}
}
func TestConnectionReadsCorrectScopeAndCurrency(t *testing.T) {
	c := &Connection{Hook: setupHook, ChainID: 8453, Category: 5}
	rpc := func(_ context.Context, id int, method string, params any, out any) error {
		if id != 8453 {
			t.Fatal(id)
		}
		var result any
		switch method {
		case "eth_chainId":
			result = "0x2105"
		case "eth_getCode":
			result = "0x1234"
		case "eth_call":
			p := params.([]any)
			if p[1] != "finalized" {
				t.Fatal("unfinalized read")
			}
			call := p[0].(map[string]string)
			data := call["data"]
			switch data[:10] {
			case topic("PERMISSIONS()")[:10]:
				result = "0x" + abiAddress(Permissions)
			case topic("owner()")[:10]:
				result = "0x" + abiAddress(RevnetOwner)
			case topic("projectId()")[:10]:
				result = "0x" + abiN(42)
			case topic("pricingContext()")[:10]:
				result = "0x" + abiN(2) + abiN(6)
			case topic("hasPermission(address,address,uint256,uint256,bool,bool)")[:10]:
				if !strings.HasSuffix(data, abiAddress(RevnetOwner)+abiN(42)+abiN(24)+abiN(1)+abiN(1)) && !strings.HasSuffix(data, abiAddress(RevnetOwner)+abiN(42)+abiN(1)+abiN(1)+abiN(1)) {
					t.Fatal("wrong permission scope", data)
				}
				result = "0x" + abiN(1)
			case topic("permissionsOf(address,address,uint256)")[:10]:
				result = "0x" + abiN(1<<24)
			case topic("allowanceFor(address,uint256)")[:10]:
				if data != abiCall("allowanceFor(address,uint256)", abiAddress(setupHook), abiN(5)) {
					t.Fatal(data)
				}
				result = "0x" + abiN(2500000) + abiN(1) + abiN(20) + abiN(100000000) + abiN(160) + abiN(1) + abiAddress(setupWallet)
			default:
				return fmt.Errorf("unexpected call %s", data)
			}
		default:
			return fmt.Errorf("unexpected RPC %s", method)
		}
		b, _ := json.Marshal(result)
		return json.Unmarshal(b, out)
	}
	s, e := ReadConnection(context.Background(), rpc, c, setupWallet, "finalized")
	if e != nil {
		t.Fatal(e)
	}
	if !s.PublisherGranted || !s.CanConfigure || s.ProjectID != "42" || s.Currency != 2 || s.Decimals != 6 || s.Criteria.MinimumPrice != "2500000" || len(s.Criteria.AllowedAddresses) != 1 {
		t.Fatal(s)
	}
}
func TestConnectionKeepsUnknownWalletIntent(t *testing.T) {
	c := &Connection{Transaction: &SetupTransaction{Attempt: &Attempt{State: "signing"}}, Expected: &PostingCriteria{}}
	if e := PlanConnection(c, &ConnectionState{Decimals: 18}, setupWallet, "0.01"); e == nil {
		t.Fatal("replaced uncertain transaction")
	}
}

func TestSetupMatchesDeployedABI(t *testing.T) {
	data, e := os.ReadFile("testdata/connect-abi.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct{ Criteria, Grant string }
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	c := &Connection{Hook: setupHook, Category: 9, Expected: &PostingCriteria{MinimumPrice: "2000000000000000", MinimumTotalSupply: 2, MaximumTotalSupply: 50, MaximumSplitPercent: 75000000, AllowedAddresses: []string{setupWallet}}}
	if got := criteriaData(c, *c.Expected); got != fixture.Criteria {
		t.Fatal("posting ABI mismatch", got)
	}
	s := &ConnectionState{Owner: setupWallet, ProjectID: "42", CanGrant: true, Permissions: "0x2000000"}
	tx, e := NextSetup(c, s, setupWallet)
	if e != nil || tx.Data != fixture.Grant {
		t.Fatal("permissions ABI mismatch", tx, e)
	}
}
