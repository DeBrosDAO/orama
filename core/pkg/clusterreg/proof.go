package clusterreg

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	// SubmitProofsTypeURL is the Any type URL of orama.storage.v1.MsgSubmitProofs.
	SubmitProofsTypeURL = "/orama.storage.v1.MsgSubmitProofs"

	proofLeafSize    = 1024
	proofSiblingSize = 32
)

// Proof is one orama.storage.v1.ReplicaProof.
type Proof struct {
	DealID    uint64
	Slot      uint32
	LeafIndex uint64
	Leaf      []byte
	Siblings  [][]byte
}

// Proofs is MsgSubmitProofs. The signer is the node's hot key.
type Proofs struct {
	Signer string
	NodeID string
	Proofs []Proof
}

type proofJSON struct {
	DealID    uint64   `json:"deal_id"`
	Slot      uint32   `json:"slot"`
	LeafIndex uint64   `json:"leaf_index"`
	Leaf      string   `json:"leaf"`
	Siblings  []string `json:"siblings"`
}

// ParseProofs reads a JSON array of proofs. Leaf and siblings are hex.
func ParseProofs(raw []byte) ([]Proof, error) {
	var rows []proofJSON
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("proof file is not a JSON array")
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("at least one proof is required")
	}
	out := make([]Proof, 0, len(rows))
	for i, row := range rows {
		leaf, err := hex.DecodeString(row.Leaf)
		if err != nil {
			return nil, fmt.Errorf("proof %d leaf is not hex", i)
		}
		sibs := make([][]byte, 0, len(row.Siblings))
		for j, sib := range row.Siblings {
			b, err := hex.DecodeString(sib)
			if err != nil {
				return nil, fmt.Errorf("proof %d sibling %d is not hex", i, j)
			}
			sibs = append(sibs, b)
		}
		out = append(out, Proof{
			DealID: row.DealID, Slot: row.Slot, LeafIndex: row.LeafIndex, Leaf: leaf, Siblings: sibs,
		})
	}
	return out, nil
}

// ValidateProofs checks the stateless rules of MsgSubmitProofs.
// ValidateBasic only checks the leaf width. The verifier also requires each
// sibling to be 32 bytes, and so does this check.
func ValidateProofs(p Proofs) error {
	if _, err := CanonicalAccount(p.Signer); err != nil {
		return fmt.Errorf("signer: %w", err)
	}
	if err := storageID("node id", p.NodeID, false); err != nil {
		return err
	}
	if len(p.Proofs) == 0 {
		return fmt.Errorf("at least one proof is required")
	}
	for i, proof := range p.Proofs {
		if proof.DealID == 0 {
			return fmt.Errorf("proof %d has no deal id", i)
		}
		if len(proof.Leaf) != proofLeafSize {
			return fmt.Errorf("proof %d leaf is %d bytes, want %d", i, len(proof.Leaf), proofLeafSize)
		}
		for j, sib := range proof.Siblings {
			if len(sib) != proofSiblingSize {
				return fmt.Errorf("proof %d sibling %d is %d bytes, want %d", i, j, len(sib), proofSiblingSize)
			}
		}
	}
	return nil
}

// EncodeProofs is the protobuf orama.storage.v1.MsgSubmitProofs.
func EncodeProofs(p Proofs) []byte {
	out := appendStringField(nil, 1, p.Signer)
	if p.NodeID != "" {
		out = appendStringField(out, 2, p.NodeID)
	}
	for i := range p.Proofs {
		out = appendBytesField(out, 3, encodeReplicaProof(p.Proofs[i]))
	}
	return out
}

func encodeReplicaProof(p Proof) []byte {
	var out []byte
	if p.DealID != 0 {
		out = appendUvarintField(out, 1, p.DealID)
	}
	if p.Slot != 0 {
		out = appendUvarintField(out, 2, uint64(p.Slot))
	}
	if p.LeafIndex != 0 {
		out = appendUvarintField(out, 3, p.LeafIndex)
	}
	if len(p.Leaf) != 0 {
		out = appendBytesField(out, 4, p.Leaf)
	}
	for _, sib := range p.Siblings {
		out = appendBytesField(out, 5, sib)
	}
	return out
}
