package keeper_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/provider"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func TestProviderAcceptAndProofRoundTrip(t *testing.T) {
	f := newFixture(t)
	f.init(t, nil)
	f.threeNodes(t, 1<<20)
	client := acc(9)
	f.fund(client, 100_000)
	data := [][]byte{payload(1), payload(2), payload(3)}
	pieces := []types.PieceCommitment{
		commit(t, data[0]), commit(t, data[1]), commit(t, data[2]),
	}
	id := f.createDeal(t, types.DealClass_DEAL_CLASS_PRIVATE, client, "", 3, 1000, 30, pieces)
	f.end(t)
	f.begin(t)

	stores := map[string]*provider.Store{}
	for i := uint32(0); i < 3; i++ {
		slot := f.slot(t, id, i)
		require.NotEmpty(t, slot.NodeId)
		s := stores[slot.NodeId]
		if s == nil {
			var err error
			s, err = provider.Open(t.TempDir(), nil, nil)
			require.NoError(t, err)
			stores[slot.NodeId] = s
		}
		body := dataForSlot(t, f, id, i, data, pieces)
		cid := fmt.Sprintf("slot-%d", i)
		decision, err := s.Ingest(cid, body, slot.PieceRoot)
		require.NoError(t, err)
		require.True(t, decision.Accept)
		info := f.Nodes.byID[slot.NodeId]
		accept, decline, err := s.Decide(info.hot.String(), slot.NodeId, id, i, slot.PieceRoot)
		require.NoError(t, err)
		require.Nil(t, decline)
		_, err = f.Msg.AcceptDeal(f.Ctx, accept)
		require.NoError(t, err)
	}

	for nodeID, s := range stores {
		ch, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{
			Epoch: f.Emission.epoch, NodeId: nodeID,
		})
		require.NoError(t, err)
		require.NotEmpty(t, ch.Challenges)
		proofs, missing, err := s.AnswerChallenges(f.Emission.epoch, nodeID, ch.Challenges)
		require.NoError(t, err)
		require.Empty(t, missing)
		require.NotEmpty(t, proofs)
		info := f.Nodes.byID[nodeID]
		_, err = f.Msg.SubmitProofs(f.Ctx, &types.MsgSubmitProofs{
			Signer: info.hot.String(), NodeId: nodeID, Proofs: proofs,
		})
		require.NoError(t, err)
		again, err := f.Query.Challenges(f.Ctx, &types.QueryChallengesRequest{
			Epoch: f.Emission.epoch, NodeId: nodeID,
		})
		require.NoError(t, err)
		for _, c := range again.Challenges {
			require.Truef(t, c.Proved, "deal %d slot %d", c.DealId, c.Slot)
		}
	}
	f.requireInvariants(t)
}
