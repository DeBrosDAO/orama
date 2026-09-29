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
		var row scored
		var claim *types.MintClaim
		failed, err := k.isolate(ctx, FailureKindScoring, challengeSubject(item.key.K2()), func(c sdk.Context) error {
			var serr error
			row, claim, serr = k.scoreRecord(c, item.key.K2(), item.rec, rate)
			return serr
		})
		if err != nil {
			return err
		}
		if failed {
			continue
		}
		if claim != nil {
			claims = append(claims, *claim)
			row.claim = len(claims) - 1
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

// challengeSubject names a challenge row in the failure counters: its node id when the key
// parses, the raw key otherwise.
func challengeSubject(id string) string {
	_, _, nodeID, err := parseChallengeID(id)
	if err != nil {
		return "challenge/" + id
	}
	return nodeID
}

// scoreRecord turns one closed-epoch challenge into a settlement row and, when the proof earns a
// protocol or subsidy mint, the claim to feed the mint plan. It reads state and writes none.
func (k Keeper) scoreRecord(ctx sdk.Context, id string, rec types.ChallengeRecord, rate math.LegacyDec) (scored, *types.MintClaim, error) {
	dealID, slotIdx, nodeID, err := parseChallengeID(id)
	if err != nil {
		return scored{}, nil, err
	}
	slot, err := k.loadSlot(ctx, dealID, slotIdx)
	if err != nil {
		return scored{}, nil, err
	}
	deal, err := k.loadDeal(ctx, dealID)
	if err != nil {
		return scored{}, nil, err
	}
	row := scored{
		nodeID: nodeID,
		dealID: dealID,
		slot:   slotIdx,
		proved: rec.Proved,
		escrow: math.ZeroInt(),
		claim:  -1,
	}
	// A proof is paid only while the node that proved it still holds the
	// slot. A slot released or re-bound before the epoch closed has no
	// operator to pay for this proof; minting for it would strand coins.
	paid := rec.Proved && slot.NodeId == nodeID && slot.Operator != ""
	if !paid {
		return row, nil, nil
	}
	row.operator = slot.Operator
	if !deal.Protocol {
		row.escrow = deal.PricePerEpoch
	}
	var claim *types.MintClaim
	switch {
	case deal.Protocol && deal.Class == types.DealClass_DEAL_CLASS_ARCHIVE:
		claim = &types.MintClaim{Operator: slot.Operator, Kind: types.MintArchive, Amount: deal.PricePerEpoch, FullPrice: deal.PricePerEpoch}
	case deal.Protocol && deal.Class == types.DealClass_DEAL_CLASS_PUBLIC_PIN:
		claim = &types.MintClaim{Operator: slot.Operator, Kind: types.MintProtocol, Amount: deal.PricePerEpoch, FullPrice: deal.PricePerEpoch}
	case !deal.Protocol && deal.Class == types.DealClass_DEAL_CLASS_PRIVATE:
		sub := types.EpochSubsidyDemand(deal.PricePerEpoch, rate, 0)
		if sub.IsPositive() {
			claim = &types.MintClaim{Operator: slot.Operator, Kind: types.MintSubsidy, Amount: sub, FullPrice: sub}
			row.subsidy = true
		}
	}
	return row, claim, nil
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
		return 0, 0, "", rejectf("bad challenge id %q", id)
	}
	parts := split3(id)
	nodeID = parts[0]
	if _, err = fmt.Sscanf(parts[1]+" "+parts[2], "%d %d", &dealID, &slot); err != nil {
		return 0, 0, "", reject(fmt.Errorf("bad challenge id %q: %w", id, err))
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
		if err := k.settleOne(ctx, p, item); err != nil {
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

// settleOne applies one queue row on its own cache branch. A row that cannot be applied (the
// operator cannot be paid, a module account is short, the deal or slot record is unreadable) has its
// writes rolled back, a storage_item_failed event and the node's settlement failure count report it,
// and the queue moves on. Returning the error instead would fail EndBlock, and with it FinalizeBlock
// on every validator, over one node's payout.
//
// A failed row is not lost on the first failure: it is queued again behind the rows already waiting
// with its attempts raised, so a failure that clears by itself does not cost the operator the
// payout. On the MaxSettlementAttempts-th failure the row is dropped: it pays nothing, the deal keeps
// the escrow the row would have moved (returned to the client when the deal expires), and any
// subsidy or protocol mint reserved for the row is burned so the storage account keeps holding
// exactly the mint payments still queued. A failure that is not about the row (see isItemFailure)
// is returned and fails the block.
func (k Keeper) settleOne(ctx sdk.Context, p types.Params, item types.Settlement) error {
	subject := item.NodeId
	if subject == "" {
		subject = dealSubject(item.DealId)
	}
	failed, err := k.isolate(ctx, FailureKindSettlement, subject, func(c sdk.Context) error {
		return k.applySettlement(c, p, item)
	})
	if err != nil || !failed {
		return err
	}
	item.Attempts++
	if item.Attempts < types.MaxSettlementAttempts {
		if err := k.enqueue(ctx, item); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent("storage_settlement_requeued",
			sdk.NewAttribute("deal_id", fmt.Sprintf("%d", item.DealId)),
			sdk.NewAttribute("node_id", item.NodeId),
			sdk.NewAttribute("attempts", fmt.Sprintf("%d", item.Attempts)),
		))
		return nil
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("storage_settlement_dropped",
		sdk.NewAttribute("deal_id", fmt.Sprintf("%d", item.DealId)),
		sdk.NewAttribute("node_id", item.NodeId),
		sdk.NewAttribute("attempts", fmt.Sprintf("%d", item.Attempts)),
	))
	if !item.MintPay.IsPositive() {
		return nil
	}
	return k.burnDroppedMint(ctx, item)
}

// burnDroppedMint burns the mint reserved for a dropped row. It must succeed: the storage account
// holds the mint of every queued row, so a mint that stays behind breaks the storage invariant.
func (k Keeper) burnDroppedMint(ctx sdk.Context, item types.Settlement) error {
	if err := k.bank.BurnCoins(ctx, types.ModuleName, coins(item.MintPay)); err != nil {
		return fmt.Errorf("failed to burn the %s reserved for dropped settlement %d: %w", item.MintPay, item.Seq, err)
	}
	return nil
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
	paid, err := k.payItem(ctx, &deal, item)
	if err != nil {
		return err
	}
	if err := k.saveDeal(ctx, deal); err != nil {
		return err
	}
	return k.noteService(ctx, p, item, paid)
}

// applyMiss records one missed challenge: the node's consecutive misses rise, a slot at the miss
// threshold is evicted, any other missed slot is challenged again next epoch, and from the second
// consecutive miss on the node is penalized. The miss and the eviction never depend on the penalty:
// a node that cannot be slashed (its capacity state, no bond, a refusing collaborator) still loses
// a slot it does not serve.
func (k Keeper) applyMiss(ctx sdk.Context, p types.Params, deal types.Deal, slot types.Slot) error {
	slot.ConsecutiveMisses++
	nodeID := slot.NodeId
	penalized := slot.ConsecutiveMisses >= 2 && nodeID != ""
	switch {
	case slot.ConsecutiveMisses >= p.MissThreshold && nodeID != "":
		if err := k.evictSlot(ctx, deal, slot); err != nil {
			return err
		}
	default:
		if nodeID != "" {
			if err := k.Rechallenge.Set(ctx, collections.Join(nodeID, rechallengeID(slot.DealId, slot.Index))); err != nil {
				return err
			}
		}
		if err := k.saveSlot(ctx, slot); err != nil {
			return err
		}
	}
	if !penalized {
		return nil
	}
	_, err := k.isolate(ctx, FailureKindSlash, nodeID, func(c sdk.Context) error {
		return k.penalize(c, p, deal, nodeID)
	})
	return err
}

// evictSlot takes a slot from the node that holds it: the node's replica and reserved bytes go
// (detachSlot), the operator cannot be assigned the slot again, and the deal is queued for
// reassignment in the next block.
func (k Keeper) evictSlot(ctx sdk.Context, deal types.Deal, slot types.Slot) error {
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

// penalize applies the C7 slash of a node with consecutive misses: slash_fraction of one epoch's
// price of the missed deal. A bonded node is slashed through x/nodes, which clamps its declared
// capacity to the smaller backing; the replicas that no longer fit are then evicted. A probation
// node has no bond: the record deposit taken from its first earnings is its stake and is slashed
// instead, and a probation node that has not earned a deposit yet has nothing to slash.
func (k Keeper) penalize(ctx sdk.Context, p types.Params, deal types.Deal, nodeID string) error {
	amount := types.ApplyRate(deal.PricePerEpoch, p.SlashFraction)
	if !amount.IsPositive() {
		return nil
	}
	state, err := k.Nodes.Get(ctx, nodeID)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("failed to load tracked state of %s: %w", nodeID, err)
	}
	if err == nil && state.DepositLocked {
		return k.slashProbationDeposit(ctx, state, amount)
	}
	if err := k.nodes.Slash(ctx, nodeID, amount); err != nil {
		return fmt.Errorf("failed to slash %s: %w", nodeID, err)
	}
	return k.trimReserved(ctx, nodeID)
}

// slashProbationDeposit burns up to amount of a probation node's record deposit. A deposit burned
// in full is gone, so the node no longer counts as holding one: it is opened again from its next
// earnings.
func (k Keeper) slashProbationDeposit(ctx sdk.Context, state types.NodeState, amount math.Int) error {
	id := probationDepositID(state.NodeId)
	held, found, err := k.deposits.DepositAmount(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to read probation deposit of %s: %w", state.NodeId, err)
	}
	if !found {
		return rejectf("probation node %s is marked as holding a deposit but none is open", state.NodeId)
	}
	burned, err := k.deposits.SlashDeposit(ctx, id, amount)
	if err != nil {
		return fmt.Errorf("failed to slash probation deposit of %s: %w", state.NodeId, err)
	}
	if burned.GTE(held) {
		state.DepositLocked = false
		if err := k.Nodes.Set(ctx, state.NodeId, state); err != nil {
			return fmt.Errorf("failed to record the burned probation deposit of %s: %w", state.NodeId, err)
		}
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("storage_probation_slashed",
		sdk.NewAttribute("node_id", state.NodeId),
		sdk.NewAttribute("amount", burned.String()),
	))
	return nil
}

// trimReserved evicts replicas from a node whose reserved bytes exceed its declared capacity, most
// recently assigned first, until they fit. A slash lowers the capacity the bond backs and
// x/nodes clamps the declaration down with it; the replicas the smaller declaration cannot hold
// are released here so reserved never exceeds declared.
func (k Keeper) trimReserved(ctx sdk.Context, nodeID string) error {
	declared, err := k.nodes.DeclaredCapacity(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("failed to read declared capacity of %s: %w", nodeID, err)
	}
	reserved, err := k.reservedOf(ctx, nodeID)
	if err != nil {
		return err
	}
	if reserved <= declared {
		return nil
	}
	var refs []types.SlotRef
	rng := collections.NewPrefixedPairRange[string, uint64](nodeID).Descending()
	if err := k.ReplicaAt.Walk(ctx, rng, func(_ collections.Pair[string, uint64], ref types.SlotRef) (bool, error) {
		refs = append(refs, ref)
		return false, nil
	}); err != nil {
		return fmt.Errorf("failed to list replicas of %s: %w", nodeID, err)
	}
	for _, ref := range refs {
		if reserved <= declared {
			return nil
		}
		slot, err := k.loadSlot(ctx, ref.DealId, ref.Slot)
		if err != nil {
			return err
		}
		deal, err := k.loadDeal(ctx, ref.DealId)
		if err != nil {
			return err
		}
		if err := k.evictSlot(ctx, deal, slot); err != nil {
			return err
		}
		if reserved, err = k.reservedOf(ctx, nodeID); err != nil {
			return err
		}
	}
	if reserved > declared {
		return rejectf("node %s still reserves %d bytes above its declared %d after every replica was released", nodeID, reserved, declared)
	}
	return nil
}

func (k Keeper) payItem(ctx sdk.Context, deal *types.Deal, item types.Settlement) (paid math.Int, err error) {
	if item.Operator == "" {
		return math.ZeroInt(), nil
	}
	paid = math.ZeroInt()
	operator, err := parseAddr(item.Operator)
	if err != nil {
		return math.ZeroInt(), err
	}
	if item.EscrowPay.IsPositive() {
		if deal.Escrow.LT(item.EscrowPay) {
			return math.ZeroInt(), rejectf("deal %d escrow %s cannot cover %s", deal.Id, deal.Escrow, item.EscrowPay)
		}
		deal.Escrow = deal.Escrow.Sub(item.EscrowPay)
		got, err := k.payService(ctx, types.EscrowModuleName, operator, item.EscrowPay)
		if err != nil {
			return math.ZeroInt(), err
		}
		paid = paid.Add(got)
	}
	if item.MintPay.IsPositive() {
		// Minted when the epoch closed (reserveMints); paid from that reserve.
		got, err := k.payService(ctx, types.ModuleName, operator, item.MintPay)
		if err != nil {
			return math.ZeroInt(), err
		}
		paid = paid.Add(got)
		cur, err := k.epochMinted(ctx, item.Epoch)
		if err != nil {
			return math.ZeroInt(), err
		}
		if err := k.EpochMinted.Set(ctx, item.Epoch, cur.Add(item.MintPay)); err != nil {
			return math.ZeroInt(), err
		}
		if item.Subsidy {
			already, err := k.operatorMinted(ctx, item.Epoch, item.Operator)
			if err != nil {
				return math.ZeroInt(), err
			}
			if err := k.OperatorMinted.Set(ctx, collections.Join(item.Epoch, item.Operator), already.Add(item.MintPay)); err != nil {
				return math.ZeroInt(), err
			}
		}
	}
	if item.ArchiveTopUp.IsPositive() {
		fund, err := k.ArchiveFund.Get(ctx)
		if err != nil {
			return math.ZeroInt(), err
		}
		top := item.ArchiveTopUp
		if top.GT(fund) {
			top = fund
		}
		if top.IsPositive() {
			if err := k.bank.SendCoinsFromModuleToModule(ctx, types.ArchiveModuleName, types.ModuleName, coins(top)); err != nil {
				return math.ZeroInt(), fmt.Errorf("failed to draw archive top-up: %w", err)
			}
			if err := k.ArchiveFund.Set(ctx, fund.Sub(top)); err != nil {
				return math.ZeroInt(), err
			}
			got, err := k.payService(ctx, types.ModuleName, operator, top)
			if err != nil {
				return math.ZeroInt(), err
			}
			paid = paid.Add(got)
		}
	}
	return paid, nil
}

// payService pays amount from source through the C2 service split and returns what reached the
// operator's earnings.
func (k Keeper) payService(ctx sdk.Context, source string, operator sdk.AccAddress, amount math.Int) (math.Int, error) {
	bal := k.bank.GetBalance(ctx, authtypes.NewModuleAddress(source), params.BaseDenom).Amount
	if bal.LT(amount) {
		return math.ZeroInt(), rejectf("module %s holds %s, need %s to pay %s", source, bal, amount, operator)
	}
	toProvider, burn, archive := types.SplitServicePayment(amount)
	if toProvider.IsPositive() {
		if err := k.earnings.CreditEarnings(ctx, source, operator, coin(toProvider)); err != nil {
			return math.ZeroInt(), fmt.Errorf("failed to credit earnings: %w", err)
		}
	}
	if burn.IsPositive() {
		if err := k.bank.BurnCoins(ctx, source, coins(burn)); err != nil {
			return math.ZeroInt(), fmt.Errorf("failed to burn service share: %w", err)
		}
	}
	if archive.IsPositive() {
		if err := k.bank.SendCoinsFromModuleToModule(ctx, source, types.ArchiveModuleName, coins(archive)); err != nil {
			return math.ZeroInt(), fmt.Errorf("failed to fund archive: %w", err)
		}
		fund, err := k.ArchiveFund.Get(ctx)
		if err != nil {
			return math.ZeroInt(), err
		}
		if err := k.ArchiveFund.Set(ctx, fund.Add(archive)); err != nil {
			return math.ZeroInt(), err
		}
	}
	return toProvider, nil
}

// noteService records that the node served. paid is what the settlement credited to the operator's
// earnings; a probation node's record deposit is taken from it (C7: "their record deposit is taken
// from their first earnings"). The deposit fills progressively, min(remaining, paid) per credit,
// so a node whose first payouts are smaller than probation_deposit is never asked for money it has
// not earned yet.
func (k Keeper) noteService(ctx sdk.Context, p types.Params, item types.Settlement, paid math.Int) error {
	if item.NodeId == "" {
		return nil
	}
	state, err := k.Nodes.Get(ctx, item.NodeId)
	if err != nil {
		return err
	}
	state.EverProved = true
	if paid.IsPositive() && state.Probation && !state.Graduated {
		var opened bool
		failed, err := k.isolate(ctx, FailureKindDeposit, item.NodeId, func(c sdk.Context) error {
			var derr error
			opened, derr = k.fundProbationDeposit(c, p, item.NodeId, item.Operator, paid)
			return derr
		})
		if err != nil {
			return err
		}
		if !failed && opened {
			state.DepositLocked = true
		}
	}
	return k.Nodes.Set(ctx, item.NodeId, state)
}

// fundProbationDeposit adds min(probation_deposit - held, paid) to the node's record deposit,
// opening the deposit on the first credit. It reports whether it opened the deposit. A failure
// here never blocks the payout that triggered it: the caller rolls this back alone and the deposit
// is topped up from the next credit.
func (k Keeper) fundProbationDeposit(ctx sdk.Context, p types.Params, nodeID, operator string, paid math.Int) (opened bool, err error) {
	id := probationDepositID(nodeID)
	held, found, err := k.deposits.DepositAmount(ctx, id)
	if err != nil {
		return false, fmt.Errorf("failed to read probation deposit of %s: %w", nodeID, err)
	}
	if !found {
		held = math.ZeroInt()
	}
	take := math.MinInt(p.ProbationDeposit.Sub(held), paid)
	if !take.IsPositive() {
		return false, nil
	}
	if found {
		if err := k.deposits.TopUpDeposit(ctx, id, take); err != nil {
			return false, fmt.Errorf("failed to top up probation deposit of %s: %w", nodeID, err)
		}
		return false, nil
	}
	op, err := parseAddr(operator)
	if err != nil {
		return false, err
	}
	if err := k.deposits.LockDeposit(ctx, op, id, take); err != nil {
		return false, fmt.Errorf("failed to lock probation deposit for %s: %w", nodeID, err)
	}
	return true, nil
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

// countActiveOperators counts the distinct operators of active tracked nodes. A node whose
// x/nodes record cannot be read is left out of the count and reported through the failure
// counters; it does not fail the epoch close that asks for the count.
func (k Keeper) countActiveOperators(ctx sdk.Context) (uint64, error) {
	var ids []string
	if err := k.Nodes.Walk(ctx, nil, func(id string, _ types.NodeState) (bool, error) {
		ids = append(ids, id)
		return false, nil
	}); err != nil {
		return 0, fmt.Errorf("failed to count active operators: %w", err)
	}
	seen := map[string]struct{}{}
	for _, id := range ids {
		var op string
		var active bool
		failed, err := k.isolate(ctx, FailureKindOperators, id, func(c sdk.Context) error {
			var rerr error
			if active, rerr = k.nodes.IsActive(c, id); rerr != nil || !active {
				return rerr
			}
			op, rerr = k.nodes.Operator(c, id)
			return rerr
		})
		if err != nil {
			return 0, err
		}
		if !failed && active {
			seen[op] = struct{}{}
		}
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
		if _, err := k.isolate(ctx, FailureKindDeal, dealSubject(id), func(c sdk.Context) error {
			return k.expireDeal(c, id)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) expireDeal(ctx sdk.Context, id uint64) error {
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
	return k.Pending.Remove(ctx, id)
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
		if _, err := k.isolate(ctx, FailureKindProbation, state.NodeId, func(c sdk.Context) error {
			if !state.EverProved {
				if err := k.nodes.Jail(c, state.NodeId); err != nil {
					return fmt.Errorf("failed to jail probation node %s: %w", state.NodeId, err)
				}
			}
			return k.endProbation(c, state, state.EverProved)
		}); err != nil {
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
	// The epoch counts as scheduled even when a deal could not be created: retrying it in every
	// later block would repeat the same failure, and the failure is reported per class.
	for _, class := range []types.DealClass{types.DealClass_DEAL_CLASS_ARCHIVE, types.DealClass_DEAL_CLASS_PUBLIC_PIN} {
		subject := fmt.Sprintf("protocol/%s", class.String())
		if _, err := k.isolate(ctx, FailureKindDeal, subject, func(c sdk.Context) error {
			payload := protocolPayload(epoch, class, p.ProtocolPieceBytes)
			_, err := k.CreateProtocolDeal(c, class, payload, p.ProtocolPricePerEpoch, p.ProtocolDurationEpochs)
			return err
		}); err != nil {
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
