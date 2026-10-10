package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/piece"
	storagekeeper "github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// proveEverySlot answers this epoch's challenge on every slot of the deal, as each slot's hot key.
func (c *wiringChain) proveEverySlot(dealID uint64, data []byte, byNode map[string]wiringNode) {
	c.t.Helper()
	c.write(func(ctx sdk.Context) {
		epoch, err := c.app.EmissionKeeper.CurrentEpoch(ctx)
		require.NoError(c.t, err)
		for i := uint32(0); i < 3; i++ {
			slot, err := c.app.StorageKeeper.Slots.Get(ctx, collectionsJoin(dealID, i))
			require.NoError(c.t, err)
			node := byNode[slot.NodeId]
			res, err := storagekeeper.NewQueryServer(c.app.StorageKeeper).Challenges(ctx, &storagetypes.QueryChallengesRequest{Epoch: epoch, NodeId: slot.NodeId})
			require.NoError(c.t, err)
			var leaf uint64
			var found bool
			for _, ch := range res.Challenges {
				if ch.DealId == dealID && ch.Slot == i {
					leaf, found = ch.LeafIndex, true
				}
			}
			require.True(c.t, found, "no challenge for %s in epoch %d", slot.NodeId, epoch)
			proof, err := piece.Prove(data, leaf)
			require.NoError(c.t, err)
			_, err = storagekeeper.NewMsgServer(c.app.StorageKeeper).SubmitProofs(ctx, &storagetypes.MsgSubmitProofs{
				Signer: node.hot.String(), NodeId: node.id,
				Proofs: []storagetypes.ReplicaProof{{DealId: dealID, Slot: i, LeafIndex: leaf, Leaf: proof.Leaf, Siblings: proof.Siblings}},
			})
			require.NoError(c.t, err)
		}
	})
}

// A probation node earns its first payouts with the real x/fees keeper and launch-default
// params: probation_deposit 1000 against a first credit of 900 (a 1000 protocol price less the
// 5% burn and 5% archive share). The deposit used to demand the whole 1000 from a balance of
// 900, which failed EndBlock and halted the chain. Now the chain keeps producing blocks and the
// deposit opens at the 900 the node has earned. (The second top-up, to 1000, is covered by the
// keeper tests: probation_expiry_epochs 4 graduates these nodes, and returns the deposit, first.)
func TestApp_probationNodeEarnsItsFirstPayoutsAndTheChainKeepsProducingBlocks(t *testing.T) {
	c := newWiringChain(t)
	nodes := map[string]wiringNode{}
	for _, n := range []wiringNode{
		c.addProbationNode("p1", "https://45.33.100.10:443", 15169),
		c.addProbationNode("p2", "https://93.184.113.10:443", 13335),
		c.addProbationNode("p3", "https://151.101.2.10:443", 16509),
	} {
		nodes[n.id] = n
	}
	c.blocks(1)
	for id := range nodes {
		state, err := c.app.StorageKeeper.Nodes.Get(c.app.NewContext(true), id)
		require.NoError(t, err)
		require.True(t, state.Probation, "%s is a probation node", id)
	}

	data := protocolPayload(3)
	var dealID uint64
	c.write(func(ctx sdk.Context) {
		id, err := c.app.StorageKeeper.CreateProtocolDeal(ctx, storagetypes.DealClass_DEAL_CLASS_PUBLIC_PIN, data,
			math.NewInt(storagetypes.DefaultProtocolPrice), 50)
		require.NoError(t, err)
		dealID = id
	})
	c.blocks(1)
	c.write(func(ctx sdk.Context) {
		for i := uint32(0); i < 3; i++ {
			slot, err := c.app.StorageKeeper.Slots.Get(ctx, collectionsJoin(dealID, i))
			require.NoError(t, err)
			require.NotEmpty(t, slot.NodeId, "slot %d is assigned to a probation node", i)
			node := nodes[slot.NodeId]
			_, err = storagekeeper.NewMsgServer(c.app.StorageKeeper).AcceptDeal(ctx, &storagetypes.MsgAcceptDeal{
				Signer: node.hot.String(), NodeId: node.id, DealId: dealID, Slot: i,
			})
			require.NoError(t, err)
		}
	})

	// held[node] is the deposit the node holds after each block, while it is on probation.
	held := map[string][]string{}
	observe := func() {
		ctx := c.app.NewContext(true)
		for id := range nodes {
			amount, found, err := c.app.FeesKeeper.DepositAmount(ctx, "storage/probation/"+id)
			require.NoError(t, err)
			if found && (len(held[id]) == 0 || held[id][len(held[id])-1] != amount.String()) {
				held[id] = append(held[id], amount.String())
			}
		}
	}
	for round := 0; round < 4; round++ {
		c.blocks(1) // opens the epoch's challenges; the block before closed and settled the last epoch.
		observe()
		c.proveEverySlot(dealID, data, nodes)
	}
	c.blocks(1) // the last epoch closes and its payouts settle. A failing EndBlock would fail here.
	observe()

	ctx := c.app.NewContext(true)
	for id, n := range nodes {
		require.Equal(t, []string{"900"}, held[id],
			"%s's first credit is 900, so its deposit opens at 900 rather than demanding probation_deposit's 1000", id)
		_, found, err := c.app.FeesKeeper.DepositAmount(ctx, "storage/probation/"+id)
		require.NoError(t, err)
		require.False(t, found, "%s outlived probation (probation_expiry_epochs 4) and recovered its deposit", id)
		state, err := c.app.StorageKeeper.Nodes.Get(ctx, id)
		require.NoError(t, err)
		require.True(t, state.Graduated && state.EverProved)
		earned, err := c.app.FeesKeeper.GetEarnings(ctx, n.operator)
		require.NoError(t, err)
		require.True(t, earned.IsPositive(), "%s keeps what it earned past the deposit", id)
	}
	inv, err := c.app.StorageKeeper.CheckInvariants(ctx)
	require.NoError(t, err)
	require.True(t, inv.EscrowConserved && inv.SubsidyWithinCeiling && inv.QueueWellFormed, inv.Detail)
	feesInv, err := c.app.FeesKeeper.CheckInvariants(ctx)
	require.NoError(t, err)
	require.True(t, feesInv.EarningsMatchModule && feesInv.DepositsMatchModule && feesInv.FeesBalance, feesInv.Detail)
}
