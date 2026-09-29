package keeper

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// Advance closes voting, closes veto windows and executes timelocks that
// have elapsed. EndBlock calls it. There is no path that skips a timelock.
//
// One proposal that cannot be advanced (a staking read that fails, a tally over data that does not
// add up) must not fail EndBlock, and with it FinalizeBlock on every validator. Its writes are
// rolled back, the failure is reported and counted on the proposal, and it is tried again in the
// next block. A proposal that fails MaxAdvanceAttempts blocks in a row is closed as FAILED with the
// reason, so a proposal that can never be tallied does not stay active for ever. A failure that is
// not about the proposal (a collection that cannot be read or decoded) is returned and fails the
// block.
func (k Keeper) Advance(ctx sdk.Context) error {
	ids, err := k.activeIDs(ctx)
	if err != nil {
		return err
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		p, err := k.getProposal(ctx, id)
		if err != nil {
			if !errors.Is(err, collections.ErrNotFound) {
				return err
			}
			k.reportProposalFailure(ctx, id, err)
			continue
		}
		failures := p.AdvanceFailures
		p.AdvanceFailures = 0
		cacheCtx, write := ctx.CacheContext()
		if err := k.advanceProposal(cacheCtx, p); err != nil {
			if !errors.Is(err, types.ErrAdvanceRejected) {
				return fmt.Errorf("failed to advance proposal %d: %w", id, err)
			}
			k.reportProposalFailure(ctx, id, err)
			p.AdvanceFailures = failures + 1
			if p.AdvanceFailures >= types.MaxAdvanceAttempts {
				err = k.finish(ctx, p, types.ProposalStatus_FAILED, err.Error())
			} else {
				err = k.Proposals.Set(ctx, p.Id, p)
			}
			if err != nil {
				return err
			}
			continue
		}
		write()
		if failures == 0 {
			continue
		}
		// The proposal advanced, so the failures were not consecutive: clear the count that a step
		// that leaves the proposal unchanged (a window still open) did not.
		advanced, err := k.getProposal(ctx, id)
		if err != nil {
			return err
		}
		advanced.AdvanceFailures = 0
		if err := k.Proposals.Set(ctx, id, advanced); err != nil {
			return fmt.Errorf("failed to clear the advance failures of proposal %d: %w", id, err)
		}
	}
	return nil
}

func (k Keeper) reportProposalFailure(ctx sdk.Context, id uint64, cause error) {
	k.Logger(ctx).Error("proposal could not be advanced", "proposal", id, "err", cause)
	ctx.EventManager().EmitEvent(sdk.NewEvent("houses_proposal_failed",
		sdk.NewAttribute("proposal_id", fmt.Sprintf("%d", id)),
		sdk.NewAttribute("error", cause.Error()),
	))
}

// advanceProposal moves one active proposal to its next state when its window has elapsed.
func (k Keeper) advanceProposal(ctx sdk.Context, p types.Proposal) error {
	switch p.Status {
	case types.ProposalStatus_VOTING:
		if !ctx.BlockTime().Before(time.Unix(0, p.VotingEndUnixNano)) {
			return k.closeVoting(ctx, p)
		}
	case types.ProposalStatus_VETO_WINDOW:
		if !ctx.BlockTime().Before(time.Unix(0, p.VetoEndUnixNano)) {
			return k.closeVeto(ctx, p)
		}
	case types.ProposalStatus_TIMELOCK:
		if !ctx.BlockTime().Before(time.Unix(0, p.TimelockEndUnixNano)) {
			return k.execute(ctx, p)
		}
	default:
		if err := k.Active.Remove(ctx, p.Id); err != nil {
			return fmt.Errorf("failed to drop inactive proposal %d: %w", p.Id, err)
		}
	}
	return nil
}

// ExecuteProposal runs a proposal whose timelock has elapsed. Any account may
// call it; the signer is not an authority.
func (k Keeper) ExecuteProposal(ctx sdk.Context, id uint64) error {
	p, err := k.getProposal(ctx, id)
	if err != nil {
		return err
	}
	if p.Status != types.ProposalStatus_TIMELOCK {
		return fmt.Errorf("proposal %d is %s, not in timelock", id, p.Status)
	}
	if ctx.BlockTime().Before(time.Unix(0, p.TimelockEndUnixNano)) {
		return fmt.Errorf("proposal %d timelock has not elapsed", id)
	}
	return k.execute(ctx, p)
}

