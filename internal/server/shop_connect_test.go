package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mejango/croptop/internal/shop"
	"golang.org/x/crypto/sha3"
)

const connectionHook = "0x2222222222222222222222222222222222222222"

func connectSelector(signature string) string {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(signature))
	return "0x" + hex.EncodeToString(h.Sum(nil))[:8]
}
func connectWord(n uint64) string    { return fmt.Sprintf("%064x", n) }
func connectAddress(a string) string { return strings.Repeat("0", 24) + a[2:] }
func TestExistingShopSetup(t *testing.T) {
	for _, revnet := range []bool{false, true} {
		t.Run(fmt.Sprint("revnet=", revnet), func(t *testing.T) {
			s, h, _ := newShopTest(t)
			base := "/v0/croptop/sites/" + shopSiteID + "/shop-connect"
			code, _ := shopReq(t, h, "POST", base, "", map[string]string{"setting": "baseMainnetCollectionAddress", "hook": connectionHook, "category": "1"})
			if code != 200 {
				t.Fatal(code)
			}
			saved, e := shop.Load(s.connectionDir(8453), shopSiteID)
			if e != nil {
				t.Fatal(e)
			}
			endpoint := base + "/8453/" + saved.ID
			readURL := base + "/8453"
			token := saved.Token
			granted, configured, finalized := revnet, false, false
			maxSupply := uint64(50)
			var tx *shop.SetupTransaction
			hash := "0x" + strings.Repeat("a", 64)
			block := "0x" + strings.Repeat("b", 64)
			owner := shopWallet
			if revnet {
				owner = shop.RevnetOwner
			}
			s.shopRPC = func(_ context.Context, id int, method string, params any, out any) error {
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
					q := p[0].(map[string]string)
					data := q["data"]
					isFinal := p[1] == "finalized"
					switch data[:10] {
					case connectSelector("PERMISSIONS()"):
						result = "0x" + connectAddress(shop.Permissions)
					case connectSelector("owner()"):
						result = "0x" + connectAddress(owner)
					case connectSelector("projectId()"):
						result = "0x" + connectWord(42)
					case connectSelector("pricingContext()"):
						result = "0x" + connectWord(1) + connectWord(18)
					case connectSelector("permissionsOf(address,address,uint256)"):
						result = "0x" + connectWord(1<<25)
					case connectSelector("hasPermission(address,address,uint256,uint256,bool,bool)"):
						operator := "0x" + data[10+24:10+64]
						value := uint64(1)
						if operator == shop.Publisher && (!granted || isFinal && !finalized) {
							value = 0
						}
						result = "0x" + connectWord(value)
					case connectSelector("allowanceFor(address,uint256)"):
						price := uint64(1000000000000000)
						if configured && (!isFinal || finalized) {
							price = 5000000000000000
						}
						result = "0x" + connectWord(price) + connectWord(2) + connectWord(maxSupply) + connectWord(75000000) + connectWord(160) + connectWord(1) + connectAddress(shopWallet)
					default:
						return fmt.Errorf("unexpected call %s", data)
					}
				case "eth_getTransactionByHash":
					result = map[string]any{"hash": hash, "from": shopWallet, "to": tx.To, "input": tx.Data, "value": "0x0", "nonce": tx.Attempt.Nonce, "blockHash": block}
				case "eth_getTransactionReceipt":
					result = map[string]any{"transactionHash": hash, "from": shopWallet, "to": tx.To, "status": "0x1", "blockNumber": "0x64", "blockHash": block}
				case "eth_getBlockByNumber":
					height := "0x64"
					if params.([]any)[0] == "finalized" && !finalized {
						height = "0x63"
					}
					result = map[string]string{"hash": block, "number": height}
				default:
					return fmt.Errorf("unexpected RPC %s", method)
				}
				b, _ := json.Marshal(result)
				return json.Unmarshal(b, out)
			}
			action := func(name string, extra map[string]any, intended int) {
				t.Helper()
				if extra == nil {
					extra = map[string]any{}
				}
				extra["action"] = name
				code, res := shopReq(t, h, "POST", endpoint, token, extra)
				if code != intended {
					t.Fatalf("%s: %d %v", name, code, res)
				}
			}
			if code, _ := shopReq(t, h, "GET", readURL, "wrong", nil); code != 403 {
				t.Fatal("unauthorized read")
			}
			action("inspect", nil, 200)
			action("plan", map[string]any{"from": shopWallet, "price": "0.005"}, 200)
			session, e := shop.Load(s.connectionDir(8453), shopSiteID)
			if e != nil {
				t.Fatal(e)
			}
			if revnet && session.Connection.Transaction.Kind != "criteria" || !revnet && session.Connection.Transaction.Kind != "permission" {
				t.Fatal(session.Connection.Transaction)
			}
			action("finish", nil, 409)
			for step := 0; step < 2; step++ {
				session, _ = shop.Load(s.connectionDir(8453), shopSiteID)
				kind := session.Connection.Transaction.Kind
				if kind == "criteria" {
					maxSupply = 60
					action("inspect", nil, 200)
					action("signing", map[string]any{"from": shopWallet, "nonce": fmt.Sprintf("0x%x", step+1)}, 409)
					maxSupply = 50
					action("inspect", nil, 200)
				}
				action("signing", map[string]any{"from": shopWallet, "nonce": fmt.Sprintf("0x%x", step+1)}, 200)
				// Explicit rejection retains the nonce; changing it on a retry is rejected.
				action("cancelled", nil, 200)
				action("signing", map[string]any{"from": shopWallet, "nonce": "0xff"}, 409)
				action("signing", map[string]any{"from": shopWallet, "nonce": fmt.Sprintf("0x%x", step+1)}, 200)
				action("submitted", map[string]any{"hash": hash}, 200)
				session, _ = shop.Load(s.connectionDir(8453), shopSiteID)
				tx = session.Connection.Transaction
				action("plan", map[string]any{"from": shopWallet, "price": "0.006"}, 409)
				action("verify", map[string]any{"hash": hash}, 200)
				session, _ = shop.Load(s.connectionDir(8453), shopSiteID)
				if session.Connection.Transaction.Attempt.State != "pending" {
					t.Fatal("accepted unfinalized transaction")
				}
				finalized = true
				if kind == "permission" {
					granted = true
				} else {
					configured = true
				}
				action("verify", map[string]any{"hash": hash}, 200)
				if kind == "criteria" {
					break
				}
				action("plan", map[string]any{"from": shopWallet, "price": "0.005"}, 200)
				finalized = false
			}

			conflicting, _ := s.Store.TemplateSettings(shopSiteID)
			conflicting["baseMainnetCollectionAddress"] = "0x3333333333333333333333333333333333333333"
			if e := s.Store.SaveTemplateSettings(shopSiteID, conflicting); e != nil {
				t.Fatal(e)
			}
			action("finish", nil, 409)
			delete(conflicting, "baseMainnetCollectionAddress")
			conflicting["collectionCategory"] = "7"
			if e := s.Store.SaveTemplateSettings(shopSiteID, conflicting); e != nil {
				t.Fatal(e)
			}
			action("finish", nil, 409)
			delete(conflicting, "collectionCategory")
			if e := s.Store.SaveTemplateSettings(shopSiteID, conflicting); e != nil {
				t.Fatal(e)
			}
			action("finish", nil, 200)
			settings, e := s.Store.TemplateSettings(shopSiteID)
			if e != nil {
				t.Fatal(e)
			}
			if settings["baseMainnetCollectionAddress"] != connectionHook || settings["collectionCategory"] != "1" || settings["maintenanceMessage"] != "Keep me" {
				t.Fatal(settings)
			}
			code, res := shopReq(t, h, "GET", readURL, token, nil)
			if code != 200 {
				t.Fatal(code)
			}
			b, _ := json.Marshal(res)
			if strings.Contains(string(b), token) {
				t.Fatal("leaked capability")
			}
		})
	}
}
func TestConnectionPageAndOrigin(t *testing.T) {
	s, h, _ := newShopTest(t)
	s.UI = fstest.MapFS{"shop-connect.html": {Data: []byte("shop setup")}}
	req := httptest.NewRequest("GET", "http://127.0.0.1:8086/shop-connect", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}
	req = httptest.NewRequest("POST", "http://127.0.0.1:8086/v0/croptop/sites/"+shopSiteID+"/shop-connect", strings.NewReader(`{}`))
	req.Header.Set("Origin", "null")
	req.Header.Set("X-Croptop-Shop", "1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
