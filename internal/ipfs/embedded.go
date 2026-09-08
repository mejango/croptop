package ipfs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/boxo/bitswap"
	bsnet "github.com/ipfs/boxo/bitswap/network/bsnet"
	"github.com/ipfs/boxo/blockservice"
	"github.com/ipfs/boxo/blockstore"
	"github.com/ipfs/boxo/ipld/merkledag"
	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/mount"
	dssync "github.com/ipfs/go-datastore/sync"
	flatfs "github.com/ipfs/go-ds-flatfs"
	leveldb "github.com/ipfs/go-ds-leveldb"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/multiformats/go-multiaddr"
)

const EmbeddedVersion = "boxo v0.42.2"

// Embedded is an IPFS node inside the croptop process, built on boxo.
// Blocks live in flatfs under <data>/node/blocks, DHT and provider state
// in leveldb under <data>/node/datastore. Site keys stay in the shared
// keystore under <data>/ipfs/keystore so both engines see them.
type Embedded struct {
	DataDir string
	Log     func(string)
	// Offline skips bootstrap and peering and listens on loopback only (tests).
	Offline bool

	mu      sync.Mutex
	running bool
	lastErr string
	cancel  context.CancelFunc

	host   host.Host
	dht    *dht.IpfsDHT
	ds     datastore.Batching
	blocks datastore.Batching
	mds    datastore.Batching
	bstore blockstore.Blockstore
	bswap  *bitswap.Bitswap
	bserv  blockservice.BlockService
	dag    ipld.DAGService
	port   int
}

func NewEmbedded(dataDir string) *Embedded {
	return &Embedded{DataDir: dataDir, Log: func(string) {}}
}

func (e *Embedded) nodeDir() string { return filepath.Join(e.DataDir, "node") }
func (e *Embedded) Keystore() *Keystore {
	return &Keystore{Dir: filepath.Join(e.DataDir, "ipfs", "keystore")}
}

func (e *Embedded) identity() (crypto.PrivKey, error) {
	path := filepath.Join(e.nodeDir(), "identity.key")
	if b, err := os.ReadFile(path); err == nil {
		return crypto.UnmarshalPrivateKey(b)
	}
	priv, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		return nil, err
	}
	b, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(e.nodeDir(), 0o700); err != nil {
		return nil, err
	}
	return priv, os.WriteFile(path, b, 0o600)
}

func (e *Embedded) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return nil
	}
	if err := os.MkdirAll(e.nodeDir(), 0o755); err != nil {
		return err
	}
	priv, err := e.identity()
	if err != nil {
		return fmt.Errorf("node identity: %w", err)
	}
	lds, err := leveldb.NewDatastore(filepath.Join(e.nodeDir(), "datastore"), nil)
	if err != nil {
		return fmt.Errorf("datastore: %w", err)
	}
	shard, _ := flatfs.ParseShardFunc("/repo/flatfs/shard/v1/next-to-last/2")
	fds, err := flatfs.CreateOrOpen(filepath.Join(e.nodeDir(), "blocks"), shard, false)
	if err != nil {
		lds.Close()
		return fmt.Errorf("blocks: %w", err)
	}
	// kubo's layout: /blocks on flatfs, everything else in leveldb
	mds := mount.New([]mount.Mount{
		{Prefix: datastore.NewKey("/blocks"), Datastore: fds},
		{Prefix: datastore.NewKey("/"), Datastore: lds},
	})
	e.ds, e.blocks = lds, fds
	e.mds = dssync.MutexWrap(mds)
	e.bstore = blockstore.NewBlockstore(e.mds)

	if e.port, err = freePort(swarmPortLow, swarmPortHigh); err != nil {
		return err
	}
	listen := []string{
		fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", e.port), fmt.Sprintf("/ip6/::/tcp/%d", e.port),
		fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", e.port), fmt.Sprintf("/ip6/::/udp/%d/quic-v1", e.port),
	}
	if e.Offline {
		listen = []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", e.port)}
	}
	cm, err := connmgr.NewConnManager(64, 192, connmgr.WithGracePeriod(20*time.Second))
	if err != nil {
		return err
	}
	opts := []libp2p.Option{
		libp2p.Identity(priv),
		libp2p.ListenAddrStrings(listen...),
		libp2p.ConnectionManager(cm),
	}
	if !e.Offline {
		opts = append(opts, libp2p.NATPortMap(), libp2p.EnableHolePunching(), libp2p.EnableNATService(),
			libp2p.EnableAutoRelayWithPeerSource(e.relaySource, autorelay.WithMinInterval(30*time.Second)))
	}
	h, err := libp2p.New(opts...)
	if err != nil {
		return fmt.Errorf("libp2p: %w", err)
	}
	e.host = h

	dopts := []dht.Option{
		dht.Mode(dht.ModeAuto),
		dht.NamespacedValidator("ipns", ipns.Validator{KeyBook: h.Peerstore()}),
		dht.Datastore(e.mds),
	}
	if e.Offline {
		dopts = append(dopts, dht.BootstrapPeers(), dht.Mode(dht.ModeServer))
	} else {
		dopts = append(dopts, dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...))
	}
	d, err := dht.New(h, dopts...)
	if err != nil {
		h.Close()
		return fmt.Errorf("dht: %w", err)
	}
	e.dht = d

	runCtx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	net := bsnet.NewFromIpfsHost(h)
	e.bswap = bitswap.New(runCtx, net, d, e.bstore)
	e.bserv = blockservice.New(e.bstore, e.bswap)
	e.dag = merkledag.NewDAGService(e.bserv)

	if !e.Offline {
		for _, ai := range dht.GetDefaultBootstrapPeerAddrInfos() {
			go func(ai peer.AddrInfo) {
				c, cancel := context.WithTimeout(runCtx, 30*time.Second)
				defer cancel()
				h.Connect(c, ai)
			}(ai)
		}
		if err := d.Bootstrap(runCtx); err != nil {
			e.Log("dht bootstrap: " + err.Error())
		}
		go e.peer(runCtx)
		go func() {
			for _, wait := range []time.Duration{30 * time.Second, 2 * time.Minute} {
				select {
				case <-runCtx.Done():
					return
				case <-time.After(wait):
				}
				var addrs []string
				for _, a := range h.Addrs() {
					addrs = append(addrs, a.String())
				}
				e.Log(fmt.Sprintf("addresses: %s", strings.Join(addrs, " ")))
			}
		}()
	}
	e.running = true
	e.Log(fmt.Sprintf("embedded ipfs node %s on port %d", h.ID(), e.port))
	if !e.Offline {
		e.waitForRoutingTable(ctx, 45*time.Second)
	}
	return nil
}

