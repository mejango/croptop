package ipfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	ft "github.com/ipfs/boxo/ipld/unixfs"
	unixfile "github.com/ipfs/boxo/ipld/unixfs/file"
	"github.com/ipfs/boxo/ipld/unixfs/importer/balanced"
	uih "github.com/ipfs/boxo/ipld/unixfs/importer/helpers"
	uio "github.com/ipfs/boxo/ipld/unixfs/io"
	blocks "github.com/ipfs/go-block-format"
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
	d, err := uio.NewDirectory(dag)
	if err != nil {
		return nil, err
	}
	return addInto(ctx, dag, d, dir)
}

// addInto adds dir's entries to d, replacing entries of the same name, and
// stores the result. dag-pb sorts links when it encodes, so the root is the
// same CID an add of the whole merged tree would give.
func addInto(ctx context.Context, dag ipld.DAGService, d uio.Directory, dir string) (ipld.Node, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
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

// AddOver adds dir's top-level files and folders on top of the directory
// base: a name in dir replaces the same name in base, and everything else in
// base is kept by reference. Only base's root block is read, so a post can be
// added to a site without the rest of its files.
func (e *Embedded) AddOver(ctx context.Context, base, dir string) (string, error) {
	d, err := e.dir(ctx, base)
	if err != nil {
		return "", err
	}
	nd, err := addInto(ctx, e.dag, d, dir)
	if err != nil {
		return "", err
	}
	return nd.Cid().String(), nil
}

// Links maps each name directly under the directory c to its CID.
func (e *Embedded) Links(ctx context.Context, c string) (map[string]string, error) {
	d, err := e.dir(ctx, c)
	if err != nil {
		return nil, err
	}
	links, err := d.Links(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(links))
	for _, l := range links {
		out[l.Name] = l.Cid.String()
	}
	return out, nil
}

// FileCID is the CID AddDir gives the file at path.
func (e *Embedded) FileCID(ctx context.Context, path string) (string, error) {
	nd, err := addFile(ctx, hashOnlyDAG(), path, true, v1Builder)
	if err != nil {
		return "", err
	}
	return nd.Cid().String(), nil
}

// Block returns a block this node holds, without asking the network.
func (e *Embedded) Block(ctx context.Context, c string) ([]byte, error) {
	if e.bstore == nil {
		return nil, fmt.Errorf("node not started")
	}
	id, err := cid.Decode(c)
	if err != nil {
		return nil, err
	}
	b, err := e.bstore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return b.RawData(), nil
}

// DirBlocks returns the directory blocks under root that this node holds,
// keyed by CID: all a reader needs to list a version, and small. File data is
// left out, and folders held elsewhere (carried from an earlier version) are
// skipped, never fetched.
func (e *Embedded) DirBlocks(ctx context.Context, root string) (map[string][]byte, error) {
	if e.bstore == nil {
		return nil, fmt.Errorf("node not started")
	}
	id, err := cid.Decode(root)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	var walk func(c cid.Cid)
	walk = func(c cid.Cid) {
		if c.Type() != cid.DagProtobuf || out[c.String()] != nil {
			return // raw leaves are file data
		}
		b, err := e.bstore.Get(ctx, c)
		if err != nil {
			return
		}
		nd, err := merkledag.DecodeProtobuf(b.RawData())
		if err != nil {
			return
		}
		fsn, err := ft.FSNodeFromBytes(nd.Data())
		if err != nil || (fsn.Type() != ft.TDirectory && fsn.Type() != ft.THAMTShard) {
			return
		}
		out[c.String()] = b.RawData()
		for _, l := range nd.Links() {
			walk(l.Cid)
		}
	}
	walk(id)
	return out, nil
}

// PutBlock stores a block fetched some other way than IPFS, such as over
// HTTP from a host, after checking that data hashes to c.
func (e *Embedded) PutBlock(ctx context.Context, c string, data []byte) error {
	if e.bstore == nil {
		return fmt.Errorf("node not started")
	}
	id, err := cid.Decode(c)
	if err != nil {
		return err
	}
	if sum, err := id.Prefix().Sum(data); err != nil || !sum.Equals(id) {
		return fmt.Errorf("block does not hash to %s", c)
	}
	b, err := blocks.NewBlockWithCid(data, id)
	if err != nil {
		return err
	}
	return e.bstore.Put(ctx, b)
}

// ErrBadManifest is what Rebuild's refusals of a manifest on its own terms
// match: a path that climbs out of the version, one the parent does not have,
// paths that sit inside each other or are given twice. A failure of the
// machine (an unreadable file, a block that cannot be read or stored) never
// matches it, so a host can tell its caller's fault from its own.
var ErrBadManifest = errors.New("bad manifest")

// refusal is an error that reads as its text and matches ErrBadManifest, so
// what a host tells its caller names the path and not the sentinel.
type refusal string

func (r refusal) Error() string        { return string(r) }
func (r refusal) Is(target error) bool { return target == ErrBadManifest }

func refuse(format string, args ...any) error { return refusal(fmt.Sprintf(format, args...)) }

// Rebuild adds the files under dir on top of the paths carry names in parent:
// the version a manifest push describes. What is neither uploaded nor carried
// is not in it. A host checks a push by comparing the result with the CID the
// push was signed for. Only parent's folders on the carried paths are read.
// It gives up when ctx ends, which a parent with blocks missing here needs.
// What is wrong with the manifest itself matches ErrBadManifest.
func (e *Embedded) Rebuild(ctx context.Context, parent, dir string, carry []string) (string, error) {
	if e.dag == nil {
		return "", fmt.Errorf("node not started")
	}
	pid, err := cid.Decode(parent)
	if err != nil {
		return "", err
	}
	f := &folders{dag: e.dag, cache: map[cid.Cid]map[string]*ipld.Link{}}
	t, err := f.carried(ctx, pid, carry)
	if err != nil {
		return "", err
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		return t.put(filepath.ToSlash(rel), &tree{file: p})
	})
	if err != nil {
		return "", err
	}
	nd, err := t.build(ctx, e.dag)
	if err != nil {
		return "", err
	}
	return nd.Cid().String(), nil
}

