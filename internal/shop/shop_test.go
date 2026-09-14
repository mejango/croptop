package shop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const wallet = "0x1111111111111111111111111111111111111111"
const hook = "0x2222222222222222222222222222222222222222"

var txHash = "0x" + strings.Repeat("a", 64)
var blockHash = "0x" + strings.Repeat("b", 64)

func fixture() (*Chain, map[string]any) {
	ch := &Chain{ID: 8453, Data: "0x1234", Value: "0x10", Attempts: []Attempt{{From: wallet, Nonce: "0x2", State: "pending", Hash: txHash}}}
	tx := &Transaction{Hash: txHash, From: wallet, To: Deployer, Input: ch.Data, Value: ch.Value, Nonce: "0x2", BlockHash: blockHash}
	r := &Receipt{TransactionHash: txHash, BlockHash: blockHash, BlockNumber: "0x10", Status: "0x1", From: wallet, To: Deployer, Logs: []Log{{Address: HookDeployer, Topics: []string{topic("HookDeployed(uint256,address,address)"), fmt.Sprintf("0x%064x", 7)}, Data: "0x" + strings.Repeat("0", 24) + hook[2:] + strings.Repeat("0", 24) + Deployer[2:], BlockHash: blockHash, TransactionHash: txHash}}}
	return ch, map[string]any{"eth_chainId": "0x2105", "eth_getTransactionByHash": tx, "eth_getTransactionReceipt": r, "block": map[string]string{"hash": blockHash, "number": "0x10"}, "finalized": map[string]string{"number": "0x10"}, "eth_getCode": "0x1234", "eth_call": "0x7"}
}
func mock(values map[string]any) RPC {
	return func(_ context.Context, id int, method string, params any, out any) error {
		if id != 8453 {
			return fmt.Errorf("wrong chain %d", id)
		}
		key := method
		if method == "eth_getBlockByNumber" {
			key = "block"
			if params.([]any)[0] == "finalized" {
				key = "finalized"
			}
		}
		value, ok := values[key]
		if !ok {
			return fmt.Errorf("unexpected method %s", key)
		}
		b, _ := json.Marshal(value)
		return json.Unmarshal(b, out)
	}
}
func TestVerifyReceipt(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any)
		want   string
		bad    bool
	}{
		{"confirmed", func(map[string]any) {}, "confirmed", false},
		{"unseen hash", func(v map[string]any) { v["eth_getTransactionByHash"] = nil }, "unknown", false},
		{"pending", func(v map[string]any) { v["eth_getTransactionReceipt"] = nil }, "pending", false},
		{"not finalized", func(v map[string]any) { v["finalized"] = map[string]string{"number": "0xf"} }, "pending", false},
		{"reorg", func(v map[string]any) { v["block"] = map[string]string{"hash": "0x123", "number": "0x10"} }, "pending", false},
		{"wrong chain", func(v map[string]any) { v["eth_chainId"] = "0x1" }, "", true},
		{"wrong wallet", func(v map[string]any) { v["eth_getTransactionByHash"].(*Transaction).From = hook }, "", true},
		{"wrong nonce", func(v map[string]any) { v["eth_getTransactionByHash"].(*Transaction).Nonce = "0x3" }, "", true},
		{"reverted", func(v map[string]any) { v["eth_getTransactionReceipt"].(*Receipt).Status = "0x0" }, "reverted", false},
		{"replaced calldata", func(v map[string]any) { v["eth_getTransactionByHash"].(*Transaction).Input = "0x9999" }, "replaced", false},
		{"replaced value", func(v map[string]any) { v["eth_getTransactionByHash"].(*Transaction).Value = "0x11" }, "replaced", false},
		{"wrong receipt tx", func(v map[string]any) { v["eth_getTransactionReceipt"].(*Receipt).TransactionHash = blockHash }, "", true},
		{"wrong event emitter", func(v map[string]any) { v["eth_getTransactionReceipt"].(*Receipt).Logs[0].Address = hook }, "", true},
		{"removed event", func(v map[string]any) { v["eth_getTransactionReceipt"].(*Receipt).Logs[0].Removed = true }, "", true},
		{"wrong caller", func(v map[string]any) {
			l := &v["eth_getTransactionReceipt"].(*Receipt).Logs[0]
			l.Data = l.Data[:66] + strings.Repeat("0", 24) + wallet[2:]
		}, "", true},
		{"truncated event", func(v map[string]any) { v["eth_getTransactionReceipt"].(*Receipt).Logs[0].Data = "0x1" }, "", true},
		{"duplicate events", func(v map[string]any) {
			r := v["eth_getTransactionReceipt"].(*Receipt)
			r.Logs = append(r.Logs, r.Logs[0])
		}, "", true},
		{"no hook code", func(v map[string]any) { v["eth_getCode"] = "0x" }, "", true},
		{"wrong hook project", func(v map[string]any) { v["eth_call"] = "0x8" }, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, v := fixture()
			tt.change(v)
			state, err := Verify(context.Background(), mock(v), ch, txHash)
			if (err != nil) != tt.bad || state != tt.want {
				t.Fatalf("state=%s error=%v", state, err)
			}
			if state == "confirmed" {
				if ch.Address != hook || ch.ProjectID != "7" {
					t.Fatalf("wrong result %+v", ch)
				}
			} else if ch.Address != "" {
				t.Fatal("saved unverified hook")
			}
		})
	}
}
func TestSessionPersistenceAndPlan(t *testing.T) {
	s := &Session{ID: Random(), SiteID: "site", SiteName: "Shop", Token: Random(), Salt: "0x" + Random(), Config: &Config{Name: "Shop", Symbol: "SHOP", Owner: wallet, Price: "0.001", ChainIDs: []int{8453}}, Chains: []Chain{{ID: 8453, Data: "0x12345678" + strings.Repeat("0", 64), Value: "0x0"}}}
	s.Chains[0].Data = DeploySelector + strings.Repeat("0", 64) + s.Salt[2:]
	if err := s.ValidatePlan(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir, "site")
	if err != nil || loaded.Token != s.Token || loaded.Salt != s.Salt {
		t.Fatal("lost recovery identity", err)
	}
	s.Chains[0].Address = hook
	if s.ValidatePlan() == nil {
		t.Fatal("accepted fabricated result")
	}
	s.Chains[0].Address = ""
	s.Config.ChainIDs = []int{1, 1}
	s.Chains = append(s.Chains, s.Chains[0])
	if s.ValidatePlan() == nil {
		t.Fatal("accepted duplicate network")
	}
}
func TestNetworkSettingsMapping(t *testing.T) {
	want := map[int]string{1: "ethereumMainnet", 10: "optimismMainnet", 42161: "arbitrumMainnet", 8453: "baseMainnet", 11155111: "ethereumSepolia", 11155420: "optimismSepolia", 421614: "arbitrumSepolia", 84532: "baseSepolia"}
	for id, key := range want {
		n, ok := NetworkFor(id)
		if !ok || n.Setting != key {
			t.Fatalf("%d mapped to %s", id, n.Setting)
		}
	}
}
