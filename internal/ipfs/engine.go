package ipfs

import "context"

// Engine is what the rest of croptop needs from an IPFS node. The kubo
// sidecar (*Node) and the embedded boxo node (*Embedded) both provide it.
type Engine interface {
	Start(ctx context.Context) error
	Stop() error
	Running() bool
	LastError() string
	Keystore() *Keystore
	Info(ctx context.Context) (Info, error)

	// FileCIDv0 hashes one file the way Planet does for attachments.
	FileCIDv0(ctx context.Context, path string) (string, error)
	// AddDir adds a rendered site and returns its CIDv1 root.
	AddDir(ctx context.Context, dir string) (string, error)
	// Get downloads an IPFS path into dest.
	Get(ctx context.Context, ipfsPath, dest string) error
	// Resolve resolves /ipns/<name-or-ens> one step.
	Resolve(ctx context.Context, path string) (string, error)
	// NetworkRecord fetches the best IPNS record the network has for name.
	NetworkRecord(ctx context.Context, name string) (*Record, error)
	// NamePublish publishes cid under the key named key at an explicit sequence.
	NamePublish(ctx context.Context, key, cid string, seq uint64) error
	// Provide announces a CID this node holds.
	Provide(ctx context.Context, cid string) error
	// ConnectLocalNodes peers with other IPFS nodes on this machine.
	ConnectLocalNodes(ctx context.Context) int
	// FindProviders lists peer IDs currently announcing a CID.
	FindProviders(ctx context.Context, cid string) ([]string, error)
}

var _ Engine = (*Node)(nil)
