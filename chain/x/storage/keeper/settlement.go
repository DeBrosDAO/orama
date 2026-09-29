package keeper

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

type scored struct {
	nodeID   string
	operator string
	dealID   uint64
	slot     uint32
	proved   bool
	escrow   math.Int
	claim    int
	subsidy  bool
}

func (k Keeper) closeEpoch(ctx sdk.Context, epoch uint64) error {
	var records []struct {
		key collections.Pair[uint64, string]
		rec types.ChallengeRecord
	}
	err := k.Challenges.Walk(ctx, collections.NewPrefixedPairRange[uint64, string](epoch), func(key collections.Pair[uint64, string], rec types.ChallengeRecord) (bool, error) {
		records = append(records, struct {
			key collections.Pair[uint64, string]
			rec types.ChallengeRecord
		}{key, rec})
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("failed to walk epoch %d challenges: %w", epoch, err)
	}
	if len(records) == 0 {
		return nil
	}
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	active, err := k.countActiveOperators(ctx)
	if err != nil {
		return err
	}
	rate := types.SubsidyRate(active, p.SMinProviders, p.SFullProviders)
	ceiling, err := k.emission.StorageCeiling(ctx, epoch)
	if err != nil {
		return fmt.Errorf("failed to read storage ceiling for epoch %d: %w", epoch, err)
	}
	if ceiling.IsNil() {
		ceiling = math.ZeroInt()
	}
	cap := types.OperatorSubsidyCap(ceiling, p.SMinProviders)

	var claims []types.MintClaim
	var rows []scored
	for _, item := range records {
		dealID, slotIdx, nodeID, err := parseChallengeID(item.key.K2())
		if err != nil {
			return err
		}
		slot, err := k.loadSlot(ctx, dealID, slotIdx)
		if err != nil {
			return err
		}
		deal, err := k.loadDeal(ctx, dealID)
		if err != nil {
			return err
		}
		row := scored{
			nodeID: nodeID,
			dealID: dealID,
			slot:   slotIdx,
			proved: item.rec.Proved,
			escrow: math.ZeroInt(),
			claim:  -1,
		}
		// A proof is paid only while the node that proved it still holds the
		// slot. A slot released or re-bound before the epoch closed has no
		// operator to pay for this proof; minting for it would strand coins.
		paid := item.rec.Proved && slot.NodeId == nodeID && slot.Operator != ""
		if paid {
			row.operator = slot.Operator
		}
		if paid && !deal.Protocol {
			row.escrow = deal.PricePerEpoch
		}
		if paid {
			switch {
			case deal.Protocol && deal.Class == types.DealClass_DEAL_CLASS_ARCHIVE:
				claims = append(claims, types.MintClaim{
					Operator: slot.Operator, Kind: types.MintArchive,
					Amount: deal.PricePerEpoch, FullPrice: deal.PricePerEpoch,
				})
				row.claim = len(claims) - 1
			case deal.Protocol && deal.Class == types.DealClass_DEAL_CLASS_PUBLIC_PIN:
				claims = append(claims, types.MintClaim{
					Operator: slot.Operator, Kind: types.MintProtocol,
					Amount: deal.PricePerEpoch, FullPrice: deal.PricePerEpoch,
				})
				row.claim = len(claims) - 1
			case !deal.Protocol && deal.Class == types.DealClass_DEAL_CLASS_PRIVATE:
				sub := types.EpochSubsidyDemand(deal.PricePerEpoch, rate, 0)
				if sub.IsPositive() {
					claims = append(claims, types.MintClaim{
						Operator: slot.Operator, Kind: types.MintSubsidy,
						Amount: sub, FullPrice: sub,
					})
					row.claim = len(claims) - 1
					row.subsidy = true
				}
			}
		}
		rows = append(rows, row)
	}
	mints, tops := types.ComputeMintPlan(claims, ceiling, cap)
	if err := k.reserveMints(ctx, epoch, mints); err != nil {
		return err
	}
	for _, row := range rows {
		mint := math.ZeroInt()
		top := math.ZeroInt()
		if row.claim >= 0 {
			mint = mints[row.claim]
			top = tops[row.claim]
		}
		if err := k.enqueue(ctx, types.Settlement{
			Epoch:        epoch,
			DealId:       row.dealID,
			Slot:         row.slot,
			NodeId:       row.nodeID,
			Operator:     row.operator,
			Proved:       row.proved,
			EscrowPay:    row.escrow,
			MintPay:      mint,
			ArchiveTopUp: top,
			Subsidy:      row.subsidy,
		}); err != nil {
			return err
		}
	}
	for _, item := range records {
		if err := k.Challenges.Remove(ctx, item.key); err != nil {
			return err
		}
	}
	return nil
}

// reserveMints has x/emission mint the epoch's whole storage payment into the
// storage module account when the epoch closes, while its ceiling record is
// certain to exist. Settlement later pays each item from that reserve, so a
// settlement queue that lags past x/emission's ceiling window never needs a
// pruned record.
func (k Keeper) reserveMints(ctx sdk.Context, epoch uint64, mints []math.Int) error {
	total := math.ZeroInt()
	for _, m := range mints {
		total = total.Add(m)
	}
	if !total.IsPositive() {
		return nil
	}
	if err := k.emission.MintStorageService(ctx, epoch, total); err != nil {
		return fmt.Errorf("failed to reserve epoch %d storage payments: %w", epoch, err)
	}
	return nil
}

func parseChallengeID(id string) (dealID uint64, slot uint32, nodeID string, err error) {
	// id is node|deal|slot. Node ids cannot contain '|'.
	n := 0
	for i := 0; i < len(id); i++ {
		if id[i] == '|' {
			n++
		}
	}
	if n != 2 {
		return 0, 0, "", fmt.Errorf("bad challenge id %q", id)
	}
	parts := split3(id)
	nodeID = parts[0]
	if _, err = fmt.Sscanf(parts[1]+" "+parts[2], "%d %d", &dealID, &slot); err != nil {
		return 0, 0, "", fmt.Errorf("bad challenge id %q: %w", id, err)
	}
	return dealID, slot, nodeID, nil
}

func split3(s string) [3]string {
	var out [3]string
	n := 0
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '|' {
			out[n] = s[start:i]
			n++
			start = i + 1
		}
	}
	out[2] = s[start:]
	return out
}

