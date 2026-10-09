package shop

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const Directory = "0x5aff29060e023e6fb87be5596652b33c65af535b"
const SuckerRegistry = "0x7903a854ae91eaf635430d120a1a434085cef297"

// ResolveProject follows registered, reciprocal bridge peers. A matching address
// or project number alone is never evidence that two chains host the same project.
func ResolveProject(ctx context.Context, rpc RPC, chain int, project string, category uint32) ([]*BundleChain, error) {
	if _, ok := NetworkFor(chain); !ok {
		return nil, errors.New("choose a supported network")
	}
	pid, ok := new(big.Int).SetString(project, 10)
	if !ok || pid.Sign() <= 0 || pid.BitLen() > 64 {
		return nil, errors.New("enter a positive project ID")
	}
	type target struct {
		chain   int
		project string
	}
	queue := []target{{chain, pid.String()}}
	seen := map[int]string{}
	result := []*BundleChain{}
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		if old, ok := seen[t.chain]; ok {
			if old != t.project {
				return nil, errors.New("linked projects conflict on the same network")
			}
			continue
		}
		if len(seen) >= 4 {
			return nil, errors.New("too many linked networks")
		}
		seen[t.chain] = t.project
		n, _ := NetworkFor(t.chain)
		id, _ := new(big.Int).SetString(t.project, 10)
		c := &Connection{ChainID: t.chain, Category: category}
		addressCall := func(to, signature string, args ...string) (string, error) {
			w, e := rpcWords(ctx, rpc, t.chain, to, abiCall(signature, args...), "latest")
			if e != nil {
				return "", e
			}
			a, ok := wordAddress(w[0])
			if !ok {
				return "", errors.New("project contract is unavailable")
			}
			return strings.ToLower(a), nil
		}
		controller, e := addressCall(Directory, "controllerOf(uint256)", abiUint(id))
		if e != nil {
			return nil, fmt.Errorf("%s project %s: could not find its controller", n.Label, t.project)
		}
		words, e := rpcWords(ctx, rpc, t.chain, controller, abiCall("currentRulesetOf(uint256)", abiUint(id)), "latest")
		if e != nil || len(words) < 9 {
			return nil, fmt.Errorf("%s: could not read the project's current ruleset", n.Label)
		}
		metadata := wordN(words[8])
		metadata.Rsh(metadata, 82)
		metadata.And(metadata, new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 160), big.NewInt(1)))
		hook := fmt.Sprintf("0x%040x", metadata)
		if hook == RevnetOwner {
			hook, e = addressCall(RevnetOwner, "tiered721HookOf(uint256)", abiUint(id))
			if e != nil {
				return nil, fmt.Errorf("%s: project has no NFT shop", n.Label)
			}
		}
		if !Address(hook) {
			return nil, fmt.Errorf("%s: project has no NFT shop", n.Label)
		}
		c.Hook = hook
		state, e := ReadConnection(ctx, rpc, c, "", "latest")
		if e != nil {
			return nil, fmt.Errorf("%s project %s: could not resolve a supported NFT shop: %w", n.Label, t.project, e)
		}
		if state.ProjectID != t.project {
			return nil, errors.New("the shop does not belong to the selected project")
		}
		c.State = state
		result = append(result, &BundleChain{Connection: c, Requests: []ForwardRequest{}, Status: "Shop found"})
		words, e = rpcWords(ctx, rpc, t.chain, SuckerRegistry, abiCall("suckersOf(uint256)", abiUint(id)), "latest")
		if e != nil || len(words) < 2 || wordN(words[0]).Cmp(big.NewInt(32)) != 0 {
			return nil, fmt.Errorf("%s: could not resolve linked networks", n.Label)
		}
		count, e := checkedUint(words[1], 8)
		if e != nil || count > 32 || len(words) != int(count)+2 {
			return nil, errors.New("invalid linked-network list")
		}
		for _, word := range words[2:] {
			sucker, ok := wordAddress(word)
			if !ok {
				return nil, errors.New("invalid project bridge")
			}
			remote, e := rpcWords(ctx, rpc, t.chain, sucker, abiCall("peerChainId()"), "latest")
			if e != nil {
				return nil, e
			}
			remoteID, e := checkedUint(remote[0], 32)
			if e != nil {
				return nil, e
			}
			if _, ok := NetworkFor(int(remoteID)); !ok || Testnet(int(remoteID)) != Testnet(chain) {
				return nil, fmt.Errorf("the project links to unsupported network %d", remoteID)
			}
			peer, e := addressCall(sucker, "peer()")
			if e != nil {
				return nil, e
			}
			remote, e = rpcWords(ctx, rpc, int(remoteID), peer, abiCall("projectId()"), "latest")
			if e != nil {
				return nil, e
			}
			remoteProject := wordN(remote[0])
			if remoteProject.Sign() == 0 || remoteProject.BitLen() > 64 {
				return nil, errors.New("invalid linked project")
			}
			registered, e := rpcWords(ctx, rpc, int(remoteID), SuckerRegistry, abiCall("isSuckerOf(uint256,address)", abiUint(remoteProject), abiAddress(peer)), "latest")
			if e != nil || wordN(registered[0]).Cmp(big.NewInt(1)) != 0 {
				return nil, errors.New("linked bridge is not registered to its project")
			}
			back, e := rpcWords(ctx, rpc, int(remoteID), peer, abiCall("peer()"), "latest")
			if e != nil || !strings.EqualFold(back[0], abiAddress(sucker)) {
				return nil, errors.New("project bridge does not have a reciprocal peer")
			}
			back, e = rpcWords(ctx, rpc, int(remoteID), peer, abiCall("peerChainId()"), "latest")
			if e != nil || wordN(back[0]).Cmp(big.NewInt(int64(t.chain))) != 0 {
				return nil, errors.New("project bridge points to a different network")
			}
			queue = append(queue, target{int(remoteID), remoteProject.String()})
		}
	}
	return result, nil
}