func (k Keeper) closeVoting(ctx sdk.Context, p types.Proposal) error {
	yes, no, abstain, err := k.tallyToken(ctx, p.Id)
	if err != nil {
		return err
	}
	p.TokenYes, p.TokenNo, p.TokenAbstain = yes, no, abstain
	params, err := k.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load houses params: %w", err)
	}
	bonded, err := k.staking.TotalBondedTokens(ctx)
	if err != nil {
		return rejectAdvance(fmt.Errorf("failed to load bonded stake: %w", err))
	}
	passed := tokenHousePasses(yes, no, abstain, bonded, params.TokenQuorum, params.TokenPassThreshold)
	if p.Content.Kind() == types.KindParameter {
		if !passed {
			return k.finish(ctx, p, types.ProposalStatus_REJECTED, "")
		}
		p.Status = types.ProposalStatus_VETO_WINDOW
		p.VetoEndUnixNano = ctx.BlockTime().Add(params.VetoWindow()).UnixNano()
		if err := k.Proposals.Set(ctx, p.Id, p); err != nil {
			return fmt.Errorf("failed to open veto window for proposal %d: %w", p.Id, err)
		}
		return nil
	}
	opYes, opNo, opAbstain, eligible, err := k.tallyOperators(ctx, p.Id)
	if err != nil {
		return err
	}
	p.OperatorYes, p.OperatorNo, p.OperatorAbstain, p.EligibleOperators = opYes, opNo, opAbstain, eligible
	if !passed || !operatorMajority(opYes, eligible) {
		return k.finish(ctx, p, types.ProposalStatus_REJECTED, "")
	}
	p.Status = types.ProposalStatus_TIMELOCK
	p.TimelockEndUnixNano = ctx.BlockTime().Add(params.TimelockFor(p.Content)).UnixNano()
	if err := k.Proposals.Set(ctx, p.Id, p); err != nil {
		return fmt.Errorf("failed to open timelock for proposal %d: %w", p.Id, err)
	}
	return nil
}

func (k Keeper) closeVeto(ctx sdk.Context, p types.Proposal) error {
	opYes, opNo, opAbstain, eligible, err := k.tallyOperators(ctx, p.Id)
	if err != nil {
		return err
	}
	p.OperatorYes, p.OperatorNo, p.OperatorAbstain, p.EligibleOperators = opYes, opNo, opAbstain, eligible
	if vetoed(opNo, eligible) {
		return k.finish(ctx, p, types.ProposalStatus_REJECTED, "operator house veto")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load houses params: %w", err)
	}
	p.Status = types.ProposalStatus_TIMELOCK
	p.TimelockEndUnixNano = ctx.BlockTime().Add(params.TimelockFor(p.Content)).UnixNano()
	if err := k.Proposals.Set(ctx, p.Id, p); err != nil {
		return fmt.Errorf("failed to open timelock for proposal %d: %w", p.Id, err)
	}
	return nil
}

// execute applies p on a cache context: a refused action leaves no partial
// write behind, and the proposal is recorded as FAILED. FAILED is final and is not retried: C5
// records a spend that x/emission refuses as failed, an enactment runs once, when its timelock
// ends, against the state the vote was cast on, and a proposer who wants the action again submits a
// new proposal. A collection that cannot be decoded is not a refusal of the action: it fails the block.
func (k Keeper) execute(ctx sdk.Context, p types.Proposal) error {
	cacheCtx, write := ctx.CacheContext()
	if err := k.apply(cacheCtx, p); err != nil {
		if errors.Is(err, collections.ErrEncoding) {
			return fmt.Errorf("failed to execute proposal %d: %w", p.Id, err)
		}
		return k.finish(ctx, p, types.ProposalStatus_FAILED, err.Error())
	}
	write()
	return k.finish(ctx, p, types.ProposalStatus_EXECUTED, "")
}

func (k Keeper) finish(ctx sdk.Context, p types.Proposal, status types.ProposalStatus, reason string) error {
	p.Status = status
	p.FailReason = reason
	if err := k.Proposals.Set(ctx, p.Id, p); err != nil {
		return fmt.Errorf("failed to store proposal %d: %w", p.Id, err)
	}
	if err := k.Active.Remove(ctx, p.Id); err != nil {
		return fmt.Errorf("failed to clear active proposal %d: %w", p.Id, err)
	}
	return nil
}

func (k Keeper) apply(ctx sdk.Context, p types.Proposal) error {
	switch {
	case p.Content.ParameterChange != nil:
		return k.applyParams(ctx, *p.Content.ParameterChange)
	case p.Content.SoftwareUpgrade != nil:
		return k.applyUpgrade(ctx, *p.Content.SoftwareUpgrade)
	case p.Content.EmissionSplit != nil:
		return k.applySplit(ctx, *p.Content.EmissionSplit)
	case p.Content.DevelopmentSpend != nil:
		return k.applySpend(ctx, *p.Content.DevelopmentSpend)
	case p.Content.PowerBounds != nil:
		return k.applyPower(ctx, *p.Content.PowerBounds)
	case p.Content.RelayReporters != nil:
		return k.applyReporters(ctx, *p.Content.RelayReporters)
	case p.Content.AllowList != nil:
		return k.applyAllowList(ctx, *p.Content.AllowList)
	default:
		return fmt.Errorf("proposal %d has no action", p.Id)
	}
}

