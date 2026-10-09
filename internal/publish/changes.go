package publish

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/ipfs/go-cid"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/store"
)

// changesEngine is what changes-only publishing needs from the IPFS engine:
// the embedded one has it, the kubo one does not.
type changesEngine interface {
	blockLister
	Files(ctx context.Context, root string) ([]ipfs.VersionFile, error)
	OpenFile(ctx context.Context, root, rel string) (io.ReadSeekCloser, int64, error)
	CheckManifest(ctx context.Context, root, parent string, upload, carry []string) error
	SignRecord(key, c string, seq uint64) ([]byte, error)
	AnnounceRecord(ctx context.Context, key, c string, rec []byte) error
}

// errNothingToUpload is what changes answers when the version differs from its
// parent only by deletions.
var errNothingToUpload = errors.New("no file changed; deletions alone go up as a full push")

// changes says how version root differs from parent, a version the host
// holds. It returns the files to upload and the paths to carry, each the
// largest unchanged file or folder. Anything of parent in neither list is
// deleted. The carry list is never nil: an empty one is still a manifest push,
// which drops what is not uploaded, where a nil one would be a plain push on
// top of parent and keep it. If root differs from parent but no file changed,
// only deletions remain, and it returns errNothingToUpload: a push is
// committed with the last file sent, so a manifest push with no files cannot
// be made (the Worker answers 400), and such a version goes up whole.
// parent's folders come from this node's blocks or, when it lacks them, from
// the host, checked against their CIDs. Where a folder cannot be compared,
// because it could not be read whole (a sharded folder of which this node lacks
// blocks) or was a file before, all its files are uploaded: more bytes, never a
// wrong version. If ctx ends that is an error, not a diff: the folders it kept
// from being read would look like folders that cannot be compared.
func (p *Publisher) changes(ctx context.Context, eng changesEngine, hostURL, root, parent string, policy ...*store.Site) (upload, carry []string, err error) {
	files, err := eng.Files(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	dirs := map[string]bool{}
	for _, f := range files {
		for d := f.Path; strings.Contains(d, "/"); {
			d = d[:strings.LastIndex(d, "/")]
			dirs[d] = true
		}
	}
	under := func(at string) []string {
		var out []string
		for _, f := range files {
			if f.Path == at || strings.HasPrefix(f.Path, at+"/") {
				out = append(out, f.Path)
			}
		}
		return out
	}
	var walk func(rel, now, before string) (up, keep []string, err error)
	walk = func(rel, now, before string) (up, keep []string, err error) {
		// now is a folder of version root, this node's own, added by AddDir, and
		// Files has just read all its folders from the blocks here: no wait on the
		// network
		nl, err := eng.Links(ctx, now)
		if err != nil {
			return nil, nil, err
		}
		bl, err := p.links(ctx, eng, hostURL, before, policy...)
		if err != nil {
			return nil, nil, err
		}
		for name, c := range nl {
			at := name
			if rel != "" {
				at = rel + "/" + name
			}
			old, ok := bl[name]
			if ok && old == c {
				keep = append(keep, at)
				continue
			}
			// an old entry that is a raw block was a small file, never a folder:
			// there is nothing to compare, and no need to ask for its block, which
			// the Worker refuses and a machine without it would wait for
			if ok && dirs[at] && !rawLeaf(old) {
				u, k, err := walk(at, c, old)
				if err == nil {
					up, keep = append(up, u...), append(keep, k...)
					continue
				}
				if ctx.Err() == nil { // an ended ctx is answered once, below
					p.log("changes since %s: %s cannot be compared: %v; sending all its files", parent, at, err)
				}
			}
			up = append(up, under(at)...)
		}
		return up, keep, nil
	}
	upload, carry, err = walk("", root, parent)
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(upload) == 0 && root != parent {
		return nil, nil, errNothingToUpload
	}
	sort.Strings(upload)
	sort.Strings(carry)
	if carry == nil {
		carry = []string{}
	}
	return upload, carry, nil
}

// rawLeaf says whether c is a block of raw file bytes, which is never a folder.
func rawLeaf(c string) bool {
	id, err := cid.Decode(c)
	return err == nil && id.Type() == cid.Raw
}
