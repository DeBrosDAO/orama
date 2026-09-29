package keeper

import (
	"fmt"
	"slices"
	"strconv"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// CreateArchiveDeal opens one protocol ARCHIVE deal for the bundle of an attested range. The
// signer must be the hot key of an active ARCHIVER node whose operator attested the range. The
// chain sets the price (x/storage's protocol price) and the duration (types.ArchiveDealEpochs);
// the archiver supplies only the piece commitment of the bundle file, which is what the deal's
// providers prove. A range may hold at most types.MaxLiveDealsPerRange live deals, counting
// recorded ones and ones still waiting for a provider, so an archiver cannot open more paid
// deals than the range needs. The new deal is OPEN until x/storage assigns it in the next block,
// and only then can MsgAttachReplicas record it.
func (k Keeper) CreateArchiveDeal(ctx sdk.Context, msg *types.MsgCreateArchiveDeal) (uint64, bool, uint32, error) {
	if msg == nil {
		return 0, false, 0, fmt.Errorf("nil MsgCreateArchiveDeal")
	}
	if err := msg.ValidateBasic(); err != nil {
		return 0, false, 0, err
	}
	if err := requireFinalized(ctx, msg.EndHeight); err != nil {
		return 0, false, 0, err
	}
	archiver, err := types.ValidateArchiver(msg.Archiver)
	if err != nil {
		return 0, false, 0, err
	}
	operator, err := k.nodes.ArchiverOperator(ctx, msg.NodeId, archiver.String())
	if err != nil {
		return 0, false, 0, fmt.Errorf("archiver %s: %w", archiver, err)
	}
	rec, err := k.GetRange(ctx, msg.StartHeight, msg.EndHeight)
	if err != nil {
		return 0, false, 0, err
	}
	if !slices.Contains(rec.Operators, operator) {
		return 0, false, 0, fmt.Errorf("%w: operator %s did not attest range %d-%d", types.ErrNotAttester, operator, msg.StartHeight, msg.EndHeight)
	}
	live, err := k.dropEndedDeals(ctx, rec.DealIds)
	if err != nil {
		return 0, false, 0, err
	}
	pending, err := k.livePending(ctx, rec.StartHeight)
	if err != nil {
		return 0, false, 0, err
	}
	if len(live)+pending >= types.MaxLiveDealsPerRange {
		return 0, false, 0, fmt.Errorf("%w: %d-%d has %d recorded and %d pending", types.ErrDealsFull, msg.StartHeight, msg.EndHeight, len(live), pending)
	}
	dealID, err := k.storage.CreateArchiveDeal(ctx, msg.PieceRoot, msg.RealLeafCount, msg.PaddedLeafCount, msg.PieceBytes, types.ArchiveDealEpochs)
	if err != nil {
		return 0, false, 0, fmt.Errorf("failed to open the archive deal of range %d-%d: %w", msg.StartHeight, msg.EndHeight, err)
	}
	if err := k.PendingDeals.Set(ctx, collections.Join(rec.StartHeight, dealID)); err != nil {
		return 0, false, 0, fmt.Errorf("failed to record pending deal %d: %w", dealID, err)
	}
	if err := k.AttachedDeals.Set(ctx, dealID, rec.StartHeight); err != nil {
		return 0, false, 0, fmt.Errorf("failed to reserve deal %d for range %d: %w", dealID, rec.StartHeight, err)
	}
	if len(live) != len(rec.DealIds) {
		rec.DealIds = live
		if err := k.Ranges.Set(ctx, collections.Join(rec.StartHeight, rec.EndHeight), rec); err != nil {
			return 0, false, 0, fmt.Errorf("failed to store range %d-%d: %w", rec.StartHeight, rec.EndHeight, err)
		}
	}
	ctx.EventManager().EmitEvent(rangeEvent(types.EventTypeCreateArchiveDeal, rec, archiver.String()).
		AppendAttributes(sdk.NewAttribute(types.AttributeKeyDealID, strconv.FormatUint(dealID, 10))))
	return dealID, rec.Archived, uint32(len(live)), nil
}

// livePending counts the range's pending deals that x/storage still runs, and forgets the rest:
// a deal that ended or was refunded without ever getting a provider stops holding a slot of the
// range's allowance.
func (k Keeper) livePending(ctx sdk.Context, start int64) (int, error) {
	var ids []uint64
	rng := collections.NewPrefixedPairRange[int64, uint64](start)
	if err := k.PendingDeals.Walk(ctx, rng, func(key collections.Pair[int64, uint64]) (bool, error) {
		ids = append(ids, key.K2())
		return false, nil
	}); err != nil {
		return 0, fmt.Errorf("failed to walk pending deals of range %d: %w", start, err)
	}
	live := 0
	for _, id := range ids {
		ok, err := k.storage.ArchiveDealLive(ctx, id)
		if err != nil {
			return 0, fmt.Errorf("read deal %d: %w", id, err)
		}
		if ok {
			live++
			continue
		}
		if err := k.forgetPending(ctx, start, id); err != nil {
			return 0, err
		}
		if err := k.AttachedDeals.Remove(ctx, id); err != nil {
			return 0, fmt.Errorf("release ended deal %d: %w", id, err)
		}
	}
	return live, nil
}

func (k Keeper) forgetPending(ctx sdk.Context, start int64, id uint64) error {
	if err := k.PendingDeals.Remove(ctx, collections.Join(start, id)); err != nil {
		return fmt.Errorf("failed to forget pending deal %d: %w", id, err)
	}
	return nil
}
