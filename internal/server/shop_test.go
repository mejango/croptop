package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mejango/croptop/internal/shop"
	"github.com/mejango/croptop/internal/store"
	"golang.org/x/crypto/sha3"
)

const shopSiteID = "11111111-1111-1111-1111-111111111111"
const shopWallet = "0x1111111111111111111111111111111111111111"

func shopReq(t *testing.T, handler http.Handler, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "http://127.0.0.1:8086"+path, bytes.NewReader(b))
	req.Header.Set("X-Croptop-Shop", "1")
	req.Header.Set("X-Shop-Token", token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}
func newShopTest(t *testing.T) (*Server, http.Handler, *shop.Session) {
	t.Helper()
	s, ts := testServer(t)
	_ = ts
	site := &store.Site{ID: shopSiteID, Name: "My shop", TemplateName: "Croptop", Created: store.Now(), Updated: 1}
	if err := s.Store.SaveSite(site); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SaveTemplateSettings(site.ID, map[string]any{"backgroundColor": "#123456", "maintenanceMessage": "Keep me"}); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	code, _ := shopReq(t, h, "POST", "/v0/croptop/sites/"+shopSiteID+"/shop", "", nil)
	if code != 200 {
		t.Fatal(code)
	}
	session, err := shop.Load(s.shopDir(), shopSiteID)
	if err != nil {
		t.Fatal(err)
	}
	return s, h, session
}
func planShop(t *testing.T, h http.Handler, session *shop.Session) {
	t.Helper()
	chains := []shop.Chain{{ID: 8453, Data: shop.DeploySelector + strings.Repeat("0", 64) + session.Salt[2:], Value: "0x0", Attempts: []shop.Attempt{}}, {ID: 10, Data: shop.DeploySelector + strings.Repeat("0", 64) + session.Salt[2:], Value: "0x0", Attempts: []shop.Attempt{}}}
	config := shop.Config{Name: "My shop", Symbol: "SHOP", Owner: shopWallet, Price: "0.01", ChainIDs: []int{8453, 10}}
	code, res := shopReq(t, h, "POST", "/v0/croptop/sites/"+shopSiteID+"/shop/"+session.ID, session.Token, map[string]any{"action": "plan", "config": config, "chains": chains})
	if code != 200 {
		t.Fatalf("plan %d %v", code, res)
	}
}
func TestShopAuthorization(t *testing.T) {
	_, h, session := newShopTest(t)
	path := "/v0/croptop/sites/" + shopSiteID + "/shop/" + session.ID
	for _, origin := range []string{"https://evil.example", "null", "http://127.0.0.1:9999"} {
		req := httptest.NewRequest("POST", "http://127.0.0.1:8086"+path, strings.NewReader(`{}`))
		req.Header.Set("Origin", origin)
		req.Header.Set("X-Croptop-Shop", "1")
		req.Header.Set("X-Shop-Token", session.Token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatal(origin, w.Code)
		}
	}
	if code, _ := shopReq(t, h, "POST", path, "wrong", map[string]any{"action": "plan"}); code != 403 {
		t.Fatal(code)
	}
	if code, _ := shopReq(t, h, "POST", strings.Replace(path, session.ID, "wrong", 1), session.Token, map[string]any{}); code != 403 {
		t.Fatal(code)
	}
	code, res := shopReq(t, h, "GET", "/v0/croptop/sites/"+shopSiteID+"/shop", "", nil)
	b, _ := json.Marshal(res)
	if code != 200 || bytes.Contains(b, []byte(session.Token)) {
		t.Fatal("leaked secret")
	}
	req := httptest.NewRequest("POST", "http://127.0.0.1:8086/v0/croptop/sites/"+shopSiteID+"/shop", nil)
	req.Header.Set("X-Croptop-Shop", "1")
	req.Header.Set("Referer", "http://127.0.0.1:8086/"+shopSiteID+"/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("accepted public preview")
	}
}
func TestShopSigningRecovery(t *testing.T) {
	s, h, session := newShopTest(t)
	planShop(t, h, session)
	path := "/v0/croptop/sites/" + shopSiteID + "/shop/" + session.ID
	send := func(action, nonce string) int {
		code, _ := shopReq(t, h, "POST", path, session.Token, map[string]any{"action": action, "chainId": 8453, "from": shopWallet, "nonce": nonce})
		return code
	}
	if send("signing", "0x5") != 200 {
		t.Fatal("signing failed")
	}
	// Lost response / process restart retains the nonce and all selected chains.
	restored, err := shop.Load(s.shopDir(), shopSiteID)
	if err != nil || restored.Chains[0].Attempts[0].Nonce != "0x5" {
		t.Fatal(err)
	}
	if send("signing", "0x6") != 409 {
		t.Fatal("allowed duplicate deployment")
	}
	if send("signing", "0x5") != 200 {
		t.Fatal("cannot resume original nonce")
	}
	if send("cancelled", "") != 200 {
		t.Fatal("cancel failed")
	}
	if send("signing", "0x6") != 409 {
		t.Fatal("cancel changed nonce")
	}
	if send("signing", "0x5") != 200 {
		t.Fatal("cancelled retry failed")
	}
	if code, _ := shopReq(t, h, "POST", path, session.Token, map[string]any{"action": "edit"}); code != 409 {
		t.Fatal("changed live intent")
	}
	hash := "0x" + strings.Repeat("a", 64)
	if code, _ := shopReq(t, h, "POST", path, session.Token, map[string]any{"action": "submitted", "chainId": 8453, "hash": hash}); code != 200 {
		t.Fatal(code)
	}
	if send("signing", "0x5") != 409 {
		t.Fatal("signed pending transaction again")
	}
}
func TestShopPartialCompletionAndSettings(t *testing.T) {
	s, h, session := newShopTest(t)
	planShop(t, h, session)
	path := "/v0/croptop/sites/" + shopSiteID + "/shop/" + session.ID
	hash := "0x" + strings.Repeat("a", 64)
	block := "0x" + strings.Repeat("b", 64)
	hook := "0x2222222222222222222222222222222222222222"
	for _, in := range []map[string]any{{"action": "signing", "chainId": 8453, "from": shopWallet, "nonce": "0x5"}, {"action": "submitted", "chainId": 8453, "hash": hash}} {
		if code, _ := shopReq(t, h, "POST", path, session.Token, in); code != 200 {
			t.Fatal(code)
		}
	}
	topic := sha3.NewLegacyKeccak256()
	topic.Write([]byte("HookDeployed(uint256,address,address)"))
	event := "0x" + hex.EncodeToString(topic.Sum(nil))
	s.shopRPC = func(_ context.Context, chain int, method string, params any, out any) error {
		if chain != 8453 {
			return fmt.Errorf("wrong network %d", chain)
		}
		var result any
		switch method {
		case "eth_chainId":
			result = "0x2105"
		case "eth_getTransactionByHash":
			result = map[string]any{"hash": hash, "from": shopWallet, "to": shop.Deployer, "input": shop.DeploySelector + strings.Repeat("0", 64) + session.Salt[2:], "value": "0x0", "nonce": "0x5", "blockHash": block}
		case "eth_getTransactionReceipt":
			result = map[string]any{"transactionHash": hash, "blockHash": block, "blockNumber": "0x10", "status": "0x1", "to": shop.Deployer, "from": shopWallet, "logs": []any{map[string]any{"address": shop.HookDeployer, "topics": []string{event, fmt.Sprintf("0x%064x", 7)}, "data": "0x" + strings.Repeat("0", 24) + hook[2:] + strings.Repeat("0", 24) + shop.Deployer[2:], "blockHash": block, "transactionHash": hash}}}
		case "eth_getBlockByNumber":
			result = map[string]any{"number": "0x10", "hash": block}
		case "eth_getCode":
			result = "0x1234"
		case "eth_call":
			result = "0x7"
		default:
			return fmt.Errorf("unexpected %s", method)
		}
		b, _ := json.Marshal(result)
		return json.Unmarshal(b, out)
	}
	verify := map[string]any{"action": "verify", "chainId": 8453}
	if code, res := shopReq(t, h, "POST", path, session.Token, verify); code != 200 {
		t.Fatalf("verify %d %v", code, res)
	}
	stored, err := s.Store.TemplateSettings(shopSiteID)
	if err != nil {
		t.Fatal(err)
	}
	if stored["baseMainnetCollectionAddress"] != hook || stored["optimismMainnetCollectionAddress"] != nil || stored["backgroundColor"] != "#123456" || stored["maintenanceMessage"] != "Keep me" {
		t.Fatal(stored)
	}
	site, _ := s.Store.Site(shopSiteID)
	if site.Updated <= 1 {
		t.Fatal("not marked dirty")
	}
	// PATCH from a stale settings window preserves the newly deployed address.
	if code, _ := shopReq(t, h, "PATCH", "/v0/croptop/sites/"+shopSiteID+"/settings", "", map[string]any{"backgroundColor": "#abcdef"}); code != 200 {
		t.Fatal(code)
	}
	stored, _ = s.Store.TemplateSettings(shopSiteID)
	if stored["baseMainnetCollectionAddress"] != hook || stored["backgroundColor"] != "#abcdef" {
		t.Fatal(stored)
	}
	// Confirmation journaling repairs a crash between receipt validation and apply.
	saved, _ := shop.Load(s.shopDir(), shopSiteID)
	if !saved.Chains[0].Applied || saved.Chains[1].Applied {
		t.Fatal("wrong partial state")
	}
	saved.Chains[0].Applied = false
	if err := shop.Save(s.shopDir(), saved); err != nil {
		t.Fatal(err)
	}
	if code, _ := shopReq(t, h, "POST", path, session.Token, verify); code != 200 {
		t.Fatal(code)
	}
	// A replay cannot overwrite a shop subsequently connected manually.
	stored["baseMainnetCollectionAddress"] = "manual"
	s.Store.SaveTemplateSettings(shopSiteID, stored)
	if code, _ := shopReq(t, h, "POST", path, session.Token, verify); code != 200 {
		t.Fatal(code)
	}
	stored, _ = s.Store.TemplateSettings(shopSiteID)
	if stored["baseMainnetCollectionAddress"] != "manual" {
		t.Fatal("replay overwrote manual shop")
	}
}
func TestShopPageHeaders(t *testing.T) {
	_, h, _ := newShopTest(t)
	r := httptest.NewRequest("GET", "http://127.0.0.1:8086/shop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal(w.Header())
	}
	_, _ = io.Copy(io.Discard, w.Result().Body)
}

func TestShopPublicDocumentsCannotReadConsoleCapabilities(t *testing.T) {
	_, h, _ := newShopTest(t)
	for _, path := range []string{"/" + shopSiteID + "/", "/f/example/"} {
		req := httptest.NewRequest("GET", "http://127.0.0.1:8086"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		policy := w.Header().Get("Content-Security-Policy")
		if !strings.HasPrefix(policy, "sandbox ") || strings.Contains(policy, "allow-same-origin") {
			t.Fatalf("unisolated preview %s: %s", path, policy)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatal("opaque previews cannot read public assets")
		}
	}
}
