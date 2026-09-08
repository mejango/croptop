//go:build !windows

package ipfs

import (
	"context"
	"net"
	"syscall"
	"testing"
)

// A listener bound with SO_REUSEPORT (as kubo does) must still count as busy.
func TestFreePortSkipsReusePortListener(t *testing.T) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		return c.Control(func(fd uintptr) {
			syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
		})
	}}
	l, err := lc.Listen(context.Background(), "tcp", "0.0.0.0:0")
	if err != nil {
		t.Skip("no reuseport:", err)
	}
	defer l.Close()
	busy := l.Addr().(*net.TCPAddr).Port
	p, err := freePort(busy, busy+3)
	if err != nil || p == busy {
		t.Fatalf("got %d %v (busy %d)", p, err, busy)
	}
}
