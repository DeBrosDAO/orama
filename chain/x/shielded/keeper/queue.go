package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// EventTypeQueuePaymentFailed is emitted when a window could not deliver a queued request. The
// request stays in the queue until it has been refused MaxQueueRefusals windows in a row.
const EventTypeQueuePaymentFailed = "shielded_queue_payment_failed"

// EventTypeQueueRefunded is emitted when a request whose target refused it MaxQueueRefusals windows
// in a row is paid back to its owner's bank balance and removed from the queue.
const EventTypeQueueRefunded = "shielded_queue_refunded"

// MaxQueueRefusals is how many windows in a row a request's target may refuse before the queue
// gives up on it and refunds it. A payment that goes out, or a window that reaches the request and
// skips it without a refusal (capacity, per-address cap, a part below the minimum), resets the count.
const MaxQueueRefusals = types.MaxQueueRefusals

func (k Keeper) queueHasEntries(ctx context.Context) (bool, error) {
	it, err := k.Queue.Iterate(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("read the unshield queue: %w", err)
	}
	defer it.Close()
	return it.Valid(), nil
}

// enqueue adds a request to the queue and to its owner's count. An owner that already has
// MaxQueuedPerOwner requests queued is refused, which fails the whole unshield.
func (k Keeper) enqueue(ctx sdk.Context, msg *types.MsgUnshield, owner sdk.AccAddress, amount math.Int) error {
	have, err := k.queuedBy(ctx, owner.String())
	if err != nil {
		return err
	}
	if have >= types.MaxQueuedPerOwner {
		return fmt.Errorf("%w: %s already has %d queued, the limit is %d", types.ErrQueueOwnerFull, owner, have, types.MaxQueuedPerOwner)
	}
	id, err := k.NextQueueID.Next(ctx)
	if err != nil {
		return fmt.Errorf("next queue id: %w", err)
	}
	key := nativePool()
	if err := k.Queue.Set(ctx, id, types.QueuedUnshield{
		Id: id, Vintage: key.K1(), Asset: key.K2(), Owner: owner.String(), Target: msg.Target,
		Validator: msg.Validator, NodeId: msg.NodeId, Role: msg.Role, Amount: amount, Height: ctx.BlockHeight(),
	}); err != nil {
		return fmt.Errorf("queue the unshield: %w", err)
	}
	return k.QueueOwners.Set(ctx, owner.String(), have+1)
}

// queuedBy is how many requests owner has in the queue.
func (k Keeper) queuedBy(ctx context.Context, owner string) (uint64, error) {
	n, err := k.QueueOwners.Get(ctx, owner)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return 0, fmt.Errorf("read %s's queued count: %w", owner, err)
	}
	return n, nil
}

// removeQueued takes a request out of the queue and out of its owner's count.
func (k Keeper) removeQueued(ctx context.Context, q types.QueuedUnshield) error {
	if err := k.Queue.Remove(ctx, q.Id); err != nil {
		return fmt.Errorf("remove queued unshield %d: %w", q.Id, err)
	}
	n, err := k.queuedBy(ctx, q.Owner)
	if err != nil {
		return err
	}
	if n <= 1 {
		return k.QueueOwners.Remove(ctx, q.Owner)
	}
	return k.QueueOwners.Set(ctx, q.Owner, n-1)
}

// MaxServedPerWindow bounds the payments one window makes, so the end blocker's work does not grow
// with the queue. The rest wait for the next window, oldest first. A request whose target refuses
// its payment is not a payment: it does not count against this.
const MaxServedPerWindow = 1000

// MaxScannedPerWindow is the hard bound on the queue rows one window examines, paid, refused or
// skipped. It keeps the end blocker's work bounded however many requests refuse; the rows past it
// wait for the next window.
const MaxScannedPerWindow = 10 * MaxServedPerWindow

// queued returns every queued request in id order, which is arrival order (genesis export and
// queries).
func (k Keeper) queued(ctx context.Context) ([]types.QueuedUnshield, error) {
	return k.queuedUpTo(ctx, 0)
}

// queuedUpTo returns the first limit queued requests in id order; limit 0 means all.
func (k Keeper) queuedUpTo(ctx context.Context, limit int) ([]types.QueuedUnshield, error) {
	var out []types.QueuedUnshield
	err := k.Queue.Walk(ctx, nil, func(_ uint64, q types.QueuedUnshield) (bool, error) {
		out = append(out, q)
		return limit > 0 && len(out) >= limit, nil
	})
	return out, err
}