// CheckManifest says whether a manifest push of upload and carry on parent
// makes exactly the version root. The uploaded entries are linked from root's
// own folders, so no file data is read, and carried ones need only parent's
// folders. A client checks this before pushing, as the host will after.
func (e *Embedded) CheckManifest(ctx context.Context, root, parent string, upload, carry []string) error {
	if e.dag == nil {
		return fmt.Errorf("node not started")
	}
	rid, err := cid.Decode(root)
	if err != nil {
		return err
	}
	pid, err := cid.Decode(parent)
	if err != nil {
		return err
	}
	f := &folders{dag: e.dag, cache: map[cid.Cid]map[string]*ipld.Link{}}
	t, err := f.carried(ctx, pid, carry)
	if err != nil {
		return err
	}
	for _, rel := range upload {
		parts, err := splitPath(rel)
		if err != nil {
			return err
		}
		l, err := f.link(ctx, rid, parts)
		if err != nil {
			return fmt.Errorf("uploaded %q: %w", rel, err)
		}
		if err := t.put(rel, &tree{node: linkOnly{l.Cid, l.Size}}); err != nil {
			return err
		}
	}
	nd, err := t.build(ctx, memoryDAG())
	if err != nil {
		return err
	}
	if nd.Cid() != rid {
		return fmt.Errorf("the manifest makes %s, not %s", nd.Cid(), root)
	}
	return nil
}

// VersionFile is one file of a version.
type VersionFile struct {
	Path string
	Size int64
	CID  string
}

// localDAG reads the blocks this node holds and nothing else: one that is not
// here is an error at once. The node's own DAG would ask the network for it,
// and wait until the context ends even offline. A push has no deadline, so
// reading a version that is not whole through that DAG would hang it forever.
func (e *Embedded) localDAG() ipld.DAGService {
	return merkledag.NewDAGService(blockservice.New(e.bstore, offline.Exchange(e.bstore)))
}