func (k Keeper) applyParams(ctx sdk.Context, change types.ParameterChange) error {
	p, err := k.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to load houses params: %w", err)
	}
	// BootstrapExitStake is deliberately left as it was at genesis.
	p.TokenQuorum = change.TokenQuorum
	p.TokenPassThreshold = change.TokenPassThreshold
	p.VotingPeriodSeconds = change.VotingPeriodSeconds
	p.HouseBond = change.HouseBond
	p.MaxEligiblePerPrefix16 = change.MaxEligiblePerPrefix16
	p.MaxEligiblePerAsn = change.MaxEligiblePerAsn
	if err := p.Validate(); err != nil {
		return err
	}
	if err := k.Params.Set(ctx, p); err != nil {
		return fmt.Errorf("failed to store houses params: %w", err)
	}
	return nil
}

func (k Keeper) applyUpgrade(ctx sdk.Context, upgrade types.SoftwareUpgrade) error {
	if err := k.checkUpgradeHeight(ctx, upgrade); err != nil {
		return err
	}
	if k.upgrades == nil {
		return fmt.Errorf("no upgrade scheduler is wired, software upgrade %q cannot be enacted", upgrade.Name)
	}
	if err := k.upgrades.ScheduleUpgrade(ctx, upgrade.Name, upgrade.Height); err != nil {
		return fmt.Errorf("failed to schedule software upgrade %q at height %d: %w", upgrade.Name, upgrade.Height, err)
	}
	enacted, err := k.Enacted.Get(ctx)
	if err != nil {
		return err
	}
	enacted.ScheduledUpgrade = &upgrade
	return k.Enacted.Set(ctx, enacted)
}

func (k Keeper) applySplit(ctx sdk.Context, split types.EmissionSplitChange) error {
	if err := splitOK(split); err != nil {
		return err
	}
	enacted, err := k.Enacted.Get(ctx)
	if err != nil {
		return err
	}
	enacted.EmissionSplit = &split
	if err := k.Enacted.Set(ctx, enacted); err != nil {
		return fmt.Errorf("failed to store emission split: %w", err)
	}
	return nil
}

func splitOK(split types.EmissionSplitChange) error {
	content := types.ProposalContent{EmissionSplit: &split}
	return content.ValidateBasic()
}

func (k Keeper) applyPower(ctx sdk.Context, change types.PowerBoundsChange) error {
	enacted, err := k.Enacted.Get(ctx)
	if err != nil {
		return err
	}
	if !change.MMax.IsNil() && !change.MMax.IsZero() {
		enacted.MMax = change.MMax
	}
	if change.ActivateM {
		enacted.MActivated = true
	}
	if err := k.Enacted.Set(ctx, enacted); err != nil {
		return fmt.Errorf("failed to store power bounds: %w", err)
	}
	return nil
}

func (k Keeper) applyReporters(ctx sdk.Context, change types.RelayReporterChange) error {
	if k.reporters == nil {
		return fmt.Errorf("no relay reporter keeper is wired, reporter change cannot be enacted")
	}
	if err := k.reporters.ChangeReporters(ctx, change.Add, change.Remove); err != nil {
		return fmt.Errorf("failed to change relay reporters: %w", err)
	}
	enacted, err := k.Enacted.Get(ctx)
	if err != nil {
		return err
	}
	enacted.RelayReporters = applyStringSet(enacted.RelayReporters, change.Add, change.Remove)
	if err := k.Enacted.Set(ctx, enacted); err != nil {
		return fmt.Errorf("failed to store relay reporters: %w", err)
	}
	return nil
}

func (k Keeper) applyAllowList(ctx sdk.Context, change types.AllowListChange) error {
	enacted, err := k.Enacted.Get(ctx)
	if err != nil {
		return err
	}
	enacted.CodeUploadAllow = applyStringSet(enacted.CodeUploadAllow, change.CodeUploadAdd, change.CodeUploadRemove)
	enacted.AdapterAllow = applyStringSet(enacted.AdapterAllow, change.AdapterAdd, change.AdapterRemove)
	if err := k.Enacted.Set(ctx, enacted); err != nil {
		return fmt.Errorf("failed to store allow-lists: %w", err)
	}
	return nil
}

func (k Keeper) applySpend(ctx sdk.Context, spend types.DevelopmentSpend) error {
	// Mint and the earnings credit commit together. A refusal discards both.
	cacheCtx, write := ctx.CacheContext()
	moduleName, err := k.emission.MintDevelopmentSpend(cacheCtx, spend.Epoch, spend.Amount)
	if err != nil {
		return err
	}
	if moduleName == "" {
		return fmt.Errorf("development mint did not name a module account")
	}
	recipient, err := sdk.AccAddressFromBech32(spend.Recipient)
	if err != nil {
		return fmt.Errorf("development spend recipient: %w", err)
	}
	coin := sdk.NewCoin(params.BaseDenom, spend.Amount)
	if err := k.earnings.CreditEarnings(cacheCtx, moduleName, recipient, coin); err != nil {
		return fmt.Errorf("failed to credit development spend: %w", err)
	}
	write()
	return nil
}

func applyStringSet(current, add, remove []string) []string {
	set := map[string]struct{}{}
	for _, item := range current {
		set[item] = struct{}{}
	}
	for _, item := range add {
		set[item] = struct{}{}
	}
	for _, item := range remove {
		delete(set, item)
	}
	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}
