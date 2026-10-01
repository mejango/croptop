package ipfs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// DefaultPeersURL lists the peers crop.top runs, for joining the network now
// that the public bootstrap nodes are gone.
const DefaultPeersURL = "https://crop.top/v0/host/peers"

type savedPeer struct {
	ID    string   `json:"id"`
	Addrs []string `json:"addrs"`
}

func (e *Embedded) peersFile() string { return filepath.Join(e.nodeDir(), "peers.json") }

func addrInfos(in []savedPeer) []peer.AddrInfo {
	var out []peer.AddrInfo
	for _, sp := range in {
		id, err := peer.Decode(sp.ID)
		if err != nil {
			continue
		}
		ai := peer.AddrInfo{ID: id}
		for _, a := range sp.Addrs {
			if m, err := ma.NewMultiaddr(a); err == nil {
				ai.Addrs = append(ai.Addrs, m)
			}
		}
		if len(ai.Addrs) > 0 {
			out = append(out, ai)
		}
	}
	return out
}

// peeringInfos is the fixed peering list (repo.go) as dialable peers.
func peeringInfos() []peer.AddrInfo {
	var in []savedPeer
	for _, p := range peers {
		in = append(in, savedPeer{ID: p["ID"].(string), Addrs: p["Addrs"].([]string)})
	}
	return addrInfos(in)
}

// loadPeers reads the peers savePeers remembered. A missing or damaged file
// is no peers.
func (e *Embedded) loadPeers() []peer.AddrInfo {
	b, err := os.ReadFile(e.peersFile())
	if err != nil {
		return nil
	}
	var in []savedPeer
	if json.Unmarshal(b, &in) != nil {
		return nil
	}
	return addrInfos(in)
}

// savePeers remembers up to 64 connected peers with public addresses, so the
// next start can rejoin the network without any bootstrap peer. An empty list
// keeps the last good one.
func (e *Embedded) savePeers() {
	if e.host == nil {
		return
	}
	var out []savedPeer
	for _, p := range e.host.Network().Peers() {
		sp := savedPeer{ID: p.String()}
		for _, a := range e.host.Peerstore().Addrs(p) {
			if manet.IsPublicAddr(a) {
				sp.Addrs = append(sp.Addrs, a.String())
			}
		}
		if len(sp.Addrs) > 0 {
			out = append(out, sp)
		}
		if len(out) == 64 {
			break
		}
	}
	if len(out) == 0 {
		return
	}
	b, _ := json.Marshal(out)
	// a temp file of its own: Stop and the periodic save can overlap
	f, err := os.CreateTemp(filepath.Dir(e.peersFile()), "peers-*.json")
	if err != nil {
		e.Log("save peers: " + err.Error())
		return
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), e.peersFile())
	}
	if err != nil { // the last good file stays, and no temp file is left behind
		os.Remove(f.Name())
		e.Log("save peers: " + err.Error())
	}
}

// hostPeers asks PeersURL for the peers a host runs. It gives up after 5 s;
// Start calls it in a goroutine so an unreachable host never delays a start.
func (e *Embedded) hostPeers(ctx context.Context) []peer.AddrInfo {
	if e.PeersURL == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.PeersURL, nil)
	if err != nil {
		e.Log("host peers: " + err.Error())
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.Log("host peers: " + err.Error())
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.Log("host peers: " + resp.Status)
		return nil
	}
	var sp savedPeer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&sp); err != nil {
		e.Log("host peers: " + err.Error())
		return nil
	}
	infos := addrInfos([]savedPeer{sp})
	if len(infos) == 0 { // valid JSON of another shape decodes to an empty peer
		e.Log("host peers: no dialable peer in the response")
	}
	return infos
}

// PeerAddrs are the addresses other nodes should dial to reach this one: the
// announced ones when set, else the public ones libp2p knows.
func (e *Embedded) PeerAddrs() []string {
	if e.host == nil {
		return nil
	}
	var out []string
	for _, a := range e.host.Addrs() {
		if len(e.Announce) > 0 || manet.IsPublicAddr(a) {
			out = append(out, a.String())
		}
	}
	return out
}

// Addrs is every address this node listens on, loopback included, each ending
// in its peer ID: what another node on this machine dials.
func (e *Embedded) Addrs() []string {
	if e.host == nil {
		return nil
	}
	var out []string
	for _, a := range e.host.Addrs() {
		out = append(out, a.String()+"/p2p/"+e.host.ID().String())
	}
	return out
}

// Dial connects to the peers at addrs (each ending /p2p/<id>), trying all of a
// peer's addresses, and returns the first peer it could not reach.
func (e *Embedded) Dial(ctx context.Context, addrs []string) error {
	if e.host == nil {
		return errors.New("node not started")
	}
	var ms []ma.Multiaddr
	for _, a := range addrs {
		m, err := ma.NewMultiaddr(a)
		if err != nil {
			return err
		}
		ms = append(ms, m)
	}
	infos, err := peer.AddrInfosFromP2pAddrs(ms...)
	if err != nil {
		return err
	}
	for _, ai := range infos {
		if err := e.host.Connect(ctx, ai); err != nil {
			return err
		}
	}
	return nil
}
