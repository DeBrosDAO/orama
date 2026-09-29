package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// CreateDeal opens a user PRIVATE or PUBLIC_PIN deal. ARCHIVE and protocol
// deals are rejected: those are created only by CreateProtocolDeal.
func (k Keeper) CreateDeal(ctx sdk.Context, msg *types.MsgCreateDeal) (uint64, error) {
	if err := msg.ValidateBasic(); err != nil {
		return 0, err
	}
	p, err := k.params(ctx)
	if err != nil {
		return 0, err
	}
	if err := k.bumpDealCount(ctx, p); err != nil {
		return 0, err
	}
	for i := range msg.Pieces {
		if err := msg.Pieces[i].MinBytes(p.MinDealBytes); err != nil {
			return 0, fmt.Errorf("piece %d: %w", i, err)
		}
	}
	epoch, err := k.currentEpoch(ctx)
	if err != nil {
		return 0, err
	}
	escrow := msg.PricePerEpoch.MulRaw(int64(msg.Replicas)).MulRaw(int64(msg.DurationEpochs))
	payer := msg.Signer
	if msg.Granter != "" {
		if err := k.spendGrant(ctx, msg, p, epoch, escrow.Add(p.DealFee)); err != nil {
			return 0, err
		}
		payer = msg.Granter
	}
	payerAddr, err := parseAddr(payer)
	if err != nil {
		return 0, err
	}
	if msg.Granter == "" {
		// The signer pays with its own money, so its earnings may cover the shortfall. A grantor's
		// funds are never topped up from the signer's earnings.
		if err := k.earnings.FundSpendFromEarnings(ctx, payerAddr, params.BaseDenom, p.DealFee.Add(escrow)); err != nil {
			return 0, err
		}
	}
	if err := k.pullDealFunds(ctx, payerAddr, p.DealFee, escrow); err != nil {
		return 0, err
	}
	id, err := k.NextDealID.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to read next deal id: %w", err)
	}
	deal := types.Deal{
		Id:             id,
		Class:          msg.Class,
		Protocol:       false,
		Client:         payer,
		Granter:        msg.Granter,
		RepairDelegate: msg.RepairDelegate,
		DealNonce:      append([]byte(nil), msg.DealNonce...),
		Replicas:       msg.Replicas,
		PricePerEpoch:  msg.PricePerEpoch,
		DurationEpochs: msg.DurationEpochs,
		StartEpoch:     epoch,
		EndEpoch:       epoch + msg.DurationEpochs,
		Escrow:         escrow,
		Status:         types.DealStatus_DEAL_STATUS_OPEN,
		AssignAtHeight: ctx.BlockHeight() + 1,
		CreatedHeight:  ctx.BlockHeight(),
	}
	if err := k.saveDeal(ctx, deal); err != nil {
		return 0, err
	}
	if err := k.writeSlots(ctx, deal, msg.Pieces); err != nil {
		return 0, err
	}
	if err := k.Pending.Set(ctx, id); err != nil {
		return 0, err
	}
	if err := k.NextDealID.Set(ctx, id+1); err != nil {
		return 0, err
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("storage_deal_created",
		sdk.NewAttribute("deal_id", fmt.Sprintf("%d", id)),
		sdk.NewAttribute("class", deal.Class.String()),
		sdk.NewAttribute("protocol", "false"),
	))
	return id, nil
}

// CreateProtocolDeal is the only way to open an ARCHIVE or protocol PUBLIC_PIN
// deal. User messages cannot reach it.
func (k Keeper) CreateProtocolDeal(ctx sdk.Context, class types.DealClass, payload []byte, price math.Int, duration uint64) (uint64, error) {
	if len(payload) == 0 {
		return 0, fmt.Errorf("protocol deal payload is empty")
	}
	commitment, err := piece.Commit(payload)
	if err != nil {
		return 0, fmt.Errorf("failed to commit protocol payload: %w", err)
	}
	pc := types.PieceCommitment{
		Root:            commitment.Root,
		RealLeafCount:   commitment.RealLeafCount,
		PaddedLeafCount: commitment.PaddedLeafCount,
		PieceBytes:      uint64(len(payload)),
	}
	return k.createProtocolDeal(ctx, class, pc, price, duration)
}

