package keeper_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// storageFailureReasons is the error attribute of every storage_item_failed event so far.
func storageFailureReasons(f *fixture) []string {
	var out []string
	for _, e := range f.Ctx.EventManager().Events() {
		if e.Type != "storage_item_failed" {
			continue
		}
		for _, a := range e.Attributes {
			if a.Key == "error" {
				out = append(out, a.Value)
			}
		}
	}
	return out
}

// When several of one node's slots cannot be challenged, the recorded failure names the same slot
// on every node: the first in challenge-id order. openNodeChallenges used to visit the slots in
// map order, so each validator recorded whichever one it met first. Two corrupt rechallenges make
// the order matter; twelve epochs make a map order that happens to agree every time improbable
// (one in 2^12).
func TestOpenChallenges_namesTheSameFailingSlotEveryTime(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 100_000_000)
	data := payload(7)
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1_000, 6, []types.PieceCommitment{
		commit(t, data), commit(t, data), commit(t, data),
	})
	f.end(t)
	f.begin(t)
	f.acceptAll(t, id, 3)

	// The node holding slot 2 is indexed for slots 0 and 1 too, which other nodes hold.
	other := f.slot(t, id, 2).NodeId
	for slot := range 2 {
		require.NotEqual(t, other, f.slot(t, id, uint32(slot)).NodeId)
		require.NoError(t, f.Keeper.Rechallenge.Set(f.Ctx, collections.Join(other, fmt.Sprintf("%d/%d", id, slot))))
	}

	f.end(t)
	const epochs = 12
	for epoch := uint64(2); epoch < 2+epochs; epoch++ {
		f.Emission.epoch = epoch
		f.Ctx = f.Ctx.WithBlockHeight(f.height + int64(epoch))
		require.NoError(t, f.Keeper.BeginBlock(f.Ctx), "one node's corrupt index must not fail BeginBlock")
	}

	reasons := storageFailureReasons(f)
	require.Len(t, reasons, epochs, "one failure per epoch for the corrupt node")
	first := fmt.Sprintf("deal %d slot 0 is indexed for node %s", id, other)
	for _, reason := range reasons {
		require.Contains(t, reason, first, "the failure names the first slot in challenge-id order")
	}
}
