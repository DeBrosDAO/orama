package indexer

import (
	"fmt"

	cosmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/DeBrosOfficial/network/chain/app/params"
	powertypes "github.com/DeBrosOfficial/network/chain/x/power/types"
)

// Roles of an account in a reward.
const (
	RoleDelegator = "delegator"
	RoleOperator  = "operator"
)

// ValidatorReward is what x/power paid one validator's share in one closed epoch, from the
// power_validator_reward event the closing block carried. OperatorShare and Delegators add up to
// Amount; Commission is the part of OperatorShare that is the validator's commission rate (the rest
// is the operator's own pro-rata cut and flooring dust); ForceBonded is the part of OperatorShare
// that was bonded into a bootstrap committee member's self-delegation instead of credited to
// earnings. Amounts are norama.
type ValidatorReward struct {
	Epoch         uint64 `json:"epoch"`
	Operator      string `json:"operator"`
	Amount        string `json:"amount"`
	Commission    string `json:"commission"`
	OperatorShare string `json:"operator_share"`
	Delegators    string `json:"delegators"`
	ForceBonded   string `json:"force_bonded"`
}

// AccountReward is what one account was credited from one validator in one closed epoch: a
// delegator's pro-rata credit (power_delegator_reward), or the operator's own share of the
// validator it runs (ForceBonded of it went to its self-delegation, not earnings).
type AccountReward struct {
	Epoch       uint64 `json:"epoch"`
	Validator   string `json:"validator"`
	Role        string `json:"role"`
	Amount      string `json:"amount"`
	ForceBonded string `json:"force_bonded"`
}

// epochRewards is what an epoch's closing block says x/power paid, and the totals the bank
// transfers of the same block moved (credited to earnings, force-bonded).
type epochRewards struct {
	validators []ValidatorReward
	accounts   map[string][]AccountReward
	credited   cosmath.Int
	bonded     cosmath.Int
}

// rewardTotals are the sums of an epoch's validator rewards.
type rewardTotals struct {
	commission, operatorShare, delegators cosmath.Int
}

// recorded is whether the epoch's payout is known from events: there is a reward event for each
// paid validator, or nothing was paid. An epoch whose closing block moved reward coins and carried
// no event closed before the chain reported them, and is not recorded.
func (rw epochRewards) recorded() bool {
	return len(rw.validators) > 0 || rw.credited.Add(rw.bonded).IsZero()
}

// apply sets the row's reward fields from the epoch's events.
func (rw epochRewards) apply(row *EpochRow) error {
	row.RewardsRecorded = rw.recorded()
	if !row.RewardsRecorded {
		return nil
	}
	t, err := rw.totals()
	if err != nil {
		return err
	}
	row.RewardsCommission, row.RewardsOperatorShare, row.RewardsDelegators = t.commission.String(), t.operatorShare.String(), t.delegators.String()
	return nil
}

func (rw epochRewards) totals() (rewardTotals, error) {
	t := rewardTotals{commission: cosmath.ZeroInt(), operatorShare: cosmath.ZeroInt(), delegators: cosmath.ZeroInt()}
	for _, v := range rw.validators {
		n, err := rewardInts(v)
		if err != nil {
			return rewardTotals{}, err
		}
		t.commission, t.operatorShare, t.delegators = t.commission.Add(n.commission), t.operatorShare.Add(n.share), t.delegators.Add(n.delegators)
	}
	return t, nil
}

