package keeper_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/repair"
	"github.com/DeBrosOfficial/network/chain/storagekey"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// repairChain is the delegate's view of the simulated chain. Providers are
// found by node id in urls, as x/nodes endpoints would name them.
type repairChain struct {
	sim  *chainSim
	urls map[string]string
}

func (c repairChain) Deal(_ context.Context, id uint64) (types.Deal, error) {
	res, err := c.sim.f.Query.Deal(c.sim.f.Ctx, &types.QueryDealRequest{DealId: id})
	if err != nil {
		return types.Deal{}, err
	}
	return res.Deal, nil
}

func (c repairChain) Slot(ctx context.Context, dealID uint64, slot uint32) (types.Slot, error) {
	return nodeChain{c.sim}.Slot(ctx, dealID, slot)
}

func (c repairChain) ProviderURL(_ context.Context, nodeID string) (string, error) {
	return repair.FirstHTTPEndpoint(nodeID, []string{"/dns4/x/tcp/1", c.urls[nodeID]})
}

func TestRepairChaos_killedProviderIsEvictedAndTheDelegateRestoresTheReplica(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) {
		gs.Params.MissThreshold = 2
		gs.Params.KC = 4
	})
	f.threeNodes(t, 1<<20)
	f.addNode(t, "n4", "10.4.0.0/16", 4, 1<<20, false)
	sim := newChainSim(t, f)
	providers := startProviders(t, sim, "n1", "n2", "n3", "n4")
	urls := map[string]string{}
	for id, p := range providers {
		srv := httptest.NewServer(p.http)
		t.Cleanup(srv.Close)
		urls[id] = srv.URL
	}

	delegateOp := acc(77).String()
	seed := bytes.Repeat([]byte{0x33}, 32)
	nonce := bytesOf(types.NonceLen, 0x42)
	inner := bytes.Repeat([]byte("sealed inner blob "), 400)
	var slotBytes [][]byte
	var pieces []types.PieceCommitment
	for j := uint32(0); j < 3; j++ {
		b, err := storagekey.Apply(seed, nonce, j, inner)
		require.NoError(t, err)
		slotBytes = append(slotBytes, b)
		pieces = append(pieces, commit(t, b))
	}
	client := acc(9)
	f.fund(client, 10_000_000)
	var dealID uint64
	require.NoError(t, sim.block(func() error {
		res, err := f.Msg.CreateDeal(f.Ctx, &types.MsgCreateDeal{
			Signer: client.String(), Class: types.DealClass_DEAL_CLASS_PRIVATE, DealNonce: nonce,
			RepairDelegate: delegateOp, Replicas: 3, PricePerEpoch: math.NewInt(1000),
			DurationEpochs: 50, Pieces: pieces,
		})
		if err == nil {
			dealID = res.DealId
		}
		return err
	}))
	require.NoError(t, sim.block(nil))

	stepAll(t, providers)
	for j := uint32(0); j < 3; j++ {
		slot := f.slot(t, dealID, j)
		require.Equal(t, http.StatusNoContent, upload(providers[slot.NodeId], slotBytes[j], slot.PieceRoot))
	}
	stepAll(t, providers)

	victim := f.slot(t, dealID, 0)
	require.True(t, victim.Accepted)
	delete(providers, victim.NodeId)
	delegate, err := repair.New(repairChain{sim: sim, urls: urls}, repair.HTTP{Client: http.DefaultClient}, delegateOp)
	require.NoError(t, err)

	var repaired []repair.Repaired
	for epoch := uint64(2); epoch <= 12 && len(repaired) == 0; epoch++ {
		f.Emission.epoch = epoch
		require.NoError(t, sim.block(nil))
		stepAll(t, providers)
		done, err := delegate.RepairDeal(context.Background(), dealID, seed)
		if err != nil {
			require.ErrorIs(t, err, repair.ErrNotReady, "only a provider that has not read its assignment may refuse")
		}
		repaired = append(repaired, done...)
	}
	require.Len(t, repaired, 1, "the evicted slot was rebuilt once")
	require.Equal(t, uint32(0), repaired[0].Slot)
	newNode := repaired[0].Provider
	require.NotEqual(t, victim.NodeId, newNode)

	stepAll(t, providers)
	require.True(t, f.slot(t, dealID, 0).Accepted, "the new provider accepted the rebuilt replica")
	f.Emission.epoch++
	require.NoError(t, sim.block(nil))
	stepAll(t, providers)
	ch, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{Epoch: f.Emission.epoch, NodeId: newNode})
	require.NoError(t, err)
	found := false
	for _, c := range ch.Challenges {
		if c.DealId == dealID && c.Slot == 0 {
			found = true
			require.True(t, c.Proved, "the new provider proves the repaired slot")
		}
	}
	require.True(t, found, "the repaired slot is challenged")
	for j := uint32(0); j < 3; j++ {
		require.NotEqual(t, delegateOp, f.slot(t, dealID, j).Operator, "the delegate's operator holds no slot")
	}
	rebuilt, err := providers[newNode].store.Assignments()
	require.NoError(t, err)
	require.NotEmpty(t, rebuilt)
	c, err := piece.Commit(slotBytes[0])
	require.NoError(t, err)
	require.Equal(t, c.Root, f.slot(t, dealID, 0).PieceRoot)
}
