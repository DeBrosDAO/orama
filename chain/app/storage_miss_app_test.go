package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/piece"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	storagekeeper "github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// acceptSlots has every assigned slot of the deal accepted by its node's hot key.
func (c *wiringChain) acceptSlots(dealID uint64, nodes map[string]wiringNode) {
	c.t.Helper()
	c.write(func(ctx sdk.Context) {
		for i := uint32(0); i < 3; i++ {
			slot, err := c.app.StorageKeeper.Slots.Get(ctx, collectionsJoin(dealID, i))
			require.NoError(c.t, err)
			require.NotEmpty(c.t, slot.NodeId, "slot %d is assigned", i)
			node := nodes[slot.NodeId]
			_, err = storagekeeper.NewMsgServer(c.app.StorageKeeper).AcceptDeal(ctx, &storagetypes.MsgAcceptDeal{
				Signer: node.hot.String(), NodeId: node.id, DealId: dealID, Slot: i,
			})
			require.NoError(c.t, err)
		}
	})
}

func (c *wiringChain) requireModuleInvariants() {
	c.t.Helper()
	ctx := c.app.NewContext(true)
	st, err := c.app.StorageKeeper.CheckInvariants(ctx)
	require.NoError(c.t, err)
	require.True(c.t, st.EscrowConserved && st.SubsidyWithinCeiling && st.DistinctOperators && st.ReservedWithinDeclared && st.QueueWellFormed, st.Detail)
	nd, err := c.app.NodesKeeper.CheckInvariants(ctx)
	require.NoError(c.t, err)
	require.True(c.t, nd.BalanceMatches && nd.ActiveRolesBonded && nd.CapacityBacked, nd.Detail)
}

// heldBy reports the slots of a deal that a node still holds.
func (c *wiringChain) heldBy(dealID uint64, nodeID string) int {
	c.t.Helper()
	n := 0
	for _, holder := range c.slotHolders(dealID) {
		if holder == nodeID {
			n++
		}
	}
	return n
}

// Probation nodes have no bond. Before, the missing bond failed the miss row, the whole row was
// rolled back, and a probation node that never proves kept its protocol slot forever. Here three
// probation nodes take a protocol deal and never prove; through real FinalizeBlocks each is
// evicted from the slot it did not serve, the chain keeps producing blocks and every invariant holds.
func TestApp_probationNodesThatNeverProveAreEvicted(t *testing.T) {
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
	var dealID uint64
	c.write(func(ctx sdk.Context) {
		id, err := c.app.StorageKeeper.CreateProtocolDeal(ctx, storagetypes.DealClass_DEAL_CLASS_PUBLIC_PIN, protocolPayload(3),
			math.NewInt(storagetypes.DefaultProtocolPrice), 50)
		require.NoError(c.t, err)
		dealID = id
	})
	c.blocks(1)
	c.acceptSlots(dealID, nodes)
	original := c.slotHolders(dealID)

	c.blocks(12)

	now := c.slotHolders(dealID)
	for i, holder := range original {
		require.NotEqual(t, holder, now[i], "slot %d is still held by %s, which never proved", i, holder)
	}
	require.Zero(t, c.failures("p1", storagekeeper.FailureKindSettlement), "no miss row was rolled back")
	c.requireModuleInvariants()
}

func (c *wiringChain) failures(nodeID, kind string) uint64 {
	c.t.Helper()
	n, err := c.app.StorageKeeper.FailureCount(c.app.NewContext(true), nodeID, kind)
	require.NoError(c.t, err)
	return n
}

