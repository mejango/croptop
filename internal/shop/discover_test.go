package shop

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

func TestProjectDiscoveryUsesVerifiedBridgePeers(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			base := bundleRPC(map[int]bool{1: true, 10: true}, map[int]bool{}, map[int]bool{})
			left := "0x" + strings.Repeat("4", 40)
			right := "0x" + strings.Repeat("5", 40)
			rpc := func(ctx context.Context, id int, method string, params any, out any) error {
				if method != "eth_call" {
					return base(ctx, id, method, params, out)
				}
				q := params.([]any)[0].(map[string]string)
				data := q["data"]
				to := q["to"]
				pid := uint64(42)
				bridge, peer := left, right
				remote := uint64(10)
				if id == 10 {
					pid = 73
					bridge, peer = right, left
					remote = 1
				}
				var value string
				switch data[:10] {
				case abiCall("controllerOf(uint256)"):
					value = "0x" + abiAddress("0x"+strings.Repeat("3", 40))
				case abiCall("currentRulesetOf(uint256)"):
					hook := RevnetOwner
					if id == 10 {
						hook = setupHook
					}
					m, _ := new(big.Int).SetString(hook[2:], 16)
					m.Lsh(m, 82)
					value = "0x" + strings.Repeat(abiN(0), 8) + abiUint(m)
				case abiCall("tiered721HookOf(uint256)"):
					value = "0x" + abiAddress(setupHook)
				case abiCall("suckersOf(uint256)"):
					value = "0x" + abiN(32) + abiN(1) + abiAddress(bridge)
				case abiCall("peerChainId()"):
					value = "0x" + abiN(remote)
				case abiCall("peer()"):
					if broken && id == 10 {
						peer = setupWallet
					}
					value = "0x" + abiAddress(peer)
				case abiCall("isSuckerOf(uint256,address)"):
					value = "0x" + abiN(1)
				case abiCall("projectId()"):
					if to == setupHook || to == bridge {
						value = "0x" + abiN(pid)
					} else {
						return fmt.Errorf("unexpected project target")
					}
				default:
					return base(ctx, id, method, params, out)
				}
				raw, _ := json.Marshal(value)
				return json.Unmarshal(raw, out)
			}
			chains, e := ResolveProject(t.Context(), rpc, 1, "42", 1)
			if broken {
				if e == nil {
					t.Fatal("accepted unrelated peer")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if len(chains) != 2 || chains[0].Connection.State.ProjectID != "42" || chains[1].Connection.State.ProjectID != "73" {
				t.Fatal("did not resolve differing peer project IDs", chains)
			}
		})
	}
}
