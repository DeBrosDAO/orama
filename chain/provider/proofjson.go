package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// proofFileRow is the JSON object `orama storage prove --file` reads.
// Leaf and siblings are lowercase hex. core/pkg/clusterreg.ParseProofs
// accepts this shape. This package does not import core.
type proofFileRow struct {
	DealID    uint64   `json:"deal_id"`
	Slot      uint32   `json:"slot"`
	LeafIndex uint64   `json:"leaf_index"`
	Leaf      string   `json:"leaf"`
	Siblings  []string `json:"siblings"`
}

// WriteProofs writes the JSON array `orama storage prove --file` reads.
// An empty list is refused, because that command refuses an empty file.
// This does not submit a transaction.
func WriteProofs(w io.Writer, proofs []types.ReplicaProof) error {
	if w == nil {
		return errors.New("proof writer is nil")
	}
	if len(proofs) == 0 {
		return errors.New("at least one proof is required")
	}
	rows := make([]proofFileRow, 0, len(proofs))
	for i, proof := range proofs {
		if proof.DealId == 0 {
			return fmt.Errorf("proof %d has no deal id", i)
		}
		if len(proof.Leaf) != piece.LeafSize {
			return fmt.Errorf("proof %d leaf is %d bytes, want %d", i, len(proof.Leaf), piece.LeafSize)
		}
		sibs := make([]string, 0, len(proof.Siblings))
		for j, sib := range proof.Siblings {
			if len(sib) != sha256.Size {
				return fmt.Errorf("proof %d sibling %d is %d bytes, want %d", i, j, len(sib), sha256.Size)
			}
			sibs = append(sibs, hex.EncodeToString(sib))
		}
		rows = append(rows, proofFileRow{
			DealID:    proof.DealId,
			Slot:      proof.Slot,
			LeafIndex: proof.LeafIndex,
			Leaf:      hex.EncodeToString(proof.Leaf),
			Siblings:  sibs,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rows); err != nil {
		return err
	}
	return nil
}
