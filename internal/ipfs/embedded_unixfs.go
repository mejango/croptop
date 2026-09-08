package ipfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ipfs/boxo/blockservice"
	"github.com/ipfs/boxo/blockstore"
	chunker "github.com/ipfs/boxo/chunker"
	"github.com/ipfs/boxo/exchange/offline"
	"github.com/ipfs/boxo/files"
	"github.com/ipfs/boxo/ipld/merkledag"
	unixfile "github.com/ipfs/boxo/ipld/unixfs/file"
	"github.com/ipfs/boxo/ipld/unixfs/importer/balanced"
	uih "github.com/ipfs/boxo/ipld/unixfs/importer/helpers"
	uio "github.com/ipfs/boxo/ipld/unixfs/io"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/multiformats/go-multicodec"
)

// These match kubo's defaults, which Planet relied on: 256 KiB chunks,
// balanced layout, 174 links per node. CIDv0 adds use protobuf leaves and
// the v0 builder; CIDv1 adds use raw leaves and dag-pb v1.
const chunkSize = 256 * 1024

var (
	v0Builder = cid.V0Builder{}
	v1Builder = cid.V1Builder{Codec: uint64(multicodec.DagPb), MhType: uint64(multicodec.Sha2_256), MhLength: -1}
)

// hashOnlyDAG stores nothing; it exists to compute CIDs.
func hashOnlyDAG() ipld.DAGService {
	bs := blockstore.NewBlockstore(dssync.MutexWrap(datastore.NewNullDatastore()))
	return merkledag.NewDAGService(blockservice.New(bs, offline.Exchange(bs)))
}

// memoryDAG keeps blocks in memory; used by tests and hash-only directory adds.
func memoryDAG() ipld.DAGService {
	bs := blockstore.NewBlockstore(dssync.MutexWrap(datastore.NewMapDatastore()))
	return merkledag.NewDAGService(blockservice.New(bs, offline.Exchange(bs)))
}

func addFile(ctx context.Context, dag ipld.DAGService, path string, rawLeaves bool, builder cid.Builder) (ipld.Node, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	params := uih.DagBuilderParams{
		Maxlinks:   uih.DefaultLinksPerBlock,
		RawLeaves:  rawLeaves,
		CidBuilder: builder,
		Dagserv:    dag,
	}
	db, err := params.New(chunker.NewSizeSplitter(f, chunkSize))
	if err != nil {
		return nil, err
	}
	return balanced.Layout(db)
}

// addDir builds a unixfs directory for dir with kubo's `add -r -H
// --cid-version=1` semantics: hidden files included, raw leaves, HAMT
// sharding above 256 KiB of links.
func addDir(ctx context.Context, dag ipld.DAGService, dir string) (ipld.Node, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	d, err := uio.NewDirectory(dag)
	if err != nil {
		return nil, err
	}
	d.SetCidBuilder(v1Builder)
	for _, ent := range entries {
		p := filepath.Join(dir, ent.Name())
		var nd ipld.Node
		switch {
		case ent.Type()&os.ModeSymlink != 0:
			continue // kubo would add a symlink node; sites never contain them
		case ent.IsDir():
			nd, err = addDir(ctx, dag, p)
		default:
			nd, err = addFile(ctx, dag, p, true, v1Builder)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if err := d.AddChild(ctx, ent.Name(), nd); err != nil {
			return nil, err
		}
	}
	nd, err := d.GetNode()
	if err != nil {
		return nil, err
	}
	if err := dag.Add(ctx, nd); err != nil {
		return nil, err
	}
	return nd, nil
}

// FileCIDv0 hashes one file the way Planet's `ipfs add --only-hash --cid-version=0` does.
func (e *Embedded) FileCIDv0(ctx context.Context, path string) (string, error) {
	return fileCIDv0(ctx, path)
}

func fileCIDv0(ctx context.Context, path string) (string, error) {
	nd, err := addFile(ctx, hashOnlyDAG(), path, false, v0Builder)
	if err != nil {
		return "", err
	}
	return nd.Cid().String(), nil
}

// AddDir adds a rendered site to the local blockstore and returns its root.
func (e *Embedded) AddDir(ctx context.Context, dir string) (string, error) {
	if e.dag == nil {
		return "", fmt.Errorf("node not started")
	}
	nd, err := addDir(ctx, e.dag, dir)
	if err != nil {
		return "", err
	}
	return nd.Cid().String(), nil
}

// Get fetches /ipfs/<cid>[/path] into dest, files or directories.
func (e *Embedded) Get(ctx context.Context, ipfsPath, dest string) error {
	if e.dag == nil {
		return fmt.Errorf("node not started")
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(ipfsPath, "/ipfs/"), "ipfs/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	root, err := cid.Decode(parts[0])
	if err != nil {
		return fmt.Errorf("%s: %w", ipfsPath, err)
	}
	session := merkledag.NewReadOnlyDagService(merkledag.NewSession(ctx, e.dag))
	nd, err := session.Get(ctx, root)
	if err != nil {
		return err
	}
	for _, seg := range parts[1:] {
		if seg == "" {
			continue
		}
		link, _, err := nd.ResolveLink([]string{seg})
		if err != nil {
			return fmt.Errorf("%s: %w", seg, err)
		}
		if nd, err = link.GetNode(ctx, session); err != nil {
			return err
		}
	}
	fnode, err := unixfile.NewUnixfsFile(ctx, session, nd)
	if err != nil {
		return err
	}
	defer fnode.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return files.WriteTo(fnode, dest)
}