func (k Keeper) enqueue(ctx sdk.Context, item types.Settlement) error {
	if item.EscrowPay.IsNil() {
		item.EscrowPay = math.ZeroInt()
	}
	if item.MintPay.IsNil() {
		item.MintPay = math.ZeroInt()
	}
	if item.ArchiveTopUp.IsNil() {
		item.ArchiveTopUp = math.ZeroInt()
	}
	tail, err := k.QueueTail.Get(ctx)
	if err != nil {
		return err
	}
	item.Seq = tail
	if err := k.Queue.Set(ctx, tail, item); err != nil {
		return fmt.Errorf("failed to enqueue settlement: %w", err)
	}
	if err := k.QueueTail.Set(ctx, tail+1); err != nil {
		return err
	}
	return k.addQueuePending(ctx, item.DealId, 1)
}

// SettleQueue applies at most max_settlements_per_block queue entries.
func (k Keeper) SettleQueue(ctx sdk.Context) error {
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	head, err := k.QueueHead.Get(ctx)
	if err != nil {
		return err
	}
	tail, err := k.QueueTail.Get(ctx)
	if err != nil {
		return err
	}
	var n uint64
	for head < tail && n < p.MaxSettlementsPerBlock {
		item, err := k.Queue.Get(ctx, head)
		if err != nil {
			return fmt.Errorf("failed to load settlement %d: %w", head, err)
		}
		if err := k.applySettlement(ctx, p, item); err != nil {
			return fmt.Errorf("settlement %d: %w", head, err)
		}
		if err := k.Queue.Remove(ctx, head); err != nil {
			return err
		}
		if err := k.addQueuePending(ctx, item.DealId, -1); err != nil {
			return err
		}
		head++
		n++
	}
	return k.QueueHead.Set(ctx, head)
}

