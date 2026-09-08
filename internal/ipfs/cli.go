package ipfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// decodeJSON reads the first JSON value from kubo output (some commands
// stream several).
func decodeJSON(out []byte, v any) error {
	return json.NewDecoder(bytes.NewReader(out)).Decode(v)
}

// FileCIDv0 hashes one file the way Planet does for attachment CIDs.
func (n *Node) FileCIDv0(ctx context.Context, path string) (string, error) {
	out, err := n.Run(ctx, "add", "--only-hash", "--cid-version=0", "-Q", path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// AddDir adds a rendered site and returns its CIDv1 root.
func (n *Node) AddDir(ctx context.Context, dir string) (string, error) {
	out, err := n.Run(ctx, "add", "-r", "-H", "--cid-version=1", "-Q", dir)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type Record struct {
	Value    string
	Sequence uint64
	Validity time.Time
}

var ErrNoRecord = errors.New("no IPNS record found")

// NetworkRecord fetches the best IPNS record the network has for name.
func (n *Node) NetworkRecord(ctx context.Context, name string) (*Record, error) {
	raw, err := n.Run(ctx, "name", "get", name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "could not resolve") || strings.Contains(err.Error(), "routing: not found") {
			return nil, ErrNoRecord
		}
		return nil, err
	}
	if len(raw) == 0 {
		return nil, ErrNoRecord
	}
	f, err := os.CreateTemp("", "ipns-record-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	f.Write(raw)
	f.Close()
	var res struct {
		Entry struct {
			Value    string `json:"Value"`
			Sequence uint64 `json:"Sequence"`
			Validity string `json:"Validity"`
		} `json:"Entry"`
	}
	if err := n.RunJSON(ctx, &res, "name", "inspect", f.Name()); err != nil {
		return nil, err
	}
	rec := &Record{Value: res.Entry.Value, Sequence: res.Entry.Sequence}
	rec.Validity, _ = time.Parse(time.RFC3339Nano, res.Entry.Validity)
	return rec, nil
}

// NamePublish publishes cid under the key named key at an explicit sequence.
func (n *Node) NamePublish(ctx context.Context, key, cid string, seq uint64) error {
	_, err := n.Run(ctx, "name", "publish", "--key="+key, "--allow-offline",
		"--lifetime=7200h", "--ttl=1m", fmt.Sprintf("--sequence=%d", seq), "-Q", "/ipfs/"+cid)
	return err
}

// Resolve resolves /ipns/<name-or-ens> one step (ENS -> IPNS or IPNS -> IPFS path).
func (n *Node) Resolve(ctx context.Context, path string) (string, error) {
	var res struct {
		Path string `json:"Path"`
	}
	if err := n.RunJSON(ctx, &res, "resolve", "-r=false", path); err != nil {
		return "", err
	}
	return res.Path, nil
}

// Get downloads an IPFS path into dest.
func (n *Node) Get(ctx context.Context, path, dest string) error {
	_, err := n.Run(ctx, "get", "-o", dest, path)
	return err
}

type Info struct {
	PeerID  string
	Version string
	Peers   int
}

func (n *Node) Info(ctx context.Context) (Info, error) {
	var id struct {
		ID           string `json:"ID"`
		AgentVersion string `json:"AgentVersion"`
	}
	if err := n.RunJSON(ctx, &id, "id"); err != nil {
		return Info{}, err
	}
	var peers struct {
		Peers []any `json:"Peers"`
	}
	n.RunJSON(ctx, &peers, "swarm", "peers")
	return Info{PeerID: id.ID, Version: id.AgentVersion, Peers: len(peers.Peers)}, nil
}
