package publish

import (
	"context"
	"errors"
	"fmt"

	"github.com/mejango/croptop/internal/gateway"
	"github.com/mejango/croptop/internal/store"
)

// ErrHostingDisabled ends an upload without retrying. A saved endpoint is
// only an address; reliable hosting always needs an explicit storage choice.
var ErrHostingDisabled = errors.New("reliable hosting is disabled; choose hosted storage in site settings")

var errHostChanged = errors.New("the site's reliable host changed during the upload")

// hostedSite reads the saved policy before work uses a host. In particular,
// background jobs cannot keep using the snapshot made before an opt-out.
func (p *Publisher) hostedSite(site *store.Site) (*store.Site, error) {
	if p.Store != nil {
		current, err := p.Store.Site(site.ID)
		if err != nil {
			return nil, err
		}
		site = current
	}
	if !site.HostingEnabled() {
		return nil, ErrHostingDisabled
	}
	return site, nil
}

// checkHost checks each individual request, including a resumed multipart
// upload. Changing a host ends this attempt instead of splitting a version
// across two hosts; a background retry starts over against the new endpoint.
func (p *Publisher) checkHost(site *store.Site, base string) error {
	current, err := p.hostedSite(site)
	if err != nil {
		return err
	}
	if HostOf(current) != base {
		return fmt.Errorf("%w: %s", errHostChanged, HostOf(current))
	}
	return nil
}

func (p *Publisher) siteHostEntry(ctx context.Context, site *store.Site, base string) (*hostKey, error) {
	if err := p.checkHost(site, base); err != nil {
		return nil, err
	}
	return hostEntry(ctx, base, site.IPNS)
}

// hostRead allows read-only use of an arbitrary published source during
// adopt and key-only posting. Reads for an owned site also check its current
// policy, so a long sparse sync or comparison stops using the host on opt-out.
func (p *Publisher) hostRead(ctx context.Context, base, path string, policy ...*store.Site) ([]byte, int, error) {
	if base == "" {
		return nil, 0, ErrHostingDisabled
	}
	if len(policy) > 0 && policy[0] != nil {
		if err := p.checkHost(policy[0], base); err != nil {
			return nil, 0, err
		}
	}
	return httpGet(ctx, base+path)
}

// restoreStorageChoice keeps consent local across source rebuilds/imports.
// A new copy starts P2P; a replacement keeps the saved owner preferences,
// including absent keys, rather than inheriting older remote choices.
func restoreStorageChoice(site, local *store.Site) error {
	mode := store.StorageP2P
	if local != nil {
		mode = local.StorageMode()
	}
	if err := site.SetStorage(mode); err != nil {
		return err
	}
	if local != nil {
		for _, key := range []string{HostKey, NameKey, gateway.SettingKey} {
			if value, ok := local.Raw[key]; ok {
				site.Raw[key] = value
			} else {
				delete(site.Raw, key)
			}
		}
	}
	return nil
}