// CreateArchiveDeal opens a protocol ARCHIVE deal over a bundle the chain does not hold: pc is the
// piece commitment of the bundle file, the price is the protocol per-replica price, and the deal
// runs for duration epochs. Only x/archive calls it, for a range it has already pinned; the
// deal's slots go to distinct operators, /16 networks and ASNs like every protocol deal.
func (k Keeper) CreateArchiveDeal(ctx sdk.Context, pc types.PieceCommitment, duration uint64) (uint64, error) {
	if err := pc.CheckShape(); err != nil {
		return 0, fmt.Errorf("archive piece: %w", err)
	}
	p, err := k.params(ctx)
	if err != nil {
		return 0, err
	}
	return k.createProtocolDeal(ctx, types.DealClass_DEAL_CLASS_ARCHIVE, pc, p.ProtocolPricePerEpoch, duration)
}

func (k Keeper) createProtocolDeal(ctx sdk.Context, class types.DealClass, pc types.PieceCommitment, price math.Int, duration uint64) (uint64, error) {
	if class != types.DealClass_DEAL_CLASS_ARCHIVE && class != types.DealClass_DEAL_CLASS_PUBLIC_PIN {
		return 0, fmt.Errorf("protocol deals must be ARCHIVE or PUBLIC_PIN, got %s", class)
	}
	if price.IsNil() || !price.IsPositive() {
		return 0, fmt.Errorf("protocol price must be positive")
	}
	if duration == 0 || duration > types.MaxDurationEpochs {
		return 0, fmt.Errorf("protocol duration must be in [1, %d]", types.MaxDurationEpochs)
	}
	epoch, err := k.currentEpoch(ctx)
	if err != nil {
		return 0, err
	}
	id, err := k.NextDealID.Get(ctx)
	if err != nil {
		return 0, err
	}
	deal := types.Deal{
		Id:             id,
		Class:          class,
		Protocol:       true,
		Replicas:       types.MinReplicas,
		PricePerEpoch:  price,
		DurationEpochs: duration,
		StartEpoch:     epoch,
		EndEpoch:       epoch + duration,
		Escrow:         math.ZeroInt(),
		Status:         types.DealStatus_DEAL_STATUS_OPEN,
		AssignAtHeight: ctx.BlockHeight() + 1,
		CreatedHeight:  ctx.BlockHeight(),
	}
	if err := k.saveDeal(ctx, deal); err != nil {
		return 0, err
	}
	if err := k.writeSlots(ctx, deal, []types.PieceCommitment{pc}); err != nil {
		return 0, err
	}
	if err := k.Pending.Set(ctx, id); err != nil {
		return 0, err
	}
	if err := k.NextDealID.Set(ctx, id+1); err != nil {
		return 0, err
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("storage_deal_created",
		sdk.NewAttribute("deal_id", fmt.Sprintf("%d", id)),
		sdk.NewAttribute("class", class.String()),
		sdk.NewAttribute("protocol", "true"),
	))
	return id, nil
}

