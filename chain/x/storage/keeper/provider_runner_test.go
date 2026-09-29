package keeper_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/provider"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// chainSim runs the storage keeper fixture as a chain: every block gets a
// fresh event manager, and a submitted message is delivered in a block of
// its own, the way a node includes a broadcast transaction.
type chainSim struct {
	t          *testing.T
	f          *fixture
	events     map[int64][]abci.Event
	drop       int
	failAccept bool
	failSlot   map[[2]uint64]bool
}

func newChainSim(t *testing.T, f *fixture) *chainSim {
	return &chainSim{t: t, f: f, events: map[int64][]abci.Event{}}
}

func (s *chainSim) block(fn func() error) error {
	s.f.Ctx = s.f.Ctx.WithEventManager(sdk.NewEventManager())
	s.f.begin(s.t)
	var err error
	if fn != nil {
		err = fn()
	}
	s.f.end(s.t)
	s.events[s.f.height] = s.f.Ctx.EventManager().ABCIEvents()
	return err
}

// nodeChain is one node's view of the simulated chain.
type nodeChain struct{ sim *chainSim }

func (c nodeChain) LatestHeight(context.Context) (int64, error) { return c.sim.f.height, nil }

func (c nodeChain) BlockEvents(_ context.Context, h int64) ([]abci.Event, error) {
	return c.sim.events[h], nil
}

func (c nodeChain) Params(context.Context) (types.Params, error) {
	return c.sim.f.Keeper.Params.Get(c.sim.f.Ctx)
}

func (c nodeChain) Slot(_ context.Context, dealID uint64, slot uint32) (types.Slot, error) {
	if c.sim.failSlot[[2]uint64{dealID, uint64(slot)}] {
		return types.Slot{}, fmt.Errorf("rpc timeout reading deal %d slot %d", dealID, slot)
	}
	res, err := c.sim.f.Query.Slot(c.sim.f.Ctx, &types.QuerySlotRequest{DealId: dealID, Slot: slot})
	if err != nil {
		return types.Slot{}, err
	}
	return res.Slot, nil
}

func (c nodeChain) CurrentEpoch(context.Context) (uint64, error) { return c.sim.f.Emission.epoch, nil }

func (c nodeChain) Challenges(_ context.Context, epoch uint64, nodeID string) ([]types.Challenge, error) {
	res, err := c.sim.f.Query.Challenges(c.sim.f.Ctx, &types.QueryChallengesRequest{Epoch: epoch, NodeId: nodeID})
	if err != nil {
		return nil, err
	}
	return res.Challenges, nil
}

func (c nodeChain) Balance(_ context.Context, addr string) (math.Int, error) {
	return c.sim.f.Bank.balanceOf(addr), nil
}

