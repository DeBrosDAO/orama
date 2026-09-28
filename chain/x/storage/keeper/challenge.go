package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// SampleReplicaIndexes selects k distinct replica sequence numbers from
// [0, count) using a hash walk. It does not scan the replica set: the number
// of hashes is O(k), with a short sequential fill only when collisions exhaust
// the hash attempts (count close to k).
func SampleReplicaIndexes(count, k uint64, seed []byte) (idxs []uint64, steps int) {
	if count == 0 || k == 0 {
		return nil, 0
	}
	if k > count {
		k = count
	}
	picked := make(map[uint64]struct{}, k)
	idxs = make([]uint64, 0, k)
	limit := k * 8
	if limit < 16 {
		limit = 16
	}
	for i := uint64(0); uint64(len(idxs)) < k && i < limit; i++ {
		steps++
		sum := hashStep(seed, i)
		idx := modHash(sum, count)
		if _, ok := picked[idx]; ok {
			continue
		}
		picked[idx] = struct{}{}
		idxs = append(idxs, idx)
	}
	if uint64(len(idxs)) < k {
		start := uint64(0)
		if len(idxs) > 0 {
			start = idxs[0]
		}
		for seq := uint64(0); uint64(len(idxs)) < k && seq < count; seq++ {
			steps++
			idx := (start + seq) % count
			if _, ok := picked[idx]; ok {
				continue
			}
			picked[idx] = struct{}{}
			idxs = append(idxs, idx)
		}
	}
	return idxs, steps
}

func (k Keeper) openChallenges(ctx sdk.Context, epoch uint64) error {
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	nodes := map[string]struct{}{}
	if err := k.ReplicaCount.Walk(ctx, nil, func(nodeID string, _ uint64) (bool, error) {
		nodes[nodeID] = struct{}{}
		return false, nil
	}); err != nil {
		return err
	}
	if err := k.Rechallenge.Walk(ctx, nil, func(key collections.Pair[string, string]) (bool, error) {
		nodes[key.K1()] = struct{}{}
		return false, nil
	}); err != nil {
		return err
	}
	for nodeID := range nodes {
		if err := k.openNodeChallenges(ctx, epoch, nodeID, p.KC); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) openNodeChallenges(ctx sdk.Context, epoch uint64, nodeID string, kC uint64) error {
	want := map[string]types.SlotRef{}
	count, err := k.ReplicaCount.Get(ctx, nodeID)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	seqs, _ := SampleReplicaIndexes(count, kC, types.SampleSeed(epoch, nodeID))
	for _, seq := range seqs {
		ref, err := k.ReplicaAt.Get(ctx, collections.Join(nodeID, seq))
		if err != nil {
			return fmt.Errorf("failed to load replica %s/%d: %w", nodeID, seq, err)
		}
		want[challengeID(nodeID, ref.DealId, ref.Slot)] = ref
	}
	if err := k.Rechallenge.Walk(ctx, collections.NewPrefixedPairRange[string, string](nodeID), func(key collections.Pair[string, string]) (bool, error) {
		var dealID uint64
		var slot uint32
		if _, err := fmt.Sscanf(key.K2(), "%d/%d", &dealID, &slot); err != nil {
			return false, fmt.Errorf("bad rechallenge key %q: %w", key.K2(), err)
		}
		ref := types.SlotRef{DealId: dealID, Slot: slot}
		want[challengeID(nodeID, dealID, slot)] = ref
		return false, nil
	}); err != nil {
		return err
	}
	for _, ref := range want {
		slot, err := k.loadSlot(ctx, ref.DealId, ref.Slot)
		if err != nil {
			return err
		}
		if err := k.openSlotChallenge(ctx, epoch, slot); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) openSlotChallenge(ctx sdk.Context, epoch uint64, slot types.Slot) error {
	if !slot.Accepted || slot.NodeId == "" {
		return nil
	}
	deal, err := k.loadDeal(ctx, slot.DealId)
	if err != nil {
		return err
	}
	if deal.Status == types.DealStatus_DEAL_STATUS_REFUNDED || deal.Status == types.DealStatus_DEAL_STATUS_EXPIRED {
		return nil
	}
	if epoch < deal.StartEpoch || epoch >= deal.EndEpoch {
		return nil
	}
	key := collections.Join(epoch, challengeID(slot.NodeId, slot.DealId, slot.Index))
	has, err := k.Challenges.Has(ctx, key)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	leaf, err := piece.LeafIndex(types.LeafChallengeSeed(epoch, slot.DealId, slot.Index, slot.NodeId), slot.RealLeafCount)
	if err != nil {
		return fmt.Errorf("failed to draw leaf challenge: %w", err)
	}
	if leaf >= slot.RealLeafCount {
		return fmt.Errorf("challenge %d landed on padding (real %d)", leaf, slot.RealLeafCount)
	}
	return k.Challenges.Set(ctx, key, types.ChallengeRecord{LeafIndex: leaf, Proved: false})
}

// SubmitProofs checks hot-key proofs against this epoch's challenges. Several
// transactions per epoch are allowed; each proof covers one challenged slot.
func (k Keeper) SubmitProofs(ctx sdk.Context, msg *types.MsgSubmitProofs) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	if err := k.requireHotKey(ctx, msg.NodeId, msg.Signer); err != nil {
		return err
	}
	epoch, err := k.currentEpoch(ctx)
	if err != nil {
		return err
	}
	for i := range msg.Proofs {
		proof := msg.Proofs[i]
		slot, err := k.loadSlot(ctx, proof.DealId, proof.Slot)
		if err != nil {
			return err
		}
		if slot.NodeId != msg.NodeId || !slot.Accepted {
			return fmt.Errorf("slot %d/%d is not an active replica of %s", proof.DealId, proof.Slot, msg.NodeId)
		}
		key := collections.Join(epoch, challengeID(msg.NodeId, proof.DealId, proof.Slot))
		rec, err := k.Challenges.Get(ctx, key)
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				return fmt.Errorf("slot %d/%d has no challenge in epoch %d", proof.DealId, proof.Slot, epoch)
			}
			return err
		}
		if rec.Proved {
			return fmt.Errorf("slot %d/%d was already proved in epoch %d", proof.DealId, proof.Slot, epoch)
		}
		if proof.LeafIndex != rec.LeafIndex {
			return fmt.Errorf("proof leaf %d does not match challenge %d", proof.LeafIndex, rec.LeafIndex)
		}
		commitment := piece.Commitment{
			Root:            slot.PieceRoot,
			RealLeafCount:   slot.RealLeafCount,
			PaddedLeafCount: slot.PaddedLeafCount,
		}
		inc := piece.Proof{Index: proof.LeafIndex, Leaf: proof.Leaf, Siblings: proof.Siblings}
		if err := piece.VerifyChallenge(commitment, inc); err != nil {
			return fmt.Errorf("slot %d/%d proof rejected: %w", proof.DealId, proof.Slot, err)
		}
		rec.Proved = true
		if err := k.Challenges.Set(ctx, key, rec); err != nil {
			return err
		}
	}
	return nil
}
