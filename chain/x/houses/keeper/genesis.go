package keeper

import (
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// InitGenesis loads genesis state. Bonds in genesis must already sit in the
// houses module account; CheckInvariants refuses a mismatch.
func (k Keeper) InitGenesis(ctx sdk.Context, genState types.GenesisState) error {
	if err := genState.Validate(); err != nil {
		return fmt.Errorf("invalid houses genesis state: %w", err)
	}
	if err := k.Params.Set(ctx, genState.Params); err != nil {
		return fmt.Errorf("failed to set houses params: %w", err)
	}
	if err := k.NextProposalID.Set(ctx, genState.NextProposalId); err != nil {
		return fmt.Errorf("failed to set next proposal id: %w", err)
	}
	enacted := genState.Enacted
	enacted.MMax = types.DecOrZero(enacted.MMax)
	if err := k.Enacted.Set(ctx, enacted); err != nil {
		return fmt.Errorf("failed to set enacted state: %w", err)
	}
	for _, p := range genState.Proposals {
		p.TokenYes = types.IntOrZero(p.TokenYes)
		p.TokenNo = types.IntOrZero(p.TokenNo)
		p.TokenAbstain = types.IntOrZero(p.TokenAbstain)
		if err := k.Proposals.Set(ctx, p.Id, p); err != nil {
			return fmt.Errorf("failed to set proposal %d: %w", p.Id, err)
		}
		if isActive(p.Status) {
			if err := k.Active.Set(ctx, p.Id); err != nil {
				return fmt.Errorf("failed to mark proposal %d active: %w", p.Id, err)
			}
		}
	}
	if err := putVotes(ctx, k.TokenVotes, genState.TokenVotes); err != nil {
		return err
	}
	if err := putVotes(ctx, k.OperatorVotes, genState.OperatorVotes); err != nil {
		return err
	}
	for _, bond := range genState.Bonds {
		if err := k.Bonds.Set(ctx, bond.Address, bond); err != nil {
			return fmt.Errorf("failed to set house bond %s: %w", bond.Address, err)
		}
	}
	for _, eq := range genState.Equivocations {
		if err := k.Equivocations.Set(ctx, collections.Join(eq.ProposalId, eq.Operator)); err != nil {
			return fmt.Errorf("failed to set equivocation: %w", err)
		}
	}
	got, err := k.CheckInvariants(ctx)
	if err != nil {
		return err
	}
	if !got.BondsMatchModule {
		return fmt.Errorf("houses genesis broke the bond invariant:\n%s", got.Detail)
	}
	return nil
}

func putVotes(ctx sdk.Context, bucket collections.Map[collections.Pair[uint64, string], types.Vote], votes []types.Vote) error {
	for _, vote := range votes {
		if err := bucket.Set(ctx, collections.Join(vote.ProposalId, vote.Voter), vote); err != nil {
			return fmt.Errorf("failed to set vote: %w", err)
		}
	}
	return nil
}

func isActive(status types.ProposalStatus) bool {
	return status == types.ProposalStatus_VOTING || status == types.ProposalStatus_VETO_WINDOW || status == types.ProposalStatus_TIMELOCK
}

// ExportGenesis reads the current state.
func (k Keeper) ExportGenesis(ctx sdk.Context) (*types.GenesisState, error) {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get houses params: %w", err)
	}
	next, err := k.NextProposalID.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get next proposal id: %w", err)
	}
	enacted, err := k.Enacted.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get enacted state: %w", err)
	}
	var proposals []types.Proposal
	if err := k.Proposals.Walk(ctx, nil, func(_ uint64, proposal types.Proposal) (bool, error) {
		proposals = append(proposals, proposal)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk proposals: %w", err)
	}
	tokenVotes, err := exportVotes(ctx, k.TokenVotes)
	if err != nil {
		return nil, err
	}
	operatorVotes, err := exportVotes(ctx, k.OperatorVotes)
	if err != nil {
		return nil, err
	}
	var bonds []types.HouseBond
	if err := k.Bonds.Walk(ctx, nil, func(_ string, bond types.HouseBond) (bool, error) {
		bonds = append(bonds, bond)
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk house bonds: %w", err)
	}
	var equivocations []types.Equivocation
	if err := k.Equivocations.Walk(ctx, nil, func(key collections.Pair[uint64, string]) (bool, error) {
		equivocations = append(equivocations, types.Equivocation{ProposalId: key.K1(), Operator: key.K2()})
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk equivocations: %w", err)
	}
	if proposals == nil {
		proposals = []types.Proposal{}
	}
	if tokenVotes == nil {
		tokenVotes = []types.Vote{}
	}
	if operatorVotes == nil {
		operatorVotes = []types.Vote{}
	}
	if bonds == nil {
		bonds = []types.HouseBond{}
	}
	if equivocations == nil {
		equivocations = []types.Equivocation{}
	}
	return &types.GenesisState{
		Params:         p,
		Proposals:      proposals,
		TokenVotes:     tokenVotes,
		OperatorVotes:  operatorVotes,
		Bonds:          bonds,
		Equivocations:  equivocations,
		Enacted:        enacted,
		NextProposalId: next,
	}, nil
}

func exportVotes(ctx sdk.Context, bucket collections.Map[collections.Pair[uint64, string], types.Vote]) ([]types.Vote, error) {
	var votes []types.Vote
	err := bucket.Walk(ctx, nil, func(_ collections.Pair[uint64, string], vote types.Vote) (bool, error) {
		votes = append(votes, vote)
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk votes: %w", err)
	}
	return votes, nil
}
