package ipfs

import (
	"net"
	"slices"
	"testing"
)

// Remote root through CVE-2026-98432 was demonstrated against MPTCP listeners,
// which Go opens by default on Linux. go.mod's godebug block turns them off.
func TestMPTCPListenersOff(t *testing.T) {
	if (&net.ListenConfig{}).MultipathTCP() {
		t.Fatal("listeners use MPTCP; go.mod should set godebug multipathtcp=0")
	}
}

func TestSidecarEnvTurnsOffMPTCP(t *testing.T) {
	t.Setenv("GODEBUG", "gctrace=1")
	if !slices.Contains((&Node{}).env(), "GODEBUG=gctrace=1,multipathtcp=0") {
		t.Fatal("kubo would keep MPTCP on") // never print env: it holds secrets
	}
}