// Files lists every file of the version root, sorted by path, from this
// node's blocks alone: a block that is not here is an error, never a wait on
// the network. A chunked file is sized by its root block, a small one by the
// link to it.
func (e *Embedded) Files(ctx context.Context, root string) ([]VersionFile, error) {
	if e.bstore == nil {
		return nil, fmt.Errorf("node not started")
	}
	rid, err := cid.Decode(root)
	if err != nil {
		return nil, err
	}
	dag := e.localDAG()
	var out []VersionFile
	var walk func(rel string, c cid.Cid, tsize uint64) error
	walk = func(rel string, c cid.Cid, tsize uint64) error {
		if c.Type() == cid.Raw { // a small file: one block of raw bytes, sized by its link
			out = append(out, VersionFile{rel, int64(tsize), c.String()})
			return nil
		}
		nd, err := dag.Get(ctx, c)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		pn, ok := nd.(*merkledag.ProtoNode)
		if !ok {
			return fmt.Errorf("%s: not a unixfs node", rel)
		}
		fsn, err := ft.FSNodeFromBytes(pn.Data())
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		switch fsn.Type() {
		case ft.TFile, ft.TRaw:
			out = append(out, VersionFile{rel, int64(fsn.FileSize()), c.String()})
			return nil
		case ft.TDirectory, ft.THAMTShard:
			d, err := uio.NewDirectoryFromNode(dag, nd)
			if err != nil {
				return err
			}
			links, err := d.Links(ctx)
			if err != nil {
				return err
			}
			for _, l := range links {
				if err := walk(joinRel(rel, l.Name), l.Cid, l.Size); err != nil {
					return err
				}
			}
			return nil
		}
		return fmt.Errorf("%s: unsupported unixfs node", rel)
	}
	if err := walk("", rid, 0); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// OpenFile reads the file at rel in version root from this node's blocks, so
// an upload sends the version's own bytes even if the files on disk have
// changed since it was added. Like Files it reads no other blocks: opening a
// file whose block is not here fails at once, and so does a read that reaches
// a chunk that is not here.
func (e *Embedded) OpenFile(ctx context.Context, root, rel string) (io.ReadSeekCloser, int64, error) {
	if e.bstore == nil {
		return nil, 0, fmt.Errorf("node not started")
	}
	rid, err := cid.Decode(root)
	if err != nil {
		return nil, 0, err
	}
	parts, err := splitPath(rel)
	if err != nil {
		return nil, 0, err
	}
	dag := e.localDAG()
	f := &folders{dag: dag, cache: map[cid.Cid]map[string]*ipld.Link{}}
	l, err := f.link(ctx, rid, parts)
	if err != nil {
		return nil, 0, err
	}
	nd, err := dag.Get(ctx, l.Cid)
	if err != nil {
		return nil, 0, err
	}
	r, err := uio.NewDagReader(ctx, nd, dag)
	if err != nil {
		return nil, 0, err
	}
	return r, int64(r.Size()), nil
}

// tree is a version being put together. Each name is a node taken as it is
// (carried, or an uploaded file already in the version), a file still to
// add, or a folder of more names.
type tree struct {
	kids map[string]*tree
	node ipld.Node
	file string
}

func (t *tree) leaf() bool { return t.node != nil || t.file != "" }

// put places leaf at rel. No path may sit inside one taken whole, and none
// may be placed twice.
func (t *tree) put(rel string, leaf *tree) error {
	parts, err := splitPath(rel)
	if err != nil {
		return err
	}
	at := t
	for i, name := range parts[:len(parts)-1] {
		next, ok := at.kids[name]
		if !ok {
			next = &tree{kids: map[string]*tree{}}
			at.kids[name] = next
		}
		if next.leaf() {
			return refuse("%q is inside %q, which is taken whole", rel, strings.Join(parts[:i+1], "/"))
		}
		at = next
	}
	last := parts[len(parts)-1]
	if _, dup := at.kids[last]; dup {
		return refuse("%q is given twice, or holds another given path", rel)
	}
	at.kids[last] = leaf
	return nil
}

// build stores the folders of t in dag, bottom up, and returns the root.
// dag-pb sorts links when it encodes, and sharding does not depend on order,
// so the root is the CID an add of the same tree gives.
func (t *tree) build(ctx context.Context, dag ipld.DAGService) (ipld.Node, error) {
	d, err := uio.NewDirectory(linkDAG{dag})
	if err != nil {
		return nil, err
	}
	d.SetCidBuilder(v1Builder)
	names := make([]string, 0, len(t.kids))
	for name := range t.kids {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		k := t.kids[name]
		nd := k.node
		switch {
		case nd != nil:
		case k.file != "":
			nd, err = addFile(ctx, dag, k.file, true, v1Builder)
		default:
			nd, err = k.build(ctx, dag)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := d.AddChild(ctx, name, nd); err != nil {
			return nil, err
		}
	}
	nd, err := d.GetNode()
	if err != nil {
		return nil, err
	}
	return nd, dag.Add(ctx, nd)
}

// folders reads the folders of versions once each, keeping their links by name.
type folders struct {
	dag   ipld.DAGService
	cache map[cid.Cid]map[string]*ipld.Link
}

// link is the link to the entry at parts under the folder root, reading only
// the folders on the way.
func (f *folders) link(ctx context.Context, root cid.Cid, parts []string) (*ipld.Link, error) {
	at := root
	var l *ipld.Link
	for i, name := range parts {
		m, ok := f.cache[at]
		if !ok {
			nd, err := f.dag.Get(ctx, at)
			if err != nil {
				return nil, err
			}
			d, err := uio.NewDirectoryFromNode(f.dag, nd)
			if err != nil {
				return nil, refuse("%q is not a folder", strings.Join(parts[:i], "/"))
			}
			links, err := d.Links(ctx)
			if err != nil {
				return nil, err
			}
			m = make(map[string]*ipld.Link, len(links))
			for _, x := range links {
				m[x.Name] = x
			}
			f.cache[at] = m
		}
		if l = m[name]; l == nil {
			return nil, fs.ErrNotExist
		}
		at = l.Cid
	}
	return l, nil
}

// carried starts a version from the paths of parent a manifest carries.
func (f *folders) carried(ctx context.Context, parent cid.Cid, carry []string) (*tree, error) {
	t := &tree{kids: map[string]*tree{}}
	for _, rel := range carry {
		parts, err := splitPath(rel)
		if err != nil {
			return nil, err
		}
		l, err := f.link(ctx, parent, parts)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, refuse("carried %q is not in the parent", rel)
		case err != nil: // a path through a file, or folders that cannot be read
			return nil, fmt.Errorf("carried %q: %w", rel, err)
		}
		if err := t.put(rel, &tree{node: linkOnly{l.Cid, l.Size}}); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// linkOnly is a node known only by its CID and size, which is all a folder
// needs to link it: a carried file or folder need not be on this machine.
type linkOnly struct {
	c    cid.Cid
	size uint64
}

var errLinkOnly = errors.New("only the link of this node is here")

func (n linkOnly) Cid() cid.Cid                                       { return n.c }
func (n linkOnly) Size() (uint64, error)                              { return n.size, nil }
func (n linkOnly) RawData() []byte                                    { return nil }
func (n linkOnly) String() string                                     { return n.c.String() }
func (n linkOnly) Loggable() map[string]interface{}                   { return map[string]interface{}{"cid": n.c} }
func (n linkOnly) Resolve([]string) (interface{}, []string, error)    { return nil, nil, errLinkOnly }
func (n linkOnly) Tree(string, int) []string                          { return nil }
func (n linkOnly) ResolveLink([]string) (*ipld.Link, []string, error) { return nil, nil, errLinkOnly }
func (n linkOnly) Copy() ipld.Node                                    { return n }
func (n linkOnly) Links() []*ipld.Link                                { return nil }
func (n linkOnly) Stat() (*ipld.NodeStat, error) {
	return &ipld.NodeStat{CumulativeSize: int(n.size)}, nil
}

// linkDAG is a DAG service that does not store linkOnly nodes. A sharded
// folder adds each child to its DAG before it links it, and a node known only
// by its CID has no block: storing it would put an empty block under that CID.
type linkDAG struct{ ipld.DAGService }

func (d linkDAG) Add(ctx context.Context, nd ipld.Node) error {
	if _, only := nd.(linkOnly); only {
		return nil
	}
	return d.DAGService.Add(ctx, nd)
}

func (d linkDAG) AddMany(ctx context.Context, nds []ipld.Node) error {
	var keep []ipld.Node
	for _, nd := range nds {
		if _, only := nd.(linkOnly); !only {
			keep = append(keep, nd)
		}
	}
	return d.DAGService.AddMany(ctx, keep)
}

// splitPath splits a slash path inside a version into names, refusing any
// that could climb out of it.
func splitPath(rel string) ([]string, error) {
	parts := strings.Split(strings.Trim(rel, "/"), "/")
	for _, s := range parts {
		if s == "" || s == "." || s == ".." {
			return nil, refuse("bad path %q", rel)
		}
	}
	return parts, nil
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// dir loads the directory c, from the network when it is not here.
func (e *Embedded) dir(ctx context.Context, c string) (uio.Directory, error) {
	if e.dag == nil {
		return nil, fmt.Errorf("node not started")
	}
	id, err := cid.Decode(c)
	if err != nil {
		return nil, err
	}
	nd, err := e.dag.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c, err)
	}
	return uio.NewDirectoryFromNode(e.dag, nd)
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
