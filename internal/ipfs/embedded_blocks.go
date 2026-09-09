package ipfs

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/ipfs/boxo/blockservice"
	"github.com/ipfs/boxo/ipld/merkledag"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
)

// Block stream: the wire format croptop uses to push a whole site to a host.
// For each block: uvarint(len(cid)) cid uvarint(len(data)) data. Root first.
// Both ends are croptop, so this stays simpler than CAR.

// BlockService exposes the node's block service for gateway serving.
func (e *Embedded) BlockService() blockservice.BlockService { return e.bserv }

// WriteBlocks streams every block under root, root first.
func (e *Embedded) WriteBlocks(ctx context.Context, root string, w io.Writer) error {
	if e.dag == nil {
		return fmt.Errorf("node not started")
	}
	id, err := cid.Decode(root)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(w)
	var n int
	err = merkledag.Walk(ctx, merkledag.GetLinksDirect(e.dag), id, func(c cid.Cid) bool {
		blk, err := e.bserv.GetBlock(ctx, c)
		if err != nil {
			return false
		}
		writeUvarint(bw, uint64(len(c.Bytes())))
		bw.Write(c.Bytes())
		writeUvarint(bw, uint64(len(blk.RawData())))
		bw.Write(blk.RawData())
		n++
		return true
	})
	if err != nil {
		return err
	}
	return bw.Flush()
}

// ReadBlocks stores a block stream and returns how many blocks it held.
// Each block is verified against its CID before it is kept.
func (e *Embedded) ReadBlocks(ctx context.Context, r io.Reader, maxBytes int64) (int, error) {
	if e.bstore == nil {
		return 0, fmt.Errorf("node not started")
	}
	br := bufio.NewReader(io.LimitReader(r, maxBytes))
	n := 0
	for {
		cl, err := binary.ReadUvarint(br)
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		if cl > 256 {
			return n, fmt.Errorf("bad cid length %d", cl)
		}
		cb := make([]byte, cl)
		if _, err := io.ReadFull(br, cb); err != nil {
			return n, err
		}
		c, err := cid.Cast(cb)
		if err != nil {
			return n, err
		}
		dl, err := binary.ReadUvarint(br)
		if err != nil {
			return n, err
		}
		if dl > 4<<20 {
			return n, fmt.Errorf("block too large: %d", dl)
		}
		data := make([]byte, dl)
		if _, err := io.ReadFull(br, data); err != nil {
			return n, err
		}
		blk, err := blocks.NewBlockWithCid(data, c) // checks the hash
		if err != nil {
			return n, err
		}
		if err := e.bstore.Put(ctx, blk); err != nil {
			return n, err
		}
		n++
	}
}

// HasTree reports whether every block under root is stored locally.
func (e *Embedded) HasTree(ctx context.Context, root string) (bool, error) {
	id, err := cid.Decode(root)
	if err != nil {
		return false, err
	}
	ok := true
	err = merkledag.Walk(ctx, merkledag.GetLinksDirect(e.dag), id, func(c cid.Cid) bool {
		has, err := e.bstore.Has(ctx, c)
		if err != nil || !has {
			ok = false
			return false
		}
		return true
	})
	return ok && err == nil, nil
}

func writeUvarint(w *bufio.Writer, v uint64) {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	w.Write(buf[:n])
}
