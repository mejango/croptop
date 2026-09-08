package ipfs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ipfs/boxo/ipns"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/go-cid"
	doh "github.com/libp2p/go-doh-resolver"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
)

const (
	ipnsLifetime = 7200 * time.Hour
	ipnsTTL      = time.Minute
	ensDoH       = "https://dns.eth.limo/dns-query"
)

// siteKey loads a site's ed25519 key from the shared keystore as a libp2p key.
func (e *Embedded) siteKey(name string) (crypto.PrivKey, ipns.Name, error) {
	priv, err := e.Keystore().private(name)
	if err != nil {
		return nil, ipns.Name{}, err
	}
	sk, err := crypto.UnmarshalEd25519PrivateKey(priv)
	if err != nil {
		return nil, ipns.Name{}, err
	}
	pid, err := peer.IDFromPrivateKey(sk)
	if err != nil {
		return nil, ipns.Name{}, err
	}
	return sk, ipns.NameFromPeer(pid), nil
}

// NamePublish signs an IPNS record at an explicit sequence and puts it in the DHT.
func (e *Embedded) NamePublish(ctx context.Context, key, c string, seq uint64) error {
	if e.dht == nil {
		return fmt.Errorf("node not started")
	}
	sk, name, err := e.siteKey(key)
	if err != nil {
		return err
	}
	id, err := cid.Decode(c)
	if err != nil {
		return err
	}
	rec, err := ipns.NewRecord(sk, path.FromCid(id), seq, time.Now().Add(ipnsLifetime), ipnsTTL)
	if err != nil {
		return err
	}
	b, err := ipns.MarshalRecord(rec)
	if err != nil {
		return err
	}
	if err := e.dht.PutValue(ctx, string(name.RoutingKey()), b); err != nil {
		return fmt.Errorf("ipns put: %w", err)
	}
	go func() {
		pctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := e.dht.Provide(pctx, id, true); err != nil {
			e.Log("provide " + c + ": " + err.Error())
		}
	}()
	return nil
}

// NetworkRecord searches the DHT for the record with the highest sequence.
func (e *Embedded) NetworkRecord(ctx context.Context, nameStr string) (*Record, error) {
	if e.dht == nil {
		return nil, fmt.Errorf("node not started")
	}
	name, err := ipns.NameFromString(strings.TrimPrefix(nameStr, "/ipns/"))
	if err != nil {
		return nil, err
	}
	if !e.Offline {
		e.waitForRoutingTable(ctx, 20*time.Second)
	}
	sctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	ch, err := e.dht.SearchValue(sctx, string(name.RoutingKey()))
	if err != nil {
		if errors.Is(err, routing.ErrNotFound) {
			return nil, ErrNoRecord
		}
		return nil, err
	}
	var best *Record
	for b := range ch {
		rec, err := ipns.UnmarshalRecord(b)
		if err != nil {
			continue
		}
		seq, err := rec.Sequence()
		if err != nil {
			continue
		}
		if best == nil || seq > best.Sequence {
			value, err := rec.Value()
			if err != nil {
				continue
			}
			r := &Record{Value: value.String(), Sequence: seq}
			r.Validity, _ = rec.Validity()
			best = r
		}
	}
	if best == nil {
		return nil, ErrNoRecord
	}
	return best, nil
}

// Resolve resolves one step: an ENS name through DNSLink over DoH, or an
// IPNS name through the DHT.
func (e *Embedded) Resolve(ctx context.Context, p string) (string, error) {
	name := strings.TrimPrefix(strings.TrimPrefix(p, "/ipns/"), "ipns/")
	if strings.HasSuffix(name, ".eth") || strings.Contains(name, ".") && !strings.HasPrefix(name, "k51") {
		return resolveDNSLink(ctx, name)
	}
	rec, err := e.NetworkRecord(ctx, name)
	if err != nil {
		return "", err
	}
	return rec.Value, nil
}

func resolveDNSLink(ctx context.Context, name string) (string, error) {
	r, err := doh.NewResolver(ensDoH)
	if err != nil {
		return "", err
	}
	rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	txts, err := r.LookupTXT(rctx, "_dnslink."+name)
	if err != nil {
		return "", fmt.Errorf("dnslink %s: %w", name, err)
	}
	if v, ok := parseDNSLink(txts); ok {
		return v, nil
	}
	return "", fmt.Errorf("%s has no dnslink record", name)
}

func parseDNSLink(txts []string) (string, bool) {
	for _, t := range txts {
		t = strings.Trim(t, `"`)
		if strings.HasPrefix(t, "dnslink=") {
			v := strings.TrimPrefix(t, "dnslink=")
			if strings.HasPrefix(v, "/ipns/") || strings.HasPrefix(v, "/ipfs/") {
				return v, true
			}
		}
	}
	return "", false
}
