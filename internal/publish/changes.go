package publish

import (
	"context"
	"io"
	"sort"
	"strings"

	"github.com/mejango/croptop/internal/ipfs"
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

// changes says how version root differs from parent, a version the host
// holds. It returns the files to upload and the paths to carry, each the
// largest unchanged file or folder. Anything of parent in neither list is
// deleted. parent's folders come from this node's blocks or, when it lacks
// them, from the host, checked against their CIDs. Where a folder cannot be
// compared, all its files are uploaded: more bytes, never a wrong version.
func (p *Publisher) changes(ctx context.Context, eng changesEngine, hostURL, root, parent string) (upload, carry []string, err error) {
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
		nl, err := eng.Links(ctx, now)
		if err != nil {
			return nil, nil, err
		}
		bl, err := p.links(ctx, eng, hostURL, before)
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
			if ok && dirs[at] {
				if u, k, err := walk(at, c, old); err == nil {
					up, keep = append(up, u...), append(keep, k...)
					continue
				}
			}
			up = append(up, under(at)...)
		}
		return up, keep, nil
	}
	upload, carry, err = walk("", root, parent)
	sort.Strings(upload)
	sort.Strings(carry)
	return upload, carry, err
}