func (k Keeper) writeSlots(ctx sdk.Context, deal types.Deal, pieces []types.PieceCommitment) error {
	for i := uint32(0); i < deal.Replicas; i++ {
		src := pieces[0]
		if deal.Class == types.DealClass_DEAL_CLASS_PRIVATE && !deal.Protocol {
			src = pieces[i]
		}
		slot := types.Slot{
			DealId:          deal.Id,
			Index:           i,
			PieceRoot:       append([]byte(nil), src.Root...),
			RealLeafCount:   src.RealLeafCount,
			PaddedLeafCount: src.PaddedLeafCount,
			PieceBytes:      src.PieceBytes,
			Status:          types.SlotStatus_SLOT_STATUS_UNASSIGNED,
		}
		if err := k.saveSlot(ctx, slot); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) bumpDealCount(ctx sdk.Context, p types.Params) error {
	height, err := k.DealCountHeight.Get(ctx)
	if err != nil {
		return err
	}
	count, err := k.DealsInBlock.Get(ctx)
	if err != nil {
		return err
	}
	if height != ctx.BlockHeight() {
		height = ctx.BlockHeight()
		count = 0
	}
	if count >= p.MaxDealsPerBlock {
		return fmt.Errorf("block already has %d deals, max is %d", count, p.MaxDealsPerBlock)
	}
	if err := k.DealCountHeight.Set(ctx, height); err != nil {
		return err
	}
	return k.DealsInBlock.Set(ctx, count+1)
}

func (k Keeper) pullDealFunds(ctx sdk.Context, payer sdk.AccAddress, fee, escrow math.Int) error {
	need := fee.Add(escrow)
	spendable := k.bank.SpendableCoins(ctx, payer).AmountOf(params.BaseDenom)
	if spendable.LT(need) {
		return fmt.Errorf("insufficient funds: need %s%s, have %s%s", need, params.BaseDenom, spendable, params.BaseDenom)
	}
	if err := k.bank.SendCoinsFromAccountToModule(ctx, payer, types.ModuleName, coins(need)); err != nil {
		return fmt.Errorf("failed to pull deal funds: %w", err)
	}
	if escrow.IsPositive() {
		if err := k.bank.SendCoinsFromModuleToModule(ctx, types.ModuleName, types.EscrowModuleName, coins(escrow)); err != nil {
			return fmt.Errorf("failed to lock escrow: %w", err)
		}
	}
	if fee.IsPositive() {
		if err := k.bank.BurnCoins(ctx, types.ModuleName, coins(fee)); err != nil {
			return fmt.Errorf("failed to burn deal fee: %w", err)
		}
	}
	return nil
}

func (k Keeper) spendGrant(ctx sdk.Context, msg *types.MsgCreateDeal, p types.Params, epoch uint64, cost math.Int) error {
	auth, err := k.Auths.Get(ctx, collections.Join(msg.Granter, msg.Signer))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return fmt.Errorf("no deal authorization from %s to %s", msg.Granter, msg.Signer)
		}
		return err
	}
	if auth.ExpiryEpoch != 0 && epoch >= auth.ExpiryEpoch {
		return fmt.Errorf("deal authorization from %s expired at epoch %d", msg.Granter, auth.ExpiryEpoch)
	}
	if auth.PeriodEpochs > 0 && epoch >= auth.PeriodStartEpoch+auth.PeriodEpochs {
		auth.Spent = math.ZeroInt()
		auth.PeriodStartEpoch = epoch
	}
	if auth.Spent.IsNil() {
		auth.Spent = math.ZeroInt()
	}
	if auth.Spent.Add(cost).GT(auth.SpendLimit) {
		return fmt.Errorf("deal authorization spend %s exceeds remaining limit %s", cost, auth.SpendLimit.Sub(auth.Spent))
	}
	if msg.Replicas != auth.Replicas {
		return fmt.Errorf("deal replicas %d do not match the grant's fixed count %d", msg.Replicas, auth.Replicas)
	}
	if msg.DurationEpochs > auth.MaxDurationEpochs {
		return fmt.Errorf("duration %d exceeds the grant maximum %d", msg.DurationEpochs, auth.MaxDurationEpochs)
	}
	for i := range msg.Pieces {
		if msg.Pieces[i].PieceBytes > auth.MaxPieceBytes {
			return fmt.Errorf("piece %d is %d bytes, grant maximum is %d", i, msg.Pieces[i].PieceBytes, auth.MaxPieceBytes)
		}
		_ = p
	}
	auth.Spent = auth.Spent.Add(cost)
	return k.Auths.Set(ctx, collections.Join(msg.Granter, msg.Signer), auth)
}