func (c nodeChain) Submit(_ context.Context, msgs ...sdk.Msg) error {
	if c.sim.drop > 0 {
		c.sim.drop--
		return node.ErrNotIncluded
	}
	if _, ok := msgs[0].(*types.MsgAcceptDeal); ok && c.sim.failAccept {
		return fmt.Errorf("accept rejected in CheckTx")
	}
	return c.sim.block(func() error {
		for _, msg := range msgs {
			var err error
			switch m := msg.(type) {
			case *types.MsgAcceptDeal:
				_, err = c.sim.f.Msg.AcceptDeal(c.sim.f.Ctx, m)
			case *types.MsgDeclineDeal:
				_, err = c.sim.f.Msg.DeclineDeal(c.sim.f.Ctx, m)
			case *types.MsgSubmitProofs:
				_, err = c.sim.f.Msg.SubmitProofs(c.sim.f.Ctx, m)
			default:
				err = fmt.Errorf("unexpected message %T", msg)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

type simProvider struct {
	store  *provider.Store
	runner *provider.Runner
	http   *provider.Retrieval
	state  string
}

func startProviders(t *testing.T, sim *chainSim, ids ...string) map[string]*simProvider {
	t.Helper()
	out := map[string]*simProvider{}
	for _, id := range ids {
		dir := t.TempDir()
		store, err := provider.Open(filepath.Join(dir, "store"), nil, nil)
		require.NoError(t, err)
		p := &simProvider{store: store, state: filepath.Join(dir, "state.json")}
		p.runner = newSimRunner(t, sim, store, id, p.state)
		p.http, err = provider.NewRetrieval(store, 1000, 1000, 16)
		require.NoError(t, err)
		require.NoError(t, p.http.AcceptUploads(1<<20, p.runner.Assigned))
		out[id] = p
	}
	return out
}

func newSimRunner(t *testing.T, sim *chainSim, store *provider.Store, id, state string) *provider.Runner {
	t.Helper()
	r, err := provider.NewRunner(store, nodeChain{sim}, provider.Config{
		NodeID: id, Signer: sim.f.Nodes.byID[id].hot.String(), StatePath: state,
		MonitorPath: state + ".monitor", StartHeight: 1,
	})
	require.NoError(t, err)
	return r
}

func upload(p *simProvider, data, root []byte) int {
	name := hex.EncodeToString(root)
	req := httptest.NewRequest(http.MethodPost, "/pieces/"+name, bytes.NewReader(data))
	req.RemoteAddr = "192.0.2.10:5000"
	req.Header.Set("X-Piece-Root", name)
	rec := httptest.NewRecorder()
	p.http.ServeHTTP(rec, req)
	return rec.Code
}

func stepAll(t *testing.T, providers map[string]*simProvider) {
	t.Helper()
	for id, p := range providers {
		require.NoErrorf(t, p.runner.Step(context.Background()), "node %s", id)
	}
}

func openDeal(t *testing.T, sim *chainSim, duration int64) (uint64, [][]byte, []types.PieceCommitment) {
	return openDealFilled(t, sim, duration, 1)
}

// openDealFilled opens a three-replica deal whose pieces are payloads fill, fill+1, fill+2.
func openDealFilled(t *testing.T, sim *chainSim, duration int64, fill byte) (uint64, [][]byte, []types.PieceCommitment) {
	t.Helper()
	client := acc(9)
	sim.f.fund(client, 1_000_000)
	data := [][]byte{payload(fill), payload(fill + 1), payload(fill + 2)}
	pieces := []types.PieceCommitment{commit(t, data[0]), commit(t, data[1]), commit(t, data[2])}
	var id uint64
	require.NoError(t, sim.block(func() error {
		id = sim.f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, duration, pieces)
		return nil
	}))
	require.NoError(t, sim.block(nil))
	return id, data, pieces
}

func TestProviderRunner_acceptsProvesAndReleases(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.threeNodes(t, 1<<20)
	sim := newChainSim(t, f)
	providers := startProviders(t, sim, "n1", "n2", "n3")
	id, data, pieces := openDeal(t, sim, 2)

	stepAll(t, providers)
	for i := uint32(0); i < 3; i++ {
		slot := f.slot(t, id, i)
		require.False(t, slot.Accepted, "nothing is accepted before the piece arrives")
		p := providers[slot.NodeId]
		require.Equal(t, http.StatusForbidden, upload(p, payload(9), commit(t, payload(9)).Root), "an unassigned root is refused")
		require.Equal(t, http.StatusNoContent, upload(p, dataForSlot(t, f, id, i, data, pieces), slot.PieceRoot))
	}

	stepAll(t, providers)
	for i := uint32(0); i < 3; i++ {
		require.True(t, f.slot(t, id, i).Accepted)
	}
	for nodeID := range providers {
		ch, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{Epoch: f.Emission.epoch, NodeId: nodeID})
		require.NoError(t, err)
		require.NotEmpty(t, ch.Challenges)
		for _, c := range ch.Challenges {
			require.Truef(t, c.Proved, "%s deal %d slot %d", nodeID, c.DealId, c.Slot)
		}
	}

	// The deal ends; the chain drops every slot, and the pieces go after the grace epoch.
	for epoch := uint64(2); epoch <= 5; epoch++ {
		f.Emission.epoch = epoch
		require.NoError(t, sim.block(nil))
		stepAll(t, providers)
	}
	for id, p := range providers {
		bound, err := p.store.Assignments()
		require.NoError(t, err)
		require.Emptyf(t, bound, "node %s still binds a released slot", id)
		used, err := p.store.UsedBytes()
		require.NoError(t, err)
		require.Zero(t, used)
	}
	f.requireInvariants(t)
}

func TestProviderRunner_declinesAMissingPieceBeforeTheWindowCloses(t *testing.T) {
	f := newFixture(t)
	f.init(t, func(gs *types.GenesisState) { gs.Params.AcceptWindowBlocks = 6 })
	f.threeNodes(t, 1<<20)
	f.addNode(t, "n4", "10.3.0.0/16", 4, 1<<20, false)
	sim := newChainSim(t, f)
	providers := startProviders(t, sim, "n1", "n2", "n3", "n4")
	id, _, _ := openDeal(t, sim, 30)

	stepAll(t, providers)
	first := f.slot(t, id, 0)
	require.Equal(t, types.SlotStatus_SLOT_STATUS_ASSIGNED, first.Status)
	for f.height < first.AssignHeight+6-provider.DeclineMarginBlocks {
		require.NoError(t, sim.block(nil))
	}
	require.NoError(t, providers[first.NodeId].runner.Step(context.Background()))

	after := f.slot(t, id, 0)
	require.NotEqual(t, first.NodeId, after.NodeId, "the declined slot is not left with the node")
	require.Equal(t, f.Nodes.byID[first.NodeId].operator.String(), after.ExcludedOperator)
}

func TestProviderRunner_retriesADroppedProofAfterARestart(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.threeNodes(t, 1<<20)
	sim := newChainSim(t, f)
	providers := startProviders(t, sim, "n1", "n2", "n3")
	id, data, pieces := openDeal(t, sim, 30)
	stepAll(t, providers)
	for i := uint32(0); i < 3; i++ {
		slot := f.slot(t, id, i)
		require.Equal(t, http.StatusNoContent, upload(providers[slot.NodeId], dataForSlot(t, f, id, i, data, pieces), slot.PieceRoot))
	}

	slot0 := f.slot(t, id, 0)
	p := providers[slot0.NodeId]
	require.NoError(t, p.runner.Step(context.Background()), "the accept lands")
	require.NoError(t, sim.block(nil))
	sim.drop = 1
	f.Emission.epoch = 2
	require.NoError(t, sim.block(nil))
	err := p.runner.Step(context.Background())
	require.ErrorIs(t, err, node.ErrNotIncluded)

	restarted := newSimRunner(t, sim, p.store, slot0.NodeId, p.state)
	require.NoError(t, restarted.Step(context.Background()))
	ch, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{Epoch: 2, NodeId: slot0.NodeId})
	require.NoError(t, err)
	require.NotEmpty(t, ch.Challenges)
	for _, c := range ch.Challenges {
		require.True(t, c.Proved)
	}
}

func TestProviderRunner_aFailingAcceptStillLetsProofsThrough(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.threeNodes(t, 1<<20)
	sim := newChainSim(t, f)
	providers := startProviders(t, sim, "n1", "n2", "n3")
	first, data, pieces := openDeal(t, sim, 30)
	stepAll(t, providers)
	for i := uint32(0); i < 3; i++ {
		slot := f.slot(t, first, i)
		require.Equal(t, http.StatusNoContent, upload(providers[slot.NodeId], dataForSlot(t, f, first, i, data, pieces), slot.PieceRoot))
	}
	stepAll(t, providers)

	second, data2, pieces2 := openDealFilled(t, sim, 30, 20)
	stepAll(t, providers)
	for i := uint32(0); i < 3; i++ {
		slot := f.slot(t, second, i)
		require.Equal(t, http.StatusNoContent, upload(providers[slot.NodeId], dataForSlot(t, f, second, i, data2, pieces2), slot.PieceRoot))
	}
	sim.failAccept = true
	f.Emission.epoch = 2
	require.NoError(t, sim.block(nil))
	for id, p := range providers {
		err := p.runner.Step(context.Background())
		require.ErrorContainsf(t, err, "accept rejected", "node %s", id)
		ch, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{Epoch: 2, NodeId: id})
		require.NoError(t, err)
		require.NotEmpty(t, ch.Challenges)
		for _, c := range ch.Challenges {
			require.Truef(t, c.Proved, "%s deal %d slot %d is proved even though its accept failed", id, c.DealId, c.Slot)
		}
	}
}

func TestProviderRunner_aFailedSlotReadStillProvesTheOtherChallenges(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.threeNodes(t, 1<<20)
	sim := newChainSim(t, f)
	providers := startProviders(t, sim, "n1", "n2", "n3")
	first, data, pieces := openDeal(t, sim, 30)
	second, data2, pieces2 := openDealFilled(t, sim, 30, 20)
	stepAll(t, providers)
	for _, d := range []struct {
		id     uint64
		data   [][]byte
		pieces []types.PieceCommitment
	}{{first, data, pieces}, {second, data2, pieces2}} {
		for i := uint32(0); i < 3; i++ {
			slot := f.slot(t, d.id, i)
			require.Equal(t, http.StatusNoContent, upload(providers[slot.NodeId], dataForSlot(t, f, d.id, i, d.data, d.pieces), slot.PieceRoot))
		}
	}
	stepAll(t, providers)

	f.Emission.epoch = 2
	require.NoError(t, sim.block(nil))
	node := f.slot(t, first, 0).NodeId
	sim.failSlot = map[[2]uint64]bool{{first, 0}: true}
	err := providers[node].runner.Step(context.Background())
	require.ErrorContains(t, err, "rpc timeout")
	ch, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{Epoch: 2, NodeId: node})
	require.NoError(t, err)
	proved := 0
	for _, c := range ch.Challenges {
		if c.DealId == first && c.Slot == 0 {
			require.False(t, c.Proved, "the unreadable slot was skipped")
			continue
		}
		require.Truef(t, c.Proved, "deal %d slot %d", c.DealId, c.Slot)
		proved++
	}
	require.Positive(t, proved, "the node's other challenges were proved")
}
