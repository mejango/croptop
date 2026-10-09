package store

import (
	"encoding/json"
	"fmt"
)

const (
	StorageKey    = "croptopStorage"
	StorageP2P    = "p2p"
	StorageHosted = "hosted"
)

// StorageMode requires an explicit choice before a site uses a reliable host.
// Older sites, including ones with a saved host URL, default to P2P.
func (s *Site) StorageMode() string {
	var mode string
	if json.Unmarshal(s.Raw[StorageKey], &mode) == nil && mode == StorageHosted {
		return StorageHosted
	}
	return StorageP2P
}

func (s *Site) HostingEnabled() bool { return s.StorageMode() == StorageHosted }

// SetStorage validates before changing the saved choice.
func (s *Site) SetStorage(mode string) error {
	if mode != StorageP2P && mode != StorageHosted {
		return fmt.Errorf("choose p2p or hosted storage")
	}
	if s.Raw == nil {
		s.Raw = doc{}
	}
	s.Raw.put(StorageKey, mode)
	return nil
}
