package keeper

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	chainparams "github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// SettleEpoch settles epoch. The app calls this once the epoch has closed;
// this module does not read the clock itself. The first epoch with at least
// min_reporters_quorum complete reports activates rewards and mints nothing.
// Later epochs with quorum mint. Quorum failure mints nothing and does not
// deactivate rewards. A repeat call returns the stored result and does not
// mint again.
//
// Each operator's settled total is paid through the C2 90/5/5 service split
// (payOperator): 90% to its earnings account, 5% burned, 5% to the archive fund.
// The payout rows and the epoch's minted amount stay gross.
func (k Keeper) SettleEpoch(ctx sdk.Context, epoch uint64) (types.EpochResult, error) {
	if epoch == 0 {
		return types.EpochResult{}, fmt.Errorf("settle epoch: epoch must be positive")
	}
	existing, err := k.EpochResults.Get(ctx, epoch)
	if err == nil {
		// Reports left behind for a settled epoch (an imported genesis can carry
		// them) are dropped here so they cannot hold back the epochs after it.
		if err := k.finishSettle(ctx, existing); err != nil {
			return types.EpochResult{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return types.EpochResult{}, fmt.Errorf("settle epoch: failed to load epoch %d: %w", epoch, err)
	}

	reports, err := k.reportsForEpoch(ctx, epoch)
	if err != nil {
		return types.EpochResult{}, err
	}
	reporters, err := k.reporterSet(ctx)
	if err != nil {
		return types.EpochResult{}, err
	}
	var counted []types.CompleteReport
	for _, report := range reports {
		if _, ok := reporters[report.Reporter]; ok {
			counted = append(counted, report)
		}
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.EpochResult{}, fmt.Errorf("settle epoch: failed to load params: %w", err)
	}
	activation, err := k.Activation.Get(ctx)
	if err != nil {
		return types.EpochResult{}, fmt.Errorf("settle epoch: failed to load activation: %w", err)
	}

	result := types.EpochResult{
		Epoch:     epoch,
		QuorumMet: uint32(len(counted)) >= params.MinReportersQuorum,
		Ceiling:   math.ZeroInt(),
		Minted:    math.ZeroInt(),
	}
	if !result.QuorumMet {
		if err := k.finishSettle(ctx, result); err != nil {
			return types.EpochResult{}, err
		}
		return result, nil
	}
	if !activation.Active {
		activation.Active = true
		activation.ActivationEpoch = epoch
		if err := k.Activation.Set(ctx, activation); err != nil {
			return types.EpochResult{}, fmt.Errorf("settle epoch: failed to activate rewards: %w", err)
		}
		result.Activating = true
		if err := k.finishSettle(ctx, result); err != nil {
			return types.EpochResult{}, err
		}
		k.Logger(ctx).Info("relay rewards activated", "epoch", epoch, "reporters", len(counted))
		return result, nil
	}
	if epoch <= activation.ActivationEpoch {
		if err := k.finishSettle(ctx, result); err != nil {
			return types.EpochResult{}, err
		}
		return result, nil
	}

	payouts, minted, ceiling, err := k.payEpoch(ctx, params, epoch, counted)
	if err != nil {
		return types.EpochResult{}, err
	}
	result.Ceiling = ceiling
	result.Minted = minted
	if err := k.storePayouts(ctx, payouts); err != nil {
		return types.EpochResult{}, err
	}
	if err := k.finishSettle(ctx, result); err != nil {
		return types.EpochResult{}, err
	}
	k.Logger(ctx).Info("relay epoch settled", "epoch", epoch, "minted", minted.String(), "ceiling", ceiling.String())
	return result, nil
}

type claim struct {
	fingerprint []byte
	operator    string
	prefix16    string
	amount      math.Int
}

func (k Keeper) payEpoch(ctx sdk.Context, params types.Params, epoch uint64, reports []types.CompleteReport) ([]types.RelayPayout, math.Int, math.Int, error) {
	ceiling, err := k.emission.RelayCeiling(ctx, epoch)
	if err != nil {
		return nil, math.Int{}, math.Int{}, fmt.Errorf("settle epoch: failed to read relay ceiling for epoch %d: %w", epoch, err)
	}
	if ceiling.IsNil() || ceiling.IsNegative() {
		return nil, math.Int{}, math.Int{}, fmt.Errorf("settle epoch: relay ceiling for epoch %d is invalid", epoch)
	}

	claims, err := k.scoreRelays(ctx, params, reports)
	if err != nil {
		return nil, math.Int{}, math.Int{}, err
	}
	if err := applyGroupCap(claims, params.PerOperatorCap, func(c claim) string { return c.operator }); err != nil {
		return nil, math.Int{}, math.Int{}, fmt.Errorf("settle epoch: operator cap: %w", err)
	}
	if err := applyGroupCap(claims, params.PerPrefix16Cap, func(c claim) string { return c.prefix16 }); err != nil {
		return nil, math.Int{}, math.Int{}, fmt.Errorf("settle epoch: prefix /16 cap: %w", err)
	}
	amounts := make([]math.Int, len(claims))
	for i, c := range claims {
		amounts[i] = c.amount
	}
	scaled, err := scaleToCap(amounts, ceiling)
	if err != nil {
		return nil, math.Int{}, math.Int{}, fmt.Errorf("settle epoch: ceiling: %w", err)
	}

	minted := math.ZeroInt()
	payouts := make([]types.RelayPayout, 0, len(claims))
	operatorTotals := map[string]math.Int{}
	var operators []string
	for i, c := range claims {
		if !scaled[i].IsPositive() {
			continue
		}
		payouts = append(payouts, types.RelayPayout{
			Epoch:          epoch,
			RsaFingerprint: append([]byte(nil), c.fingerprint...),
			Operator:       c.operator,
			Amount:         scaled[i],
		})
		minted = minted.Add(scaled[i])
		if _, ok := operatorTotals[c.operator]; !ok {
			operators = append(operators, c.operator)
			operatorTotals[c.operator] = math.ZeroInt()
		}
		operatorTotals[c.operator] = operatorTotals[c.operator].Add(scaled[i])
	}
	if minted.GT(ceiling) {
		return nil, math.Int{}, math.Int{}, fmt.Errorf("settle epoch: payout %s exceeds ceiling %s", minted, ceiling)
	}
	if !minted.IsPositive() {
		return nil, math.ZeroInt(), ceiling, nil
	}
	if err := k.emission.MintRelayReward(ctx, epoch, minted); err != nil {
		return nil, math.Int{}, math.Int{}, fmt.Errorf("settle epoch: failed to mint relay reward for epoch %d: %w", epoch, err)
	}
	sort.Strings(operators)
	for _, operator := range operators {
		addr, err := sdk.AccAddressFromBech32(operator)
		if err != nil {
			return nil, math.Int{}, math.Int{}, fmt.Errorf("settle epoch: operator %q: %w", operator, err)
		}
		if err := k.payOperator(ctx, addr, operatorTotals[operator]); err != nil {
			return nil, math.Int{}, math.Int{}, fmt.Errorf("settle epoch: %w", err)
		}
	}
	return payouts, minted, ceiling, nil
}

// payOperator pays one operator's settled total through the C2 service split: the operator's part
// goes to its earnings account, the burn share is burned and the archive share funds the archive.
// The operator receives the rounding remainder, so the three parts sum to total exactly.
func (k Keeper) payOperator(ctx sdk.Context, operator sdk.AccAddress, total math.Int) error {
	toOperator, burn, archive := k.service.SplitServicePayment(total)
	if !toOperator.Add(burn).Add(archive).Equal(total) {
		return fmt.Errorf("service split of %s summed to %s", total, toOperator.Add(burn).Add(archive))
	}
	if toOperator.IsPositive() {
		coin := sdk.NewCoin(chainparams.BaseDenom, toOperator)
		if err := k.earnings.CreditEarnings(ctx, types.ModuleName, operator, coin); err != nil {
			return fmt.Errorf("failed to credit %s to %s: %w", coin, operator, err)
		}
	}
	if burn.IsPositive() {
		if err := k.service.BurnService(ctx, types.ModuleName, burn); err != nil {
			return fmt.Errorf("failed to burn the service share of %s: %w", operator, err)
		}
	}
	if archive.IsPositive() {
		if err := k.service.FundArchive(ctx, types.ModuleName, archive); err != nil {
			return fmt.Errorf("failed to fund the archive from the payment to %s: %w", operator, err)
		}
	}
	return nil
}

func (k Keeper) scoreRelays(ctx sdk.Context, params types.Params, reports []types.CompleteReport) ([]claim, error) {
	type bucket struct {
		weights []math.Int
		uptimes []math.LegacyDec
		flags   []uint32
	}
	type scored struct {
		bucket
		ids [][]byte
	}
	byFP := map[string]*scored{}
	var order []string
	for _, report := range reports {
		for _, entry := range report.Entries {
			fp := string(entry.RsaFingerprint)
			b, ok := byFP[fp]
			if !ok {
				b = &scored{}
				byFP[fp] = b
				order = append(order, fp)
			}
			b.weights = append(b.weights, entry.ConsensusWeight)
			b.uptimes = append(b.uptimes, entry.UptimeFraction)
			b.flags = append(b.flags, entry.Flags)
			b.ids = append(b.ids, entry.Ed25519Id)
		}
	}
	sort.Strings(order)

	claims := make([]claim, 0, len(order))
	quorum := int(params.MinReportersQuorum)
	for _, fp := range order {
		b := byFP[fp]
		if len(b.weights) < quorum {
			continue
		}
		relay, found, err := k.getRelay(ctx, []byte(fp))
		if err != nil {
			return nil, err
		}
		if !found || relay.Jailed || !identitiesMatch(relay, b.ids) {
			continue
		}
		live, err := k.nodes.NodeLive(ctx, relay.NodeId)
		if err != nil {
			return nil, fmt.Errorf("settle epoch: failed to check node %s of relay: %w", relay.NodeId, err)
		}
		if !live {
			continue
		}
		if medianDec(b.uptimes).LT(params.MinUptimeFraction) {
			continue
		}
		amount := medianInt(b.weights)
		if amount.GT(params.PerRelayCap) {
			amount = params.PerRelayCap
		}
		if medianExit(b.flags) {
			amount = math.LegacyNewDecFromInt(amount).Mul(params.ExitMultiplier).TruncateInt()
		}
		if !amount.IsPositive() {
			continue
		}
		claims = append(claims, claim{
			fingerprint: append([]byte(nil), relay.RsaFingerprint...),
			operator:    relay.Operator,
			prefix16:    relay.Prefix16,
			amount:      amount,
		})
	}
	return claims, nil
}

// identitiesMatch reports whether every observation carries the ed25519 id
// registered for the relay. A mismatch is not paid and is not a settlement
// error: ingest already refused it, and a later key change must not abort
// the epoch.
func identitiesMatch(relay types.Relay, ids [][]byte) bool {
	for _, id := range ids {
		if !bytes.Equal(relay.Ed25519Id, id) {
			return false
		}
	}
	return true
}

func applyGroupCap(claims []claim, cap math.Int, group func(claim) string) error {
	buckets := map[string][]int{}
	var keys []string
	for i, c := range claims {
		key := group(c)
		if _, ok := buckets[key]; !ok {
			keys = append(keys, key)
		}
		buckets[key] = append(buckets[key], i)
	}
	sort.Strings(keys)
	for _, key := range keys {
		indexes := buckets[key]
		amounts := make([]math.Int, len(indexes))
		for j, idx := range indexes {
			amounts[j] = claims[idx].amount
		}
		scaled, err := scaleToCap(amounts, cap)
		if err != nil {
			return err
		}
		for j, idx := range indexes {
			claims[idx].amount = scaled[j]
		}
	}
	return nil
}

func (k Keeper) reportsForEpoch(ctx sdk.Context, epoch uint64) ([]types.CompleteReport, error) {
	var reports []types.CompleteReport
	err := k.Reports.Walk(ctx, collections.NewPrefixedPairRange[uint64, string](epoch), func(_ reportMapKey, report types.CompleteReport) (bool, error) {
		reports = append(reports, report)
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("settle epoch: failed to walk reports for epoch %d: %w", epoch, err)
	}
	return reports, nil
}

func (k Keeper) storePayouts(ctx sdk.Context, payouts []types.RelayPayout) error {
	for _, payout := range payouts {
		if err := k.Payouts.Set(ctx, payoutKey(payout.Epoch, payout.RsaFingerprint), payout); err != nil {
			return fmt.Errorf("settle epoch: failed to store payout: %w", err)
		}
	}
	return nil
}

func (k Keeper) finishSettle(ctx sdk.Context, result types.EpochResult) error {
	if err := k.EpochResults.Set(ctx, result.Epoch, result); err != nil {
		return fmt.Errorf("settle epoch: failed to store epoch %d: %w", result.Epoch, err)
	}
	reports, err := k.reportsForEpoch(ctx, result.Epoch)
	if err != nil {
		return err
	}
	for _, report := range reports {
		if err := k.Reports.Remove(ctx, reportKey(report.Epoch, report.Reporter)); err != nil {
			return fmt.Errorf("settle epoch: failed to delete report: %w", err)
		}
	}
	var chunkKeys []chunkMapKey
	err = k.Chunks.Walk(ctx, collections.NewPrefixedTripleRange[uint64, string, uint32](result.Epoch), func(key chunkMapKey, _ types.ReportChunk) (bool, error) {
		chunkKeys = append(chunkKeys, key)
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("settle epoch: failed to walk chunks for epoch %d: %w", result.Epoch, err)
	}
	for _, key := range chunkKeys {
		if err := k.Chunks.Remove(ctx, key); err != nil {
			return fmt.Errorf("settle epoch: failed to delete chunk: %w", err)
		}
	}
	return nil
}
