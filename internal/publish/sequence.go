package publish

import (
	"errors"

	"github.com/mejango/croptop/internal/ipfs"
)

var (
	// ErrPublishedElsewhere: the network holds a newer record for this name
	// that we did not write. Another machine owns the site now; sync first.
	ErrPublishedElsewhere = errors.New("this site was published from another machine; run sync first or publish with --force")
	// ErrWouldResetSequence: we have no sequence history for a site that has
	// been published before and the network is unreachable. Publishing at 1
	// would be ignored by the network forever.
	ErrWouldResetSequence = errors.New("cannot reach the IPNS network to learn the current sequence; retry online or publish with --force")
)

// nextSequence decides the IPNS sequence number for the next publish.
// IPNS orders records by sequence; the DHT keeps the highest one it has
// seen, so every publish must go above whatever the network already holds.
func nextSequence(local uint64, lastCID string, net *ipfs.Record, netErr error, force bool) (uint64, error) {
	switch {
	case netErr == nil:
		// Only a machine that has published before can be "overtaken". A fresh
		// import or adopt (local == 0) may see an older record from a slow DHT
		// peer; it takes the network as its baseline instead of refusing.
		if local > 0 && lastCID != "" && net.Value != "/ipfs/"+lastCID && !force {
			return 0, ErrPublishedElsewhere
		}
		return max(local, net.Sequence) + 1, nil
	case errors.Is(netErr, ipfs.ErrNoRecord):
		return local + 1, nil
	default:
		if local == 0 && lastCID != "" && !force {
			return 0, ErrWouldResetSequence
		}
		return local + 1, nil
	}
}