// ExtendDeal adds epochs and escrow. The current epoch's subsidy is unchanged:
// settlement prices one epoch at a time.
func (k Keeper) ExtendDeal(ctx sdk.Context, msg *types.MsgExtendDeal) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	deal, err := k.loadDeal(ctx, msg.DealId)
	if err != nil {
		return err
	}
	if deal.Protocol {
		return fmt.Errorf("protocol deals cannot be extended")
	}
	if deal.Status == types.DealStatus_DEAL_STATUS_REFUNDED || deal.Status == types.DealStatus_DEAL_STATUS_EXPIRED {
		return fmt.Errorf("deal %d is %s", deal.Id, deal.Status)
	}
	if deal.DurationEpochs+msg.ExtraEpochs > types.MaxDurationEpochs {
		return fmt.Errorf("extended duration exceeds %d epochs", types.MaxDurationEpochs)
	}
	signer, err := parseAddr(msg.Signer)
	if err != nil {
		return err
	}
	client, err := parseAddr(deal.Client)
	if err != nil {
		return err
	}
	if !signer.Equals(client) {
		return fmt.Errorf("only the paying client can extend deal %d", deal.Id)
	}
	extra := deal.PricePerEpoch.MulRaw(int64(deal.Replicas)).MulRaw(int64(msg.ExtraEpochs))
	if err := k.earnings.FundSpendFromEarnings(ctx, client, params.BaseDenom, extra); err != nil {
		return err
	}
	if err := k.pullDealFunds(ctx, client, math.ZeroInt(), extra); err != nil {
		return err
	}
	deal.DurationEpochs += msg.ExtraEpochs
	deal.EndEpoch += msg.ExtraEpochs
	deal.Escrow = deal.Escrow.Add(extra)
	return k.saveDeal(ctx, deal)
}

// GrantDealAuthorization records a backup grant. It is not an x/authz grant.
func (k Keeper) GrantDealAuthorization(ctx sdk.Context, msg *types.MsgGrantDealAuthorization) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	epoch, err := k.currentEpoch(ctx)
	if err != nil {
		return err
	}
	auth := types.DealAuthorization{
		Granter:           msg.Signer,
		Grantee:           msg.Grantee,
		SpendLimit:        msg.SpendLimit,
		Spent:             math.ZeroInt(),
		PeriodEpochs:      msg.PeriodEpochs,
		PeriodStartEpoch:  epoch,
		MaxPieceBytes:     msg.MaxPieceBytes,
		MaxDurationEpochs: msg.MaxDurationEpochs,
		Replicas:          msg.Replicas,
		ExpiryEpoch:       msg.ExpiryEpoch,
	}
	if err := k.Auths.Set(ctx, collections.Join(msg.Signer, msg.Grantee), auth); err != nil {
		return fmt.Errorf("failed to store deal authorization: %w", err)
	}
	return nil
}

// RevokeDealAuthorization removes a grant. Existing deals are left in place.
func (k Keeper) RevokeDealAuthorization(ctx sdk.Context, msg *types.MsgRevokeDealAuthorization) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	has, err := k.Auths.Has(ctx, collections.Join(msg.Signer, msg.Grantee))
	if err != nil {
		return err
	}
	if !has {
		return fmt.Errorf("no deal authorization from %s to %s", msg.Signer, msg.Grantee)
	}
	return k.Auths.Remove(ctx, collections.Join(msg.Signer, msg.Grantee))
}

func (k Keeper) returnEscrow(ctx sdk.Context, deal *types.Deal) error {
	if !deal.Escrow.IsPositive() {
		deal.Escrow = math.ZeroInt()
		return nil
	}
	if deal.Client == "" {
		return fmt.Errorf("deal %d holds escrow %s but has no client", deal.Id, deal.Escrow)
	}
	client, err := parseAddr(deal.Client)
	if err != nil {
		return err
	}
	amount := deal.Escrow
	if err := k.earnings.CreditEarnings(ctx, types.EscrowModuleName, client, coin(amount)); err != nil {
		return fmt.Errorf("failed to refund deal %d escrow: %w", deal.Id, err)
	}
	deal.Escrow = math.ZeroInt()
	return nil
}

func (k Keeper) refundUnassigned(ctx sdk.Context, deal types.Deal) error {
	slots, err := k.dealSlots(ctx, deal.Id)
	if err != nil {
		return err
	}
	for i := range slots {
		if err := k.detachSlot(ctx, &slots[i]); err != nil {
			return err
		}
		slots[i].Status = types.SlotStatus_SLOT_STATUS_UNASSIGNED
		slots[i].ExcludedOperator = ""
		if err := k.saveSlot(ctx, slots[i]); err != nil {
			return err
		}
	}
	if err := k.returnEscrow(ctx, &deal); err != nil {
		return err
	}
	deal.Status = types.DealStatus_DEAL_STATUS_REFUNDED
	if err := k.saveDeal(ctx, deal); err != nil {
		return err
	}
	return k.Pending.Remove(ctx, deal.Id)
}
