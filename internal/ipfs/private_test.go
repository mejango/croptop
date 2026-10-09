package ipfs

import (
	"context"
	"testing"
)

func TestPrivateNodeCannotBeDiscoveredOrServeBlocks(t *testing.T) {
	node := NewEmbedded(t.TempDir())
	node.Private = true
	// A stale operator configuration must not advertise the private node.
	node.Announce = []string{"/ip4/127.0.0.1/tcp/12345"}
	if err := node.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { node.Stop() })
	if !node.Offline {
		t.Fatal("private node did not disable routing and peering")
	}
	if len(node.host.Network().ListenAddresses()) != 0 || len(node.Addrs()) != 0 {
		t.Fatalf("private node has listeners: %v, %v", node.host.Network().ListenAddresses(), node.Addrs())
	}
	if node.ConnectLocalNodes(context.Background()) != 0 {
		t.Fatal("private node connected to a local public node")
	}
	if len(node.host.Network().Peers()) != 0 {
		t.Fatal("private node has public peers")
	}
}
