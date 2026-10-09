package mobile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

// PrivatePublisher confines unpublished IPFS blocks to a disposable node for
// each preparation and commit. Durable files remain in the operation directory
// owned by Server. No draft block enters the public host's node before commit.
type PrivatePublisher struct {
	base     *publish.Publisher
	tempRoot string // empty uses the operating system's private temporary directory
}

const privateNodePrefix = "croptop-mobile-node-"

var _ Publisher = (*PrivatePublisher)(nil)

func NewPrivatePublisher(base *publish.Publisher) *PrivatePublisher {
	return &PrivatePublisher{base: base}
}

// CleanupStaging selects the service-owned cache root and removes node caches
// stranded by process death. Server calls it once after acquiring its exclusive
// data-directory lock and before starting jobs. Durable operation directories
// are outside this root and are never removed here.
func (p *PrivatePublisher) CleanupStaging(stagingRoot string) error {
	if stagingRoot == "" || filepath.Base(filepath.Clean(stagingRoot)) != "private-nodes" {
		return errors.New("private node cache must use the service's private-nodes directory")
	}
	root, err := filepath.Abs(stagingRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private node cache root must be a real directory")
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		suffix, ok := strings.CutPrefix(entry.Name(), privateNodePrefix)
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(suffix, 10, 32)
		if err != nil || strconv.FormatUint(n, 10) != suffix {
			continue // only the exact decimal suffix os.MkdirTemp generates
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("private node cache %s is not a real directory", entry.Name())
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	p.tempRoot = root
	return nil
}

func (p *PrivatePublisher) PreparePost(ctx context.Context, hostURL, name string, post publish.NewPost, workDir string) (publish.PreparedPost, error) {
	private, cleanup, err := p.open(ctx)
	if err != nil {
		return publish.PreparedPost{}, err
	}
	defer cleanup()
	return private.PreparePost(ctx, hostURL, name, post, workDir)
}

func (p *PrivatePublisher) CommitPreparedPost(ctx context.Context, prepared publish.PreparedPost, auth publish.PostAuthorization) (publish.Posted, error) {
	private, cleanup, err := p.open(ctx)
	if err != nil {
		return publish.Posted{}, err
	}
	defer cleanup()
	return private.CommitPreparedPost(ctx, prepared, auth)
}

// Inspection only reads an already-published signed head and its CID-verified
// metadata. Sharing the public node for these reads introduces no draft blocks.
func (p *PrivatePublisher) InspectSite(ctx context.Context, hostURL, name string) (publish.SiteSnapshot, error) {
	if p.base == nil {
		return publish.SiteSnapshot{}, errors.New("mobile publisher is not configured")
	}
	return p.base.InspectSite(ctx, hostURL, name)
}

func (p *PrivatePublisher) InspectPost(ctx context.Context, hostURL, name, postID string) (*publish.Posted, error) {
	if p.base == nil {
		return nil, errors.New("mobile publisher is not configured")
	}
	return p.base.InspectPost(ctx, hostURL, name, postID)
}

func (p *PrivatePublisher) open(ctx context.Context) (*publish.Publisher, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if p.base == nil || p.base.Render == nil {
		return nil, nil, errors.New("mobile publisher needs a renderer")
	}
	dir, err := os.MkdirTemp(p.tempRoot, privateNodePrefix+"*")
	if err != nil {
		return nil, nil, err
	}
	node := ipfs.NewEmbedded(dir)
	node.Private = true
	cleanup := func() {
		stopErr := node.Stop()
		removeErr := os.RemoveAll(dir)
		if p.base.Log != nil {
			if err := errors.Join(stopErr, removeErr); err != nil {
				p.base.Log(fmt.Sprintf("private mobile staging cleanup: %v", err))
			}
		}
	}
	if err := node.Start(ctx); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("private mobile staging: %w", err)
	}
	st := &store.Store{Root: filepath.Join(dir, "store")}
	// The mobile compatibility contract covers the operator's default
	// template, never a site-selected local template or its custom code.
	r := &render.Renderer{Store: st, Templates: p.base.Render.Templates, CIDs: node,
		FFmpeg: p.base.Render.FFmpeg, Log: p.base.Render.Log}
	return &publish.Publisher{Store: st, Node: node, Render: r, Log: p.base.Log}, cleanup, nil
}