func (k Keeper) applySettlement(ctx sdk.Context, p types.Params, item types.Settlement) error {
	deal, err := k.loadDeal(ctx, item.DealId)
	if err != nil {
		return err
	}
	slot, err := k.loadSlot(ctx, item.DealId, item.Slot)
	if err != nil {
		return err
	}
	// A row is about the node that was challenged. If the slot has since gone
	// to another node, that node's miss count and rechallenge are not this
	// row's to change.
	holder := slot.NodeId != "" && slot.NodeId == item.NodeId
	if !item.Proved {
		if !holder {
			return nil
		}
		return k.applyMiss(ctx, p, deal, slot)
	}
	if holder {
		slot.ConsecutiveMisses = 0
		if err := k.Rechallenge.Remove(ctx, collections.Join(slot.NodeId, rechallengeID(slot.DealId, slot.Index))); err != nil {
			return err
		}
		if err := k.saveSlot(ctx, slot); err != nil {
			return err
		}
	}
	credited, err := k.payItem(ctx, &deal, item)
	if err != nil {
		return err
	}
	if err := k.saveDeal(ctx, deal); err != nil {
		return err
	}
	return k.noteService(ctx, p, item, credited)
}

func (k Keeper) applyMiss(ctx sdk.Context, p types.Params, deal types.Deal, slot types.Slot) error {
	slot.ConsecutiveMisses++
	if slot.ConsecutiveMisses >= 2 && slot.NodeId != "" {
		slashAmt := types.ApplyRate(deal.PricePerEpoch, p.SlashFraction)
		if slashAmt.IsPositive() {
			if err := k.nodes.Slash(ctx, slot.NodeId, slashAmt); err != nil {
				return fmt.Errorf("failed to slash %s: %w", slot.NodeId, err)
			}
		}
	}
	if slot.ConsecutiveMisses >= p.MissThreshold && slot.NodeId != "" {
		op := slot.Operator
		nodeID := slot.NodeId
		if err := k.detachSlot(ctx, &slot); err != nil {
			return err
		}
		slot.ExcludedOperator = op
		slot.Status = types.SlotStatus_SLOT_STATUS_UNASSIGNED
		slot.ConsecutiveMisses = 0
		if err := k.saveSlot(ctx, slot); err != nil {
			return err
		}
		deal.AssignAtHeight = ctx.BlockHeight() + 1
		if err := k.saveDeal(ctx, deal); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent("storage_slot_evicted",
			sdk.NewAttribute("deal_id", fmt.Sprintf("%d", deal.Id)),
			sdk.NewAttribute("slot", fmt.Sprintf("%d", slot.Index)),
			sdk.NewAttribute("node_id", nodeID),
		))
		return k.Pending.Set(ctx, deal.Id)
	}
	if slot.NodeId != "" {
		if err := k.Rechallenge.Set(ctx, collections.Join(slot.NodeId, rechallengeID(slot.DealId, slot.Index))); err != nil {
			return err
		}
	}
	return k.saveSlot(ctx, slot)
}

func (k Keeper) payItem(ctx sdk.Context, deal *types.Deal, item types.Settlement) (credited bool, err error) {
	if item.Operator == "" {
		return false, nil
	}
	operator, err := parseAddr(item.Operator)
	if err != nil {
		return false, err
	}
	if item.EscrowPay.IsPositive() {
		if deal.Escrow.LT(item.EscrowPay) {
			return false, fmt.Errorf("deal %d escrow %s cannot cover %s", deal.Id, deal.Escrow, item.EscrowPay)
		}
		deal.Escrow = deal.Escrow.Sub(item.EscrowPay)
		if err := k.payService(ctx, types.EscrowModuleName, operator, item.EscrowPay); err != nil {
			return false, err
		}
		credited = true
	}
	if item.MintPay.IsPositive() {
		// Minted when the epoch closed (reserveMints); paid from that reserve.
		if err := k.payService(ctx, types.ModuleName, operator, item.MintPay); err != nil {
			return false, err
		}
		cur, err := k.epochMinted(ctx, item.Epoch)
		if err != nil {
			return false, err
		}
		if err := k.EpochMinted.Set(ctx, item.Epoch, cur.Add(item.MintPay)); err != nil {
			return false, err
		}
		if item.Subsidy {
			already, err := k.operatorMinted(ctx, item.Epoch, item.Operator)
			if err != nil {
				return false, err
			}
			if err := k.OperatorMinted.Set(ctx, collections.Join(item.Epoch, item.Operator), already.Add(item.MintPay)); err != nil {
				return false, err
			}
		}
		credited = true
	}
	if item.ArchiveTopUp.IsPositive() {
		fund, err := k.ArchiveFund.Get(ctx)
		if err != nil {
			return false, err
		}
		top := item.ArchiveTopUp
		if top.GT(fund) {
			top = fund
		}
		if top.IsPositive() {
			if err := k.bank.SendCoinsFromModuleToModule(ctx, types.ArchiveModuleName, types.ModuleName, coins(top)); err != nil {
				return false, fmt.Errorf("failed to draw archive top-up: %w", err)
			}
			if err := k.ArchiveFund.Set(ctx, fund.Sub(top)); err != nil {
				return false, err
			}
			if err := k.payService(ctx, types.ModuleName, operator, top); err != nil {
				return false, err
			}
			credited = true
		}
	}
	return credited, nil
}