// A bonded node whose declaration is all the bond backs and whose capacity is all reserved: the
// slash shrinks the backing below what it holds. Through real FinalizeBlocks the slash is applied
// (bond burned, declared capacity clamped), the replicas that no longer fit are released, the node
// is evicted at the miss threshold, and x/nodes and x/storage invariants hold throughout.
func TestApp_fullCapacityNodeThatMissesIsSlashedAndEvicted(t *testing.T) {
	const fullBytes = uint64(1 << 20)
	c := newWiringChainWith(t, func(gs *nodestypes.GenesisState) {
		// One orama of bond backs exactly wiringCapacity (1 MiB), so a slash always shrinks the backing.
		gs.Params.BondPerGib = math.NewInt(params.NoramaPerOrama).MulRaw(1024)
	})
	nodes := map[string]wiringNode{}
	for _, n := range []wiringNode{
		c.addStorageNode("n1", "https://45.33.100.10:443", 15169),
		c.addStorageNode("n2", "https://93.184.113.10:443", 13335),
		c.addStorageNode("n3", "https://151.101.2.10:443", 16509),
	} {
		nodes[n.id] = n
	}
	c.blocks(1)
	node, err := c.app.NodesKeeper.GetNode(c.app.NewContext(true), "n1")
	require.NoError(t, err)
	require.Equal(t, fullBytes, node.DeclaredCapacityBytes)

	client := sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address())
	var dealID uint64
	c.write(func(ctx sdk.Context) {
		funds := sdk.NewCoins(sdk.NewCoin(params.BaseDenom, math.NewInt(params.NoramaPerOrama).MulRaw(10)))
		require.NoError(c.t, c.app.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, funds))
		require.NoError(c.t, c.app.BankKeeper.SendCoinsFromModuleToAccount(ctx, emissiontypes.ModuleName, client, funds))
		pieces := make([]storagetypes.PieceCommitment, 3)
		for i := range pieces {
			data := make([]byte, fullBytes)
			data[0] = byte(i + 1)
			cm, err := piece.Commit(data)
			require.NoError(c.t, err)
			pieces[i] = storagetypes.PieceCommitment{
				Root: cm.Root, RealLeafCount: cm.RealLeafCount, PaddedLeafCount: cm.PaddedLeafCount, PieceBytes: uint64(len(data)),
			}
		}
		nonce := make([]byte, storagetypes.NonceLen)
		nonce[0] = 9
		res, err := storagekeeper.NewMsgServer(c.app.StorageKeeper).CreateDeal(ctx, &storagetypes.MsgCreateDeal{
			Signer: client.String(), Class: storagetypes.DealClass_DEAL_CLASS_PRIVATE, DealNonce: nonce,
			Replicas: 3, PricePerEpoch: math.NewInt(1000), DurationEpochs: 100, Pieces: pieces,
		})
		require.NoError(c.t, err)
		dealID = res.DealId
	})
	c.blocks(1)
	c.acceptSlots(dealID, nodes)
	original := c.slotHolders(dealID)
	bondBefore := map[string]math.Int{}
	for id := range nodes {
		n, err := c.app.NodesKeeper.GetNode(c.app.NewContext(true), id)
		require.NoError(t, err)
		bondBefore[id] = n.Bonds[0].Amount
	}

	for i := 0; i < 12; i++ {
		c.blocks(1)
		c.requireModuleInvariants() // reserved never exceeds the clamped declaration, block by block.
	}

	now := c.slotHolders(dealID)
	for i, holder := range original {
		require.NotEqual(t, holder, now[i], "slot %d is still held by %s, which never proved", i, holder)
		n, err := c.app.NodesKeeper.GetNode(c.app.NewContext(true), holder)
		require.NoError(t, err)
		require.True(t, n.Bonds[0].Amount.LT(bondBefore[holder]), "%s was slashed", holder)
		require.Less(t, n.DeclaredCapacityBytes, fullBytes, "%s's declaration was clamped to what the smaller bond backs", holder)
	}
	c.requireModuleInvariants()
}

