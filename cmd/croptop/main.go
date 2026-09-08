package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mejango/croptop/internal/ipfs"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: croptop <command>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "ipfs-smoke":
		if err := ipfsSmoke(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintln(os.Stderr, "unknown command", os.Args[1])
		os.Exit(2)
	}
}

func ipfsSmoke() error {
	data := ".data"
	bin, err := ipfs.EnsureKubo(filepath.Join(data, "kubo"), func(s string) { fmt.Println(s) })
	if err != nil {
		return err
	}
	n := ipfs.NewNode(bin, filepath.Join(data, "ipfs"))
	ctx := context.Background()
	if err := n.Init(ctx); err != nil {
		return err
	}
	fmt.Printf("ports api=%d gateway=%d swarm=%d\n", n.APIPort, n.GatewayPort, n.SwarmPort)
	if err := n.Start(ctx); err != nil {
		return err
	}
	defer n.Stop()
	info, err := n.Info(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("peer %s version %s peers %d\n", info.PeerID, info.Version, info.Peers)
	if len(os.Args) > 2 {
		rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		raw, err := n.Run(rctx, "name", "get", os.Args[2])
		fmt.Printf("name get: %d bytes err=%v\n", len(raw), err)
		rec, err := n.NetworkRecord(rctx, os.Args[2])
		fmt.Printf("record %+v err=%v\n", rec, err)
		f, _ := os.CreateTemp("", "rec")
		f.Write(raw); f.Close()
		out, err := n.Run(rctx, "name", "inspect", "--enc=json", f.Name())
		fmt.Printf("inspect raw: %s err=%v\n", out, err)
	}
	return nil
}