func (k Keeper) payService(ctx sdk.Context, source string, operator sdk.AccAddress, amount math.Int) error {
	bal := k.bank.GetBalance(ctx, authtypes.NewModuleAddress(source), params.BaseDenom).Amount
	if bal.LT(amount) {
		return fmt.Errorf("module %s holds %s, need %s to pay %s", source, bal, amount, operator)
	}
	toProvider, burn, archive := types.SplitServicePayment(amount)
	if toProvider.IsPositive() {
		if err := k.earnings.CreditEarnings(ctx, source, operator, coin(toProvider)); err != nil {
			return fmt.Errorf("failed to credit earnings: %w", err)
		}
	}
	if burn.IsPositive() {
		if err := k.bank.BurnCoins(ctx, source, coins(burn)); err != nil {
			return fmt.Errorf("failed to burn service share: %w", err)
		}
	}
	if archive.IsPositive() {
		if err := k.bank.SendCoinsFromModuleToModule(ctx, source, types.ArchiveModuleName, coins(archive)); err != nil {
			return fmt.Errorf("failed to fund archive: %w", err)
		}
		fund, err := k.ArchiveFund.Get(ctx)
		if err != nil {
			return err
		}
		if err := k.ArchiveFund.Set(ctx, fund.Add(archive)); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) noteService(ctx sdk.Context, p types.Params, item types.Settlement, credited bool) error {
	if item.NodeId == "" {
		return nil
	}
	state, err := k.Nodes.Get(ctx, item.NodeId)
	if err != nil {
		return err
	}
	state.EverProved = true
	if credited && state.Probation && !state.Graduated && !state.DepositLocked {
		op, err := parseAddr(item.Operator)
		if err != nil {
			return err
		}
		if err := k.deposits.LockDeposit(ctx, op, probationDepositID(item.NodeId), p.ProbationDeposit); err != nil {
			return fmt.Errorf("failed to lock probation deposit for %s: %w", item.NodeId, err)
		}
		state.DepositLocked = true
	}
	return k.Nodes.Set(ctx, item.NodeId, state)
}

func (k Keeper) epochMinted(ctx sdk.Context, epoch uint64) (math.Int, error) {
	v, err := k.EpochMinted.Get(ctx, epoch)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return math.ZeroInt(), nil
		}
		return math.Int{}, err
	}
	if v.IsNil() {
		return math.ZeroInt(), nil
	}
	return v, nil
}

func (k Keeper) operatorMinted(ctx sdk.Context, epoch uint64, operator string) (math.Int, error) {
	v, err := k.OperatorMinted.Get(ctx, collections.Join(epoch, operator))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return math.ZeroInt(), nil
		}
		return math.Int{}, err
	}
	if v.IsNil() {
		return math.ZeroInt(), nil
	}
	return v, nil
}