// A settlement row that can never be applied (here its operator address is unusable) is retried
// and then dropped, and the mint reserved for it is burned. Through real FinalizeBlocks the storage
// module account still holds exactly the mint of the rows still queued and x/emission's supply
// invariant still holds.
func TestApp_aDroppedSettlementKeepsTheStorageAndEmissionSupplyInvariants(t *testing.T) {
	c := newWiringChain(t)
	nodes := map[string]wiringNode{}
	for _, n := range []wiringNode{
		c.addProbationNode("p1", "https://45.33.100.10:443", 15169),
		c.addProbationNode("p2", "https://93.184.113.10:443", 13335),
		c.addProbationNode("p3", "https://151.101.2.10:443", 16509),
	} {
		nodes[n.id] = n
	}
	c.blocks(2)
	var dealID uint64
	c.write(func(ctx sdk.Context) {
		id, err := c.app.StorageKeeper.CreateProtocolDeal(ctx, storagetypes.DealClass_DEAL_CLASS_PUBLIC_PIN, protocolPayload(3),
			math.NewInt(storagetypes.DefaultProtocolPrice), 50)
		require.NoError(t, err)
		dealID = id
	})
	c.blocks(1)
	holder := c.slotHolders(dealID)[0]

	// The harness funds operators with mints that bypass x/emission. Record that supply as
	// genesis supply so the real supply invariant can be checked exactly.
	c.write(func(ctx sdk.Context) {
		state, err := c.app.EmissionKeeper.EpochState.Get(ctx)
		require.NoError(t, err)
		expected := state.GenesisSupply.Add(state.CumulativeMinted).Add(state.CumulativeDevelopmentMinted).
			Add(state.CumulativeServiceMinted).Sub(state.CumulativeBurned)
		state.GenesisSupply = state.GenesisSupply.Add(c.app.BankKeeper.GetSupply(ctx, params.BaseDenom).Amount.Sub(expected))
		require.NoError(t, c.app.EmissionKeeper.EpochState.Set(ctx, state))
	})
	c.blocks(1)

	// A row of a past epoch whose mint was reserved when that epoch closed, as closeEpoch does.
	reserved := math.NewInt(1234)
	c.write(func(ctx sdk.Context) {
		epoch, err := c.app.EmissionKeeper.CurrentEpoch(ctx)
		require.NoError(t, err)
		require.Greater(t, epoch, uint64(1))
		require.NoError(t, c.app.EmissionKeeper.MintStorageService(ctx, epoch-1, reserved))
		tail, err := c.app.StorageKeeper.QueueTail.Get(ctx)
		require.NoError(t, err)
		require.NoError(t, c.app.StorageKeeper.Queue.Set(ctx, tail, storagetypes.Settlement{
			Seq: tail, Epoch: epoch - 1, DealId: dealID, Slot: 0, NodeId: holder, Operator: "not-an-address", Proved: true,
			EscrowPay: math.ZeroInt(), MintPay: reserved, ArchiveTopUp: math.ZeroInt(),
		}))
		require.NoError(t, c.app.StorageKeeper.QueueTail.Set(ctx, tail+1))
		require.NoError(t, c.app.StorageKeeper.QueuePending.Set(ctx, dealID, 1))
	})
	requireSupply := func() {
		ctx := c.app.NewContext(true)
		st, err := c.app.StorageKeeper.CheckInvariants(ctx)
		require.NoError(t, err)
		require.True(t, st.SubsidyWithinCeiling && st.QueueWellFormed, st.Detail)
		detail, brokenSupply := c.app.EmissionKeeper.CheckSupplyInvariant(ctx)
		require.False(t, brokenSupply, detail)
	}
	requireSupply()

	for i := uint32(1); i < storagetypes.MaxSettlementAttempts; i++ {
		c.blocks(1)
		queued, err := storagekeeper.NewQueryServer(c.app.StorageKeeper).Queue(c.app.NewContext(true), &storagetypes.QueryQueueRequest{})
		require.NoError(t, err)
		require.Equal(t, uint64(1), queued.Pending, "the row is still retried on attempt %d", i)
		require.Equal(t, uint64(i), c.failures(holder, storagekeeper.FailureKindSettlement))
		requireSupply()
	}
	c.blocks(1)

	queued, err := storagekeeper.NewQueryServer(c.app.StorageKeeper).Queue(c.app.NewContext(true), &storagetypes.QueryQueueRequest{})
	require.NoError(t, err)
	require.Zero(t, queued.Pending, "the row is dropped on its last attempt")
	held := c.app.BankKeeper.GetBalance(c.app.NewContext(true), authtypes.NewModuleAddress(storagetypes.ModuleName), params.BaseDenom).Amount
	require.True(t, held.IsZero(), "the mint reserved for the dropped row is burned, storage holds %s", held)
	requireSupply()
	c.blocks(1)
	requireSupply()
}