// waitForRoutingTable blocks until the DHT knows some peers, so the first
// IPNS query does not come back "not found" from an empty table.
func (e *Embedded) waitForRoutingTable(ctx context.Context, max time.Duration) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if n := e.dht.RoutingTable().Size(); n >= 4 {
			e.Log(fmt.Sprintf("dht routing table has %d peers", n))
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	e.Log(fmt.Sprintf("dht routing table still small (%d peers) after %s", e.dht.RoutingTable().Size(), max))
}

// relaySource offers connected peers that run the circuit relay v2 hop
// protocol, so a node behind NAT gets a relayed address and hole punching.
func (e *Embedded) relaySource(ctx context.Context, num int) <-chan peer.AddrInfo {
	out := make(chan peer.AddrInfo)
	go func() {
		defer close(out)
		if e.host == nil {
			return
		}
		for _, p := range e.host.Network().Peers() {
			if num <= 0 {
				return
			}
			ok, err := e.host.Peerstore().SupportsProtocols(p, "/libp2p/circuit/relay/0.2.0/hop")
			if err != nil || len(ok) == 0 {
				continue
			}
			select {
			case out <- e.host.Peerstore().PeerInfo(p):
				num--
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// peer keeps connections to the content providers Planet peers with, and
// to any IPFS node on this machine, retrying in the background.
func (e *Embedded) peer(ctx context.Context) {
	var infos []peer.AddrInfo
	for _, p := range peers {
		id, err := peer.Decode(p["ID"].(string))
		if err != nil {
			continue
		}
		info := peer.AddrInfo{ID: id}
		for _, a := range p["Addrs"].([]string) {
			if m, err := multiaddr.NewMultiaddr(a); err == nil {
				info.Addrs = append(info.Addrs, m)
			}
		}
		infos = append(infos, info)
	}
	for {
		for _, info := range infos {
			if e.host.Network().Connectedness(info.ID) == 1 {
				continue
			}
			c, cancel := context.WithTimeout(ctx, 20*time.Second)
			e.host.Connect(c, info)
			cancel()
		}
		e.ConnectLocalNodes(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Minute):
		}
	}
}

// ConnectLocalNodes dials other kubo or croptop nodes on this machine by
// asking the kubo API ports for their identity.
func (e *Embedded) ConnectLocalNodes(ctx context.Context) int {
	if e.host == nil {
		return 0
	}
	connected := 0
	for p := apiPortLow; p <= apiPortHigh; p++ {
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		req, _ := http.NewRequestWithContext(c, "POST", fmt.Sprintf("http://127.0.0.1:%d/api/v0/id", p), nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			continue
		}
		var id struct {
			ID        string   `json:"ID"`
			Addresses []string `json:"Addresses"`
		}
		json.NewDecoder(resp.Body).Decode(&id)
		resp.Body.Close()
		pid, err := peer.Decode(id.ID)
		if err != nil || pid == e.host.ID() {
			continue
		}
		info := peer.AddrInfo{ID: pid}
		for _, a := range id.Addresses {
			if strings.HasPrefix(a, "/ip4/127.0.0.1/") {
				if m, err := multiaddr.NewMultiaddr(strings.TrimSuffix(a, "/p2p/"+id.ID)); err == nil {
					info.Addrs = append(info.Addrs, m)
				}
			}
		}
		if len(info.Addrs) == 0 {
			continue
		}
		c, cancel = context.WithTimeout(ctx, 10*time.Second)
		if err := e.host.Connect(c, info); err == nil {
			e.Log("peered with local ipfs node " + id.ID)
			connected++
		}
		cancel()
	}
	return connected
}

func (e *Embedded) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return nil
	}
	e.running = false
	if e.cancel != nil {
		e.cancel()
	}
	if e.bswap != nil {
		e.bswap.Close()
	}
	if e.dht != nil {
		e.dht.Close()
	}
	if e.host != nil {
		e.host.Close()
	}
	if e.blocks != nil {
		e.blocks.Close()
	}
	if e.ds != nil {
		e.ds.Close()
	}
	return nil
}

func (e *Embedded) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

func (e *Embedded) LastError() string { return e.lastErr }

func (e *Embedded) Info(ctx context.Context) (Info, error) {
	if e.host == nil {
		return Info{Version: EmbeddedVersion}, fmt.Errorf("node not started")
	}
	return Info{PeerID: e.host.ID().String(), Version: "embedded " + EmbeddedVersion, Peers: len(e.host.Network().Peers())}, nil
}

// Provide announces a CID to the DHT so other nodes can find this one.
func (e *Embedded) Provide(ctx context.Context, c string) error {
	id, err := cid.Decode(c)
	if err != nil {
		return err
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return e.dht.Provide(pctx, id, true)
}