func (k Keeper) countActiveOperators(ctx sdk.Context) (uint64, error) {
	seen := map[string]struct{}{}
	err := k.Nodes.Walk(ctx, nil, func(id string, _ types.NodeState) (bool, error) {
		active, err := k.nodes.IsActive(ctx, id)
		if err != nil {
			return false, err
		}
		if !active {
			return false, nil
		}
		op, err := k.nodes.Operator(ctx, id)
		if err != nil {
			return false, err
		}
		seen[op] = struct{}{}
		return false, nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to count active operators: %w", err)
	}
	return uint64(len(seen)), nil
}

func (k Keeper) expireDeals(ctx sdk.Context, epoch uint64) error {
	var ids []uint64
	if err := k.Deals.Walk(ctx, nil, func(id uint64, deal types.Deal) (bool, error) {
		if deal.Status == types.DealStatus_DEAL_STATUS_REFUNDED || deal.Status == types.DealStatus_DEAL_STATUS_EXPIRED {
			return false, nil
		}
		if epoch >= deal.EndEpoch {
			ids = append(ids, id)
		}
		return false, nil
	}); err != nil {
		return err
	}
	for _, id := range ids {
		pending, err := k.queuePending(ctx, id)
		if err != nil {
			return err
		}
		if pending > 0 {
			continue
		}
		deal, err := k.loadDeal(ctx, id)
		if err != nil {
			return err
		}
		slots, err := k.dealSlots(ctx, id)
		if err != nil {
			return err
		}
		for i := range slots {
			if err := k.detachSlot(ctx, &slots[i]); err != nil {
				return err
			}
			slots[i].Status = types.SlotStatus_SLOT_STATUS_UNASSIGNED
			if err := k.saveSlot(ctx, slots[i]); err != nil {
				return err
			}
		}
		if err := k.returnEscrow(ctx, &deal); err != nil {
			return err
		}
		deal.Status = types.DealStatus_DEAL_STATUS_EXPIRED
		if err := k.saveDeal(ctx, deal); err != nil {
			return err
		}
		if err := k.Pending.Remove(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) expireProbation(ctx sdk.Context, epoch uint64) error {
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	var due []types.NodeState
	if err := k.Nodes.Walk(ctx, nil, func(_ string, state types.NodeState) (bool, error) {
		if state.Probation && !state.Graduated && epoch >= state.RegisteredEpoch+p.ProbationExpiryEpochs {
			due = append(due, state)
		}
		return false, nil
	}); err != nil {
		return err
	}
	for _, state := range due {
		if state.EverProved {
			if state.DepositLocked {
				if _, _, err := k.deposits.ReleaseDeposit(ctx, probationDepositID(state.NodeId)); err != nil {
					return fmt.Errorf("failed to recover probation deposit of %s: %w", state.NodeId, err)
				}
				state.DepositLocked = false
			}
			state.Probation = false
			state.Graduated = true
		} else {
			if err := k.nodes.Jail(ctx, state.NodeId); err != nil {
				return fmt.Errorf("failed to jail probation node %s: %w", state.NodeId, err)
			}
			state.Probation = false
		}
		if err := k.Nodes.Set(ctx, state.NodeId, state); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) maybeProtocolDeals(ctx sdk.Context, epoch uint64) error {
	p, err := k.params(ctx)
	if err != nil {
		return err
	}
	if p.ProtocolEveryEpochs == 0 || epoch == 0 || epoch%p.ProtocolEveryEpochs != 0 {
		return nil
	}
	last, err := k.LastProtocolEpoch.Get(ctx)
	if err != nil {
		return err
	}
	if last == epoch {
		return nil
	}
	for _, class := range []types.DealClass{types.DealClass_DEAL_CLASS_ARCHIVE, types.DealClass_DEAL_CLASS_PUBLIC_PIN} {
		payload := protocolPayload(epoch, class, p.ProtocolPieceBytes)
		if _, err := k.CreateProtocolDeal(ctx, class, payload, p.ProtocolPricePerEpoch, p.ProtocolDurationEpochs); err != nil {
			return err
		}
	}
	return k.LastProtocolEpoch.Set(ctx, epoch)
}

func protocolPayload(epoch uint64, class types.DealClass, size uint64) []byte {
	prefix := fmt.Sprintf("orama-protocol/%s/%d/", class.String(), epoch)
	buf := make([]byte, size)
	copy(buf, prefix)
	return buf
}
