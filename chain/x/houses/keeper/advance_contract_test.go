package keeper_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// advanceReads names every collaborator read that advanceProposal makes while it closes a vote and
// how a test makes that read fail. A read added to the advance path without a mark from
// rejectAdvance fails the block on the first transient error; a read that is missing here is not
// pinned, so each new collaborator call in advanceProposal gets a row.
var advanceReads = map[string]func(f *testFixture, err error){
	"staking total bonded tokens": func(f *testFixture, err error) { f.Staking.failTotalWith = err },
	"staking delegations":         func(f *testFixture, err error) { f.Staking.failDelegationsWith = err },
	"operator set":                func(f *testFixture, err error) { f.Operators.failWith = err },
}

func structuralAtVotingEnd(t *testing.T) (*testFixture, uint64) {
	t.Helper()
	f := openStructural(t)
	f.Emission.ceiling[1] = math.NewInt(100)
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), spendContent(math.NewInt(10)))
	require.NoError(t, err)
	f.passStructuralVotes(t, id)
	f.Ctx = f.Ctx.WithBlockTime(f.Ctx.BlockTime().Add(24 * time.Hour))
	return f, id
}

// The contract of Advance: a collaborator error is one proposal's failure only when it is marked
// ErrAdvanceRejected (the keeper marks every collaborator read it makes), and then the proposal is
// retried in a later block; an unmarked error (here a collections encoding fault reported by the
// collaborator, which the mark refuses to cover) fails the block.
func TestAdvance_everyCollaboratorReadIsRetriedWhenMarkedAndFatalWhenNot(t *testing.T) {
	for name, fail := range advanceReads {
		t.Run(name+" refused is retried", func(t *testing.T) {
			f, id := structuralAtVotingEnd(t)
			fail(f, errors.New("collaborator store unreadable"))

			require.NoError(t, f.Keeper.Advance(f.Ctx), "one proposal's failed read must not fail the block")

			p := f.proposal(t, id)
			require.Equal(t, types.ProposalStatus_VOTING, p.Status)
			require.Equal(t, uint32(1), p.AdvanceFailures)
			reported := false
			for _, e := range f.Ctx.EventManager().Events() {
				reported = reported || e.Type == "houses_proposal_failed"
			}
			require.True(t, reported)
		})
		t.Run(name+" fault is fatal", func(t *testing.T) {
			f, id := structuralAtVotingEnd(t)
			fail(f, fmt.Errorf("decode: %w", collections.ErrEncoding))

			require.ErrorIs(t, f.Keeper.Advance(f.Ctx), collections.ErrEncoding)
			require.Equal(t, types.ProposalStatus_VOTING, f.proposal(t, id).Status)
		})
	}
}

// An enacted action a collaborator refuses fails that proposal, once; it never fails the block.
func TestAdvance_aRefusedEnactmentFailsTheProposalNotTheBlock(t *testing.T) {
	f := openStructural(t)
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{RelayReporters: &types.RelayReporterChange{Add: []string{acc(5).String()}}})
	require.NoError(t, err)
	f.passStructuralVotes(t, id)
	f.advance(t, 24*time.Hour)
	f.Reporters.fail = true

	f.advance(t, 60*24*time.Hour)

	p := f.proposal(t, id)
	require.Equal(t, types.ProposalStatus_FAILED, p.Status)
	require.Contains(t, p.FailReason, "reporter set would be empty")
}
