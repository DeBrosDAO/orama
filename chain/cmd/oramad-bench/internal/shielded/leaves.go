package shielded

import (
	"context"
	"encoding/hex"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// TxRecord is one transaction of a block as the scan needs it: whether it executed and what it
// carried. A transaction that failed changed no state, so its bundle added no note.
type TxRecord struct {
	Code uint32
	Msgs []sdk.Msg
}

// BlockSource reads the transactions of committed blocks, in block order.
type BlockSource interface {
	Txs(ctx context.Context, height int64) ([]TxRecord, error)
}

// Leaves is the chain's note commitments in tree order.
type Leaves struct {
	// Commitments are the hex of each commitment, the first leaf first.
	Commitments []string
	// LastChange is the height of the last block that appended a commitment (0 when none did).
	LastChange int64
}

// Commitments returns the note commitments msg appends to the tree, and whether msg is a shielded
// message at all.
func Commitments(msg sdk.Msg) ([][bundle.NodeLen]byte, bool, error) {
	var raw []byte
	switch m := msg.(type) {
	case *types.MsgShield:
		raw = m.Bundle
	case *types.MsgShieldEarnings:
		raw = m.Bundle
	case *types.MsgShieldedTransfer:
		raw = m.Bundle
	case *types.MsgUnshield:
		raw = m.Bundle
	default:
		return nil, false, nil
	}
	b, err := bundle.Parse(raw)
	if err != nil {
		return nil, true, fmt.Errorf("parse a bundle the chain executed: %w", err)
	}
	return b.Commitments, true, nil
}

// ScanLeaves rebuilds the note tree's leaves from blocks from to to (inclusive) by reading the
// executed bundles in block and transaction order, which is the order the chain appends them in.
// from must be the first block of the chain for the result to be the whole tree.
func ScanLeaves(ctx context.Context, src BlockSource, from, to int64) (Leaves, error) {
	var out Leaves
	for h := from; h <= to; h++ {
		txs, err := src.Txs(ctx, h)
		if err != nil {
			return Leaves{}, fmt.Errorf("read the transactions of block %d: %w", h, err)
		}
		for _, rec := range txs {
			if rec.Code != 0 {
				continue
			}
			added, err := appendTx(&out, rec)
			if err != nil {
				return Leaves{}, fmt.Errorf("block %d: %w", h, err)
			}
			if added {
				out.LastChange = h
			}
		}
	}
	return out, nil
}

func appendTx(out *Leaves, rec TxRecord) (bool, error) {
	added := false
	for _, msg := range rec.Msgs {
		cmxs, shielded, err := Commitments(msg)
		if err != nil {
			return false, err
		}
		if !shielded {
			continue
		}
		for _, c := range cmxs {
			out.Commitments = append(out.Commitments, hex.EncodeToString(c[:]))
			added = true
		}
	}
	return added, nil
}
