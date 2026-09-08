package ipfs

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// Port ranges match the Mac app so both can coexist on one machine.
const (
	apiPortLow, apiPortHigh         = 5981, 5991
	gatewayPortLow, gatewayPortHigh = 18181, 18191
	swarmPortLow, swarmPortHigh     = 4001, 4011
)

// Same content-provider peers Planet configures (docs.ipfs.tech peering list).
var peers = []map[string]any{
	{"ID": "12D3KooWBJY6ZVV8Tk8UDDFMEqWoxn89Xc8wnpm8uBFSR3ijDkui", "Addrs": []string{ // Pinnable
		"/ip4/167.71.172.216/tcp/4001", "/ip6/2604:a880:800:10::826:1/tcp/4001",
		"/ip4/167.71.172.216/udp/4001/quic-v1", "/ip6/2604:a880:800:10::826:1/udp/4001/quic-v1"}},
	{"ID": "12D3KooWJ6MTkNM8Bu8DzNiRm1GY3Wqh8U8Pp1zRWap6xY3MvsNw", "Addrs": []string{"/dnsaddr/node-1.ipfs.bit.site"}},      // bit.site
	{"ID": "12D3KooWQ85aSCFwFkByr5e3pUCQeuheVhobVxGSSs1DrRQHGv1t", "Addrs": []string{"/dnsaddr/node-1.ipfs.4everland.net"}}, // 4everland
	{"ID": "12D3KooWGtYkBAaqJMJEmywMxaCiNP7LCEFUAFiLEBASe232c2VH", "Addrs": []string{"/dns4/bitswap.filebase.io/tcp/443/wss"}},
}

var resolvers = map[string]string{
	"eth.": "https://dns.eth.limo/dns-query",
	"bit.": "https://dweb-dns.v2ex.pro/dns-query",
	"sol.": "https://dweb-dns.v2ex.pro/dns-query",
	"fc.":  "https://dweb-dns.v2ex.pro/dns-query",
}

func freePort(low, high int) (int, error) {
	for p := low; p <= high; p++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			l.Close()
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port in %d-%d", low, high)
}

// Init creates the repo when missing and (re)applies our config every time,
// since ports are chosen per run.
func (n *Node) Init(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(n.RepoPath, "config")); os.IsNotExist(err) {
		if err := os.MkdirAll(n.RepoPath, 0o755); err != nil {
			return err
		}
		if _, err := n.Run(ctx, "init"); err != nil {
			return err
		}
	}
	var err error
	if n.APIPort, err = freePort(apiPortLow, apiPortHigh); err != nil {
		return err
	}
	if n.GatewayPort, err = freePort(gatewayPortLow, gatewayPortHigh); err != nil {
		return err
	}
	if n.SwarmPort, err = freePort(swarmPortLow, swarmPortHigh); err != nil {
		return err
	}
	set := func(key string, v any) error {
		b, _ := json.Marshal(v)
		_, err := n.Run(ctx, "config", "--json", key, string(b))
		return err
	}
	s := n.SwarmPort
	steps := []struct {
		key string
		val any
	}{
		{"Addresses.API", fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", n.APIPort)},
		{"Addresses.Gateway", fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", n.GatewayPort)},
		{"Addresses.Swarm", []string{
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", s), fmt.Sprintf("/ip6/::/tcp/%d", s),
			fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", s), fmt.Sprintf("/ip6/::/udp/%d/quic-v1", s)}},
		{"Swarm.ConnMgr", map[string]any{"Type": "basic", "LowWater": 10, "HighWater": 20, "GracePeriod": "20s"}},
		{"Peering.Peers", peers},
		{"DNS.Resolvers", resolvers},
		{"Ipns.UsePubsub", true},
	}
	for _, st := range steps {
		if err := set(st.key, st.val); err != nil {
			return err
		}
	}
	return nil
}
