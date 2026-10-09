package ipfs

import (
	"net"
	"testing"
)

func TestAssetName(t *testing.T) {
	cases := map[[2]string]string{
		{"darwin", "arm64"}:  "kubo_v0.43.0_darwin-arm64.tar.gz",
		{"linux", "amd64"}:   "kubo_v0.43.0_linux-amd64.tar.gz",
		{"windows", "amd64"}: "kubo_v0.43.0_windows-amd64.zip",
	}
	for k, want := range cases {
		if got := assetName(k[0], k[1]); got != want {
			t.Fatalf("%v: got %s want %s", k, got, want)
		}
	}
}

func TestFreePortSkipsBusy(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	busy := l.Addr().(*net.TCPAddr).Port
	p, err := freePort(busy, busy+3)
	if err != nil || p == busy {
		t.Fatalf("got %d %v", p, err)
	}
}