// serveQueue pays the queue at most once per 24h. The capacity is what the cap leaves of the
// drained bucket, served oldest first and capped per address, so no address takes more than its
// cap and a bond is never paid in a part the delegation minimum would refuse. A request whose
// target refuses the payment stays queued and an event says so; the other requests are still paid.
func (k Keeper) serveQueue(ctx sdk.Context) error {
	waiting, err := k.queueHasEntries(ctx)
	if err != nil || !waiting {
		return err
	}
	key := nativePool()
	lim, stored, err := k.limiter(ctx, key)
	if err != nil {
		return err
	}
	now := ctx.BlockTime().Unix()
	if stored.ServedWindowStart != 0 && now < stored.ServedWindowStart+pool.WindowSeconds {
		return nil
	}
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	balance, err := k.poolBalance(ctx, key)
	if err != nil {
		return err
	}
	limit := pool.CapAmount(balance, p.UnshieldFloor)
	lim.Roll(ctx.BlockTime(), limit)
	reqs, err := k.queuedUpTo(ctx, MaxScannedPerWindow)
	if err != nil {
		return err
	}
	poolReqs := make([]pool.Request, len(reqs))
	for i, q := range reqs {
		poolReqs[i] = pool.Request{Address: q.Owner, Amount: q.Amount}
	}
	grants, refused, examined := pool.Serve(lim.Remaining(limit), p.QueuePerAddressCap, MaxServedPerWindow, poolReqs, func(i int, grant math.Int) bool {
		return k.partPaymentFits(ctx, reqs[i], grant)
	}, func(i int, grant math.Int) bool {
		return k.payQueued(ctx, reqs[i], grant)
	})
	for _, g := range grants {
		lim.Counted = lim.Counted.Add(g.Amount)
	}
	refusedNow := make(map[int]bool, len(refused))
	for _, i := range refused {
		refusedNow[i] = true
		if err := k.recordRefusal(ctx, reqs[i]); err != nil {
			return err
		}
	}
	// Only rows the window reached count: a row after the point where capacity or the payment budget
	// ran out was not looked at, so its refusals stay as they were.
	for i, q := range reqs[:examined] {
		// Not refused and not paid: the window skipped it (capacity, address cap, a part below the
		// minimum), so the refusals it had were not in a row.
		if q.Refusals > 0 && !refusedNow[i] && !grants[i].Amount.IsPositive() {
			q.Refusals = 0
			if err := k.Queue.Set(ctx, q.Id, q); err != nil {
				return fmt.Errorf("reset the refusals of queued unshield %d: %w", q.Id, err)
			}
		}
	}
	stored.ServedWindowStart = now
	return k.saveLimiter(ctx, key, lim, stored)
}

// partPaymentFits reports whether paying only grant of a queued bond would be accepted by its
// target. A part payment below the delegation minimum would be refused when paid, and issuing it
// would spend the window's capacity on nothing. The minimum of a node bond is checked by x/nodes
// only when the bond is made.
func (k Keeper) partPaymentFits(ctx sdk.Context, q types.QueuedUnshield, grant math.Int) bool {
	if q.Target != types.UnshieldTargetBond {
		return true
	}
	owner, err := sdk.AccAddressFromBech32(q.Owner)
	if err != nil {
		return true
	}
	return k.deps.Bonder.CheckMinimum(ctx, owner, q.Validator, grant) == nil
}

// payQueued pays grant of one queued request in a cache context and reports whether it went out.
// A payment that goes out clears the request's count of refusals; a refused one is left to
// recordRefusal, and the window's capacity is not used by it.
func (k Keeper) payQueued(ctx sdk.Context, q types.QueuedUnshield, grant math.Int) bool {
	cache, write := ctx.CacheContext()
	owner, err := sdk.AccAddressFromBech32(q.Owner)
	if err == nil {
		err = k.pay(cache, owner, q.Target, q.Validator, q.NodeId, q.Role, grant)
	}
	if err == nil {
		q.Amount = q.Amount.Sub(grant)
		q.Refusals = 0
		if q.Amount.IsPositive() {
			err = k.Queue.Set(cache, q.Id, q)
		} else {
			err = k.removeQueued(cache, q)
		}
	}
	if err != nil {
		ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeQueuePaymentFailed,
			sdk.NewAttribute("id", fmt.Sprint(q.Id)),
			sdk.NewAttribute("owner", q.Owner),
			sdk.NewAttribute("refusals", fmt.Sprint(q.Refusals+1)),
			sdk.NewAttribute("reason", err.Error()),
		))
		return false
	}
	write()
	return true
}

// recordRefusal counts one more window in which q's target refused it. At MaxQueueRefusals the
// request is refunded: what is left of it goes from the module account to its owner's bank
// balance, it leaves the queue and an event says so. A refund the bank refuses leaves the request
// queued with its count, so the next window tries again and nothing is lost.
func (k Keeper) recordRefusal(ctx sdk.Context, q types.QueuedUnshield) error {
	q.Refusals++
	if q.Refusals < MaxQueueRefusals {
		return k.Queue.Set(ctx, q.Id, q)
	}
	cache, write := ctx.CacheContext()
	owner, err := sdk.AccAddressFromBech32(q.Owner)
	if err == nil {
		err = k.deps.Bank.SendCoinsFromModuleToAccount(cache, types.ModuleName, owner, coins(q.Amount))
	}
	if err == nil {
		err = k.removeQueued(cache, q)
	}
	if err != nil {
		ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeQueuePaymentFailed,
			sdk.NewAttribute("id", fmt.Sprint(q.Id)),
			sdk.NewAttribute("owner", q.Owner),
			sdk.NewAttribute("reason", fmt.Sprintf("refund: %v", err)),
		))
		return k.Queue.Set(ctx, q.Id, q)
	}
	write()
	ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeQueueRefunded,
		sdk.NewAttribute("id", fmt.Sprint(q.Id)),
		sdk.NewAttribute("owner", q.Owner),
		sdk.NewAttribute("amount", q.Amount.String()),
	))
	return nil
}
