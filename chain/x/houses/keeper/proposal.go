package keeper

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// SubmitProposal opens a proposal when its tier is already open.
func (k Keeper) SubmitProposal(ctx sdk.Context, proposer sdk.AccAddress, content types.ProposalContent) (uint64, error) {
	if err := content.ValidateBasic(); err != nil {
		return 0, err
	}
	open, err := k.tierOpen(ctx, content.Kind())
	if err != nil {
		return 0, err
	}
	if !open {
		return 0, fmt.Errorf("%w", types.ErrTierClosed)
	}
	if content.SoftwareUpgrade != nil {
		if err := k.checkUpgradeHeight(ctx, *content.SoftwareUpgrade); err != nil {
			return 0, err
		}
	}
	ids, err := k.activeIDs(ctx)
	if err != nil {
		return 0, err
	}
	if len(ids) >= types.MaxActiveProposals {
		return 0, fmt.Errorf("active proposal cap of %d is full", types.MaxActiveProposals)
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to load houses params: %w", err)
	}
	next, err := k.NextProposalID.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to load next proposal id: %w", err)
	}
	if err := k.NextProposalID.Set(ctx, next+1); err != nil {
		return 0, fmt.Errorf("failed to advance next proposal id: %w", err)
	}
	proposal := types.Proposal{
		Id:                next,
		Proposer:          proposer.String(),
		SubmitUnixNano:    ctx.BlockTime().UnixNano(),
		VotingEndUnixNano: ctx.BlockTime().Add(params.VotingPeriod()).UnixNano(),
		Status:            types.ProposalStatus_VOTING,
		Content:           content,
		TokenYes:          math.ZeroInt(),
		TokenNo:           math.ZeroInt(),
		TokenAbstain:      math.ZeroInt(),
	}
	if err := k.Proposals.Set(ctx, next, proposal); err != nil {
		return 0, fmt.Errorf("failed to store proposal %d: %w", next, err)
	}
	if err := k.Active.Set(ctx, next); err != nil {
		return 0, fmt.Errorf("failed to mark proposal %d active: %w", next, err)
	}
	return next, nil
}

func (k Keeper) checkUpgradeHeight(ctx sdk.Context, upgrade types.SoftwareUpgrade) error {
	if upgrade.Height <= ctx.BlockHeight() {
		return fmt.Errorf("software upgrade height %d must be above current height %d", upgrade.Height, ctx.BlockHeight())
	}
	enacted, err := k.Enacted.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load enacted state: %w", err)
	}
	if enacted.ScheduledUpgrade != nil && enacted.ScheduledUpgrade.Height > ctx.BlockHeight() {
		return fmt.Errorf("software upgrade %q is already scheduled at height %d", enacted.ScheduledUpgrade.Name, enacted.ScheduledUpgrade.Height)
	}
	return nil
}

// VoteToken records a direct token-house vote. A different second vote is
// rejected and does not slash: the house bond belongs to the operator house.
func (k Keeper) VoteToken(ctx sdk.Context, voter sdk.AccAddress, proposalID uint64, option types.VoteOption) error {
	if !types.ValidVoteOption(option) {
		return fmt.Errorf("vote option is empty")
	}
	p, err := k.getProposal(ctx, proposalID)
	if err != nil {
		return err
	}
	if p.Status != types.ProposalStatus_VOTING || !ctx.BlockTime().Before(time.Unix(0, p.VotingEndUnixNano)) {
		return fmt.Errorf("token voting is closed for proposal %d", proposalID)
	}
	_, err = k.castVote(ctx, k.TokenVotes, proposalID, voter, option, false)
	return err
}

// VoteOperator records one operator vote. A different second vote burns the
// house bond, records equivocation, and leaves the first vote in place. The
// message itself succeeds so the slash is not rolled back with the transaction.
func (k Keeper) VoteOperator(ctx sdk.Context, voter sdk.AccAddress, proposalID uint64, option types.VoteOption) (bool, error) {
	if !types.ValidVoteOption(option) {
		return false, fmt.Errorf("vote option is empty")
	}
	p, err := k.getProposal(ctx, proposalID)
	if err != nil {
		return false, err
	}
	if !operatorVoteOpen(ctx, p) {
		return false, fmt.Errorf("operator voting is closed for proposal %d", proposalID)
	}
	eligible, err := k.eligibleSet(ctx)
	if err != nil {
		return false, err
	}
	if _, ok := eligible[voter.String()]; !ok {
		return false, fmt.Errorf("%w", types.ErrNotEligible)
	}
	equivocated, err := k.Equivocations.Has(ctx, collections.Join(proposalID, voter.String()))
	if err != nil {
		return false, fmt.Errorf("failed to check equivocation: %w", err)
	}
	if equivocated {
		return false, fmt.Errorf("%w", types.ErrNotEligible)
	}
	slashed, err := k.castVote(ctx, k.OperatorVotes, proposalID, voter, option, true)
	if err != nil || !slashed {
		return slashed, err
	}
	if err := k.slashBond(ctx, voter); err != nil {
		return false, err
	}
	if err := k.Equivocations.Set(ctx, collections.Join(proposalID, voter.String())); err != nil {
		return false, fmt.Errorf("failed to record equivocation: %w", err)
	}
	return true, nil
}

func operatorVoteOpen(ctx sdk.Context, p types.Proposal) bool {
	now := ctx.BlockTime()
	switch p.Status {
	case types.ProposalStatus_VOTING:
		return now.Before(time.Unix(0, p.VotingEndUnixNano))
	case types.ProposalStatus_VETO_WINDOW:
		return p.Content.Kind() == types.KindParameter && now.Before(time.Unix(0, p.VetoEndUnixNano))
	default:
		return false
	}
}

// castVote stores a first vote. same option is a no-op. A different option
// returns slashed=true without writing the new option when slash is set, or
// an error when slash is not set.
func (k Keeper) castVote(ctx context.Context, bucket collections.Map[collections.Pair[uint64, string], types.Vote], proposalID uint64, voter sdk.AccAddress, option types.VoteOption, slashOnConflict bool) (bool, error) {
	key := collections.Join(proposalID, voter.String())
	existing, err := bucket.Get(ctx, key)
	if err == nil {
		if existing.Option == option {
			return false, nil
		}
		if slashOnConflict {
			return true, nil
		}
		return false, fmt.Errorf("vote on proposal %d is already %s", proposalID, existing.Option)
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return false, fmt.Errorf("failed to load vote: %w", err)
	}
	vote := types.Vote{ProposalId: proposalID, Voter: voter.String(), Option: option}
	if err := bucket.Set(ctx, key, vote); err != nil {
		return false, fmt.Errorf("failed to store vote: %w", err)
	}
	return false, nil
}
