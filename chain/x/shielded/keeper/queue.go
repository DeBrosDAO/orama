package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// EventTypeQueuePaymentFailed is emitted when a window could not deliver a queued request. The
// request stays in the queue.
const EventTypeQueuePaymentFailed = "shielded_queue_payment_failed"

func (k Keeper) queueHasEntries(ctx context.Context) (bool, error) {
	it, err := k.Queue.Iterate(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("read the unshield queue: %w", err)
	}
	defer it.Close()
	return it.Valid(), nil
}

func (k Keeper) enqueue(ctx sdk.Context, msg *types.MsgUnshield, owner sdk.AccAddress, amount math.Int) error {
	id, err := k.NextQueueID.Next(ctx)
	if err != nil {
		return fmt.Errorf("next queue id: %w", err)
	}
	key := nativePool()
	return k.Queue.Set(ctx, id, types.QueuedUnshield{
		Id: id, Vintage: key.K1(), Asset: key.K2(), Owner: owner.String(), Target: msg.Target,
		Validator: msg.Validator, NodeId: msg.NodeId, Role: msg.Role, Amount: amount, Height: ctx.BlockHeight(),
	})
}

// MaxServedPerWindow bounds the payments one window makes, so the end blocker's work does not grow
// with the queue. The rest wait for the next window, oldest first.
const MaxServedPerWindow = 1000

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

// serveQueue pays the queue once per 24h window. The window's capacity is what the cap leaves,
// split pro rata by amount and capped per address, so no request at the head of the queue can
// take the whole window. A request whose target refuses the payment stays queued and an event says
// so; the other requests are still paid.
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
	lim.Roll(ctx.BlockTime())
	if stored.ServedWindowStart == lim.Start.Unix() {
		return k.saveLimiter(ctx, key, lim, stored)
	}
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	reqs, err := k.queuedUpTo(ctx, MaxServedPerWindow)
	if err != nil {
		return err
	}
	balance, err := k.poolBalance(ctx, key)
	if err != nil {
		return err
	}
	capacity := pool.CapAmount(balance, p.UnshieldFloor).Sub(lim.Counted)
	grants := grantsByOwner(capacity, p.QueuePerAddressCap, reqs)
	for _, q := range reqs {
		grant := math.MinInt(grants[q.Owner], q.Amount)
		if !grant.IsPositive() {
			continue
		}
		grants[q.Owner] = grants[q.Owner].Sub(grant)
		if k.payQueued(ctx, q, grant) {
			lim.Counted = lim.Counted.Add(grant)
		}
	}
	stored.ServedWindowStart = lim.Start.Unix()
	return k.saveLimiter(ctx, key, lim, stored)
}

// grantsByOwner runs pool.Serve over one request per owner, so the per-address cap binds an
// address however many requests it queued.
func grantsByOwner(capacity, perAddress math.Int, reqs []types.QueuedUnshield) map[string]math.Int {
	var owners []string
	wanted := map[string]math.Int{}
	for _, q := range reqs {
		if _, ok := wanted[q.Owner]; !ok {
			owners = append(owners, q.Owner)
			wanted[q.Owner] = math.ZeroInt()
		}
		wanted[q.Owner] = wanted[q.Owner].Add(q.Amount)
	}
	poolReqs := make([]pool.Request, len(owners))
	for i, o := range owners {
		poolReqs[i] = pool.Request{Address: o, Amount: wanted[o]}
	}
	out := map[string]math.Int{}
	for _, g := range pool.Serve(capacity, perAddress, poolReqs) {
		out[g.Address] = g.Amount
	}
	return out
}

// payQueued pays grant of one queued request in a cache context and reports whether it went out.
func (k Keeper) payQueued(ctx sdk.Context, q types.QueuedUnshield, grant math.Int) bool {
	cache, write := ctx.CacheContext()
	owner, err := sdk.AccAddressFromBech32(q.Owner)
	if err == nil {
		err = k.pay(cache, owner, q.Target, q.Validator, q.NodeId, q.Role, grant)
	}
	if err == nil {
		q.Amount = q.Amount.Sub(grant)
		if q.Amount.IsPositive() {
			err = k.Queue.Set(cache, q.Id, q)
		} else {
			err = k.Queue.Remove(cache, q.Id)
		}
	}
	if err != nil {
		ctx.EventManager().EmitEvent(sdk.NewEvent(EventTypeQueuePaymentFailed,
			sdk.NewAttribute("id", fmt.Sprint(q.Id)),
			sdk.NewAttribute("owner", q.Owner),
			sdk.NewAttribute("reason", err.Error()),
		))
		return false
	}
	write()
	return true
}
