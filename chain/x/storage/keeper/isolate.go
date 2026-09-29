package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// FailureKindSync counts consecutive blocks x/nodes reconciliation of one node failed.
	FailureKindSync = "sync"
	// FailureKindSettlement counts consecutive settlement rows of one node that failed.
	FailureKindSettlement = "settlement"
	// FailureKindDeposit counts consecutive credits whose share of a probation record deposit
	// could not be locked (the payout itself was applied).
	FailureKindDeposit = "deposit"
	// FailureKindChallenge counts consecutive epochs a node's challenges could not be opened.
	FailureKindChallenge = "challenge"
	// FailureKindProbation counts consecutive blocks a node's probation expiry failed.
	FailureKindProbation = "probation"
	// FailureKindScoring counts consecutive epochs a node's closed challenge could not be scored.
	FailureKindScoring = "scoring"
	// FailureKindOperators counts consecutive epoch closes a node's operator could not be read.
	FailureKindOperators = "operators"
	// FailureKindDeal counts consecutive blocks one deal's BeginBlock work failed.
	FailureKindDeal = "deal"

	// failureEscalationEvery is the consecutive-failure interval at which a stuck subject is
	// logged at error level and reported with a storage_item_stuck event, so an operator
	// watching the chain sees a node that never recovers, not one bad block.
	failureEscalationEvery = 100
)

// dealSubject names a deal in the failure counters, which are keyed by node id otherwise.
func dealSubject(dealID uint64) string { return fmt.Sprintf("deal/%d", dealID) }

// isolate runs one item's work on a cache branch of ctx. When fn fails, the branch is dropped,
// the failure is counted against subject, a storage_item_failed event carries the reason, and
// isolate reports failed=true so the caller can decide whether the item is retried or dropped.
// When fn succeeds the branch is written and the subject's failure count is cleared.
//
// A per-node, per-deal or per-user record that cannot be processed is data about that one item,
// not a reason to stop the chain: returning its error out of BeginBlock or EndBlock would fail
// FinalizeBlock and halt every validator on one node's funds. The returned error is only for the
// failure counters themselves not being writable, which is a store fault and stays fatal.
func (k Keeper) isolate(ctx sdk.Context, kind, subject string, fn func(sdk.Context) error) (failed bool, err error) {
	branch, write := ctx.CacheContext()
	if ferr := fn(branch); ferr != nil {
		return true, k.recordFailure(ctx, kind, subject, ferr)
	}
	write()
	return false, k.clearFailure(ctx, kind, subject)
}

func (k Keeper) recordFailure(ctx sdk.Context, kind, subject string, cause error) error {
	key := collections.Join(subject, kind)
	n, err := k.Failures.Get(ctx, key)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("failed to read failure count of %s/%s: %w", subject, kind, err)
	}
	n++
	if err := k.Failures.Set(ctx, key, n); err != nil {
		return fmt.Errorf("failed to record failure of %s/%s: %w", subject, kind, err)
	}
	attrs := []sdk.Attribute{
		sdk.NewAttribute("kind", kind),
		sdk.NewAttribute("subject", subject),
		sdk.NewAttribute("consecutive", fmt.Sprintf("%d", n)),
		sdk.NewAttribute("error", cause.Error()),
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("storage_item_failed", attrs...))
	if kind == FailureKindSync {
		// Kept for the node-sync monitors that already watch this event name.
		ctx.EventManager().EmitEvent(sdk.NewEvent("storage_node_sync_failed",
			sdk.NewAttribute("node_id", subject), sdk.NewAttribute("error", cause.Error())))
	}
	k.Logger(ctx).Error("storage work rolled back for one item", "kind", kind, "subject", subject, "consecutive", n, "err", cause)
	if n%failureEscalationEvery == 0 {
		ctx.EventManager().EmitEvent(sdk.NewEvent("storage_item_stuck", attrs...))
		k.Logger(ctx).Error("storage item has failed repeatedly and needs an operator", "kind", kind, "subject", subject, "consecutive", n)
	}
	return nil
}

func (k Keeper) clearFailure(ctx sdk.Context, kind, subject string) error {
	key := collections.Join(subject, kind)
	has, err := k.Failures.Has(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to read failure count of %s/%s: %w", subject, kind, err)
	}
	if !has {
		return nil
	}
	if err := k.Failures.Remove(ctx, key); err != nil {
		return fmt.Errorf("failed to clear failure count of %s/%s: %w", subject, kind, err)
	}
	return nil
}

// FailureCount returns the current consecutive-failure count of a subject and kind.
func (k Keeper) FailureCount(ctx sdk.Context, subject, kind string) (uint64, error) {
	n, err := k.Failures.Get(ctx, collections.Join(subject, kind))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return n, nil
}