// readRewards reads the reward events of the block that closes epoch and checks them against the
// bank transfers x/power made in the same block: the validators' amounts must add up to what moved,
// or the index would show a payout the chain did not make.
func readRewards(epoch uint64, events []abci.Event) (epochRewards, error) {
	credited, bonded, err := rewardFlows(events)
	if err != nil {
		return epochRewards{}, err
	}
	rw := epochRewards{accounts: map[string][]AccountReward{}, credited: credited, bonded: bonded}
	delegated := map[string]cosmath.Int{}
	for i, e := range events {
		var err error
		switch e.Type {
		case powertypes.EventTypeValidatorReward:
			err = rw.addValidator(epoch, e)
		case powertypes.EventTypeDelegatorReward:
			err = rw.addDelegator(epoch, e, delegated)
		}
		if err != nil {
			return epochRewards{}, fmt.Errorf("%s event %d of the block closing epoch %d: %w", e.Type, i, epoch, err)
		}
	}
	return rw, rw.check(epoch, delegated)
}

func (rw *epochRewards) addValidator(epoch uint64, e abci.Event) error {
	v := ValidatorReward{
		Epoch: epoch, Operator: attr(e, powertypes.AttributeKeyValidator), Amount: attr(e, powertypes.AttributeKeyAmount),
		Commission: attr(e, powertypes.AttributeKeyCommission), OperatorShare: attr(e, powertypes.AttributeKeyOperatorShare),
		Delegators: attr(e, powertypes.AttributeKeyDelegators), ForceBonded: attr(e, powertypes.AttributeKeyForceBonded),
	}
	if !IsValidatorAddress(v.Operator) {
		return fmt.Errorf("validator %q is not an operator address", v.Operator)
	}
	n, err := rewardInts(v)
	if err != nil {
		return err
	}
	if !n.share.Add(n.delegators).Equal(n.amount) || n.commission.GT(n.share) || n.bonded.GT(n.share) {
		return fmt.Errorf("the split of %s's reward does not add up (amount %s, commission %s, operator %s, delegators %s, force-bonded %s)",
			v.Operator, v.Amount, v.Commission, v.OperatorShare, v.Delegators, v.ForceBonded)
	}
	rw.validators = append(rw.validators, v)
	if !n.share.IsPositive() {
		return nil
	}
	acct, err := operatorAccount(v.Operator)
	if err != nil {
		return err
	}
	rw.accounts[acct] = append(rw.accounts[acct], AccountReward{Epoch: epoch, Validator: v.Operator, Role: RoleOperator, Amount: v.OperatorShare, ForceBonded: v.ForceBonded})
	return nil
}

func (rw *epochRewards) addDelegator(epoch uint64, e abci.Event, delegated map[string]cosmath.Int) error {
	validator, delegator, raw := attr(e, powertypes.AttributeKeyValidator), attr(e, powertypes.AttributeKeyDelegator), attr(e, powertypes.AttributeKeyAmount)
	if !IsValidatorAddress(validator) || !IsAccountAddress(delegator) {
		return fmt.Errorf("validator %q or delegator %q is not an address", validator, delegator)
	}
	n, ok := cosmath.NewIntFromString(raw)
	if !ok || !n.IsPositive() {
		return fmt.Errorf("amount %q is not a positive integer", raw)
	}
	sum, seen := delegated[validator]
	if !seen {
		sum = cosmath.ZeroInt()
	}
	delegated[validator] = sum.Add(n)
	rw.accounts[delegator] = append(rw.accounts[delegator], AccountReward{Epoch: epoch, Validator: validator, Role: RoleDelegator, Amount: raw, ForceBonded: "0"})
	return nil
}

