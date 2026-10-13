package provider

import (
	"errors"
	"os"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// ReplicaProof builds the proof x/storage checks for one challenged slot.
// The leaf index is piece.LeafIndex of the chain's LeafChallengeSeed, so a
// padding leaf is never selected. This does not submit a transaction.
func (s *Store) ReplicaProof(cid string, epoch, dealID uint64, slot uint32, nodeID string) (types.ReplicaProof, error) {
	if err := validCID(cid); err != nil {
		return types.ReplicaProof{}, err
	}
	if dealID == 0 {
		return types.ReplicaProof{}, errors.New("deal id is empty")
	}
	if nodeID == "" {
		return types.ReplicaProof{}, errors.New("node id is empty")
	}
	data, err := os.ReadFile(s.piecePath(cid))
	if err != nil {
		return types.ReplicaProof{}, err
	}
	committed, err := piece.Commit(data)
	if err != nil {
		return types.ReplicaProof{}, err
	}
	index, err := piece.LeafIndex(types.LeafChallengeSeed(epoch, dealID, slot, nodeID), committed.RealLeafCount)
	if err != nil {
		return types.ReplicaProof{}, err
	}
	proof, err := piece.Prove(data, index)
	if err != nil {
		return types.ReplicaProof{}, err
	}
	if err := piece.VerifyChallenge(committed, proof); err != nil {
		return types.ReplicaProof{}, err
	}
	return types.ReplicaProof{
		DealId:    dealID,
		Slot:      slot,
		LeafIndex: proof.Index,
		Leaf:      proof.Leaf,
		Siblings:  proof.Siblings,
	}, nil
}
