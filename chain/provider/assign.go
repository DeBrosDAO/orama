package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// Assignment is the stored CID that fills one deal slot. The chain's replica
// sequence is not kept here. The provider answers the challenge list the
// chain already opened.
type Assignment struct {
	CID    string `json:"cid"`
	DealID uint64 `json:"deal_id"`
	Slot   uint32 `json:"slot"`
}

// Bind records that cid fills dealID/slot. The piece must already be stored.
// Binding the same CID again is a no-op. A different CID for that slot is refused.
func (s *Store) Bind(cid string, dealID uint64, slot uint32) error {
	if err := validCID(cid); err != nil {
		return err
	}
	if dealID == 0 {
		return errors.New("deal id is empty")
	}
	if !s.Has(cid) {
		return fmt.Errorf("cid %q is not stored", cid)
	}
	cur, ok, err := s.Lookup(dealID, slot)
	if err != nil {
		return err
	}
	if ok {
		if cur == cid {
			return nil
		}
		return fmt.Errorf("deal %d slot %d is already bound to %s", dealID, slot, cur)
	}
	body, err := json.Marshal(Assignment{CID: cid, DealID: dealID, Slot: slot})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "assignments"), 0o700); err != nil {
		return err
	}
	path := s.assignPath(dealID, slot)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Lookup returns the CID bound to dealID/slot. ok is false when nothing is bound.
func (s *Store) Lookup(dealID uint64, slot uint32) (string, bool, error) {
	body, err := os.ReadFile(s.assignPath(dealID, slot))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	var rec Assignment
	if err := json.Unmarshal(body, &rec); err != nil {
		return "", false, fmt.Errorf("deal %d slot %d: %w", dealID, slot, err)
	}
	if rec.DealID != dealID || rec.Slot != slot || rec.CID == "" {
		return "", false, fmt.Errorf("deal %d slot %d binding does not match the file", dealID, slot)
	}
	if err := validCID(rec.CID); err != nil {
		return "", false, err
	}
	return rec.CID, true, nil
}

// AnswerChallenges builds proofs for the unproved challenges this node holds.
// A proved challenge is skipped. A challenge with no local piece is listed in
// missing and is not an error. A leaf index that does not match the chain's
// challenge is an error. This does not submit a transaction.
func (s *Store) AnswerChallenges(epoch uint64, nodeID string, challenges []types.Challenge) ([]types.ReplicaProof, []types.Challenge, error) {
	if nodeID == "" {
		return nil, nil, errors.New("node id is empty")
	}
	var proofs []types.ReplicaProof
	var missing []types.Challenge
	for _, ch := range challenges {
		if ch.Proved {
			continue
		}
		if ch.DealId == 0 {
			return nil, nil, errors.New("challenge has no deal id")
		}
		cid, ok, err := s.Lookup(ch.DealId, ch.Slot)
		if err != nil {
			return nil, nil, err
		}
		if !ok || !s.Has(cid) {
			missing = append(missing, ch)
			continue
		}
		proof, err := s.ReplicaProof(cid, epoch, ch.DealId, ch.Slot, nodeID)
		if err != nil {
			return nil, nil, err
		}
		if proof.LeafIndex != ch.LeafIndex {
			return nil, nil, fmt.Errorf("deal %d slot %d leaf %d does not match challenge %d", ch.DealId, ch.Slot, proof.LeafIndex, ch.LeafIndex)
		}
		proofs = append(proofs, proof)
	}
	return proofs, missing, nil
}

func (s *Store) assignPath(dealID uint64, slot uint32) string {
	return filepath.Join(s.dir, "assignments", fmt.Sprintf("%d-%d.json", dealID, slot))
}