// check holds the events against each other and against the transfers: each validator's delegator
// credits add up to its Delegators, and every validator's Amount adds up to what x/power credited
// and force-bonded. No event at all is not checked: the epoch is then not recorded.
func (rw epochRewards) check(epoch uint64, delegated map[string]cosmath.Int) error {
	if len(rw.validators) == 0 {
		if len(delegated) > 0 {
			return fmt.Errorf("epoch %d: delegators were credited and no validator was reported as paid", epoch)
		}
		return nil
	}
	paid := cosmath.ZeroInt()
	known := map[string]bool{}
	for _, v := range rw.validators {
		known[v.Operator] = true
		n, _ := rewardInts(v)
		got, ok := delegated[v.Operator]
		if !ok {
			got = cosmath.ZeroInt()
		}
		if !got.Equal(n.delegators) {
			return fmt.Errorf("epoch %d: the delegator credits of %s add up to %s but its reward says %s", epoch, v.Operator, got, n.delegators)
		}
		paid = paid.Add(n.amount)
	}
	for operator := range delegated {
		if !known[operator] {
			return fmt.Errorf("epoch %d: delegators were credited by %s, which was not reported as paid", epoch, operator)
		}
	}
	if moved := rw.credited.Add(rw.bonded); !paid.Equal(moved) {
		return fmt.Errorf("epoch %d: the validator rewards add up to %s but x/power moved %s (credited %s, force-bonded %s); this indexer's copy of the reward events is out of date",
			epoch, paid, moved, rw.credited, rw.bonded)
	}
	return nil
}

type rewardNumbers struct{ amount, commission, share, delegators, bonded cosmath.Int }

func rewardInts(v ValidatorReward) (rewardNumbers, error) {
	var n rewardNumbers
	for _, p := range []struct {
		into *cosmath.Int
		raw  string
	}{{&n.amount, v.Amount}, {&n.commission, v.Commission}, {&n.share, v.OperatorShare}, {&n.delegators, v.Delegators}, {&n.bonded, v.ForceBonded}} {
		x, ok := cosmath.NewIntFromString(p.raw)
		if !ok || x.IsNegative() {
			return rewardNumbers{}, fmt.Errorf("reward of %s holds %q, not a non-negative integer", v.Operator, p.raw)
		}
		*p.into = x
	}
	return n, nil
}

// operatorAccount is the account address of a validator's operator: the same bytes under the
// account prefix.
func operatorAccount(operator string) (string, error) {
	_, raw, err := bech32.DecodeAndConvert(operator)
	if err != nil {
		return "", fmt.Errorf("operator address %q: %w", operator, err)
	}
	acct, err := bech32.ConvertAndEncode(params.Bech32Prefix, raw)
	if err != nil {
		return "", fmt.Errorf("failed to encode the account of operator %s: %w", operator, err)
	}
	return acct, nil
}

// putRewards stores what the epoch paid: a row per validator, in the epoch's list and in the
// validator's history, and a row per credited account. It records the first epoch whose payout is
// known.
func (w *writer) putRewards(epoch uint64, rw epochRewards) error {
	if !rw.recorded() {
		return nil
	}
	if _, set, err := getUint(w.b, []byte(keyRewardsFrom)); err != nil {
		return err
	} else if !set {
		if err := w.set([]byte(keyRewardsFrom), be64(epoch)); err != nil {
			return err
		}
	}
	for _, v := range rw.validators {
		if err := w.setJSON(rewardEpochKey(epoch, v.Operator), v); err != nil {
			return err
		}
		if err := w.setJSON(rewardValKey(v.Operator, epoch), v); err != nil {
			return err
		}
	}
	for acct, rows := range rw.accounts {
		for _, a := range rows {
			if err := w.setJSON(rewardAcctKey(acct, epoch, a.Validator), a); err != nil {
				return err
			}
		}
	}
	return nil
}

func rewardEpochPrefix(epoch uint64) []byte { return join([]byte(pfxRewardEpoch), be64(epoch)) }
func rewardEpochKey(epoch uint64, operator string) []byte {
	return join(rewardEpochPrefix(epoch), []byte(operator))
}
func rewardValPrefix(operator string) []byte { return []byte(pfxRewardVal + operator + "/") }
func rewardValKey(operator string, epoch uint64) []byte {
	return join(rewardValPrefix(operator), be64(epoch))
}
func rewardAcctPrefix(account string) []byte { return []byte(pfxRewardAcct + account + "/") }
func rewardAcctKey(account string, epoch uint64, validator string) []byte {
	return join(rewardAcctPrefix(account), be64(epoch), []byte(validator))
}
