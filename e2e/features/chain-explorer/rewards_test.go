//go:build e2e_fleet

package chainexplorer

import (
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

type epochRewardsRow struct {
	Operator      string `json:"operator"`
	Amount        string `json:"amount"`
	Commission    string `json:"commission"`
	OperatorShare string `json:"operator_share"`
	Delegators    string `json:"delegators"`
	ForceBonded   string `json:"force_bonded"`
}

func sum(t *testing.T, parts ...string) *big.Int {
	t.Helper()
	total := new(big.Int)
	for _, p := range parts {
		n, ok := new(big.Int).SetString(p, 10)
		if !ok || n.Sign() < 0 {
			t.Fatalf("%q is not a non-negative integer", p)
		}
		total.Add(total, n)
	}
	return total
}

// TestExplorerRewards_aRecordedEpochSaysWhoWasPaidAndItAddsUp: for the newest closed epoch whose
// payout is recorded, the epoch row, its per-validator rewards, the validator's own reward history
// and the account routes agree. The validators' amounts add up to what the chain's bank transfers
// moved (credited plus force-bonded); each reward splits into the operator's share and the
// delegators'; the commission is part of the operator's share; and the run's validator is paid.
// An epoch that is not recorded says so and lists no reward. A run that has not closed an epoch has
// empty lists, not errors.
func TestExplorerRewards_aRecordedEpochSaysWhoWasPaidAndItAddsUp(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	c := chain.New(t)
	valoper := c.Valoper(t, c.Validator(t, c.Node(t, 0)))
	account := c.Validator(t, c.Node(t, 0)).Address

	var epochs struct {
		Epochs []struct {
			Epoch                uint64 `json:"epoch"`
			Unavailable          bool   `json:"unavailable"`
			RewardsRecorded      bool   `json:"rewards_recorded"`
			RewardsCredited      string `json:"rewards_credited"`
			RewardsForceBonded   string `json:"rewards_force_bonded"`
			RewardsCommission    string `json:"rewards_commission"`
			RewardsOperatorShare string `json:"rewards_operator_share"`
			RewardsDelegators    string `json:"rewards_delegators"`
		} `json:"epochs"`
	}
	decode(t, read(t, "/index/epochs", url.Values{"limit": {fmt.Sprint(maxIndexLimit)}}), &epochs)

	recorded := -1
	for i, e := range epochs.Epochs {
		if e.RewardsRecorded {
			recorded = i
			break
		}
	}
	if len(epochs.Epochs) == 0 || recorded < 0 {
		t.Logf("no closed epoch with a recorded payout yet (%d closed): only the routes' shapes are checked", len(epochs.Epochs))
		checkRewardRoutesWhenEmpty(t, valoper, account)
		return
	}
	e := epochs.Epochs[recorded]
	if e.RewardsCommission == "" || e.RewardsOperatorShare == "" || e.RewardsDelegators == "" {
		t.Fatalf("epoch %d is recorded and its split is incomplete: %+v", e.Epoch, e)
	}
	if !e.Unavailable && sum(t, e.RewardsOperatorShare, e.RewardsDelegators).Cmp(sum(t, e.RewardsCredited, e.RewardsForceBonded)) != 0 {
		t.Errorf("epoch %d: operators %s + delegators %s is not credited %s + force-bonded %s", e.Epoch, e.RewardsOperatorShare, e.RewardsDelegators, e.RewardsCredited, e.RewardsForceBonded)
	}

	var rewards struct {
		Epoch    uint64            `json:"epoch"`
		Recorded bool              `json:"recorded"`
		Rewards  []epochRewardsRow `json:"rewards"`
	}
	decode(t, read(t, fmt.Sprintf("/index/epochs/%d/rewards", e.Epoch), url.Values{"limit": {fmt.Sprint(maxIndexLimit)}}), &rewards)
	if rewards.Epoch != e.Epoch || !rewards.Recorded || len(rewards.Rewards) == 0 {
		t.Fatalf("epoch %d rewards answer %+v", e.Epoch, rewards)
	}
	var paid, commission *big.Int = new(big.Int), new(big.Int)
	var own *epochRewardsRow
	for i, r := range rewards.Rewards {
		if sum(t, r.OperatorShare, r.Delegators).Cmp(sum(t, r.Amount)) != 0 || sum(t, r.Commission).Cmp(sum(t, r.OperatorShare)) > 0 || sum(t, r.ForceBonded).Cmp(sum(t, r.OperatorShare)) > 0 {
			t.Errorf("reward %+v does not split: operator share + delegators must be the amount, commission and force-bonded within the operator share", r)
		}
		paid.Add(paid, sum(t, r.Amount))
		commission.Add(commission, sum(t, r.Commission))
		if r.Operator == valoper {
			own = &rewards.Rewards[i]
		}
	}
	if len(rewards.Rewards) < maxIndexLimit {
		if paid.Cmp(sum(t, e.RewardsOperatorShare, e.RewardsDelegators)) != 0 || commission.Cmp(sum(t, e.RewardsCommission)) != 0 {
			t.Errorf("epoch %d: the validators' rewards add up to %s paid and %s commission, the row says %s and %s",
				e.Epoch, paid, commission, sum(t, e.RewardsOperatorShare, e.RewardsDelegators), e.RewardsCommission)
		}
	}
	if own == nil {
		return
	}

	var history struct {
		Rewards []struct {
			Epoch    uint64           `json:"epoch"`
			Recorded bool             `json:"recorded"`
			Reward   *epochRewardsRow `json:"reward"`
		} `json:"rewards"`
	}
	decode(t, read(t, "/index/validators/"+valoper+"/rewards", url.Values{"limit": {fmt.Sprint(maxIndexLimit)}}), &history)
	matched := false
	for _, h := range history.Rewards {
		if h.Epoch == e.Epoch {
			matched = h.Recorded && h.Reward != nil && *h.Reward == *own
		}
		if !h.Recorded && h.Reward != nil {
			t.Errorf("epoch %d is not recorded and carries a reward", h.Epoch)
		}
	}
	if !matched {
		t.Errorf("validator %s: epoch %d is not in its reward history as the epoch's own row %+v", valoper, e.Epoch, own)
	}

	var credits struct {
		From    uint64 `json:"recorded_from_epoch"`
		Rewards []struct {
			Epoch     uint64 `json:"epoch"`
			Validator string `json:"validator"`
			Role      string `json:"role"`
			Amount    string `json:"amount"`
		} `json:"rewards"`
	}
	decode(t, read(t, "/index/accounts/"+account+"/rewards", url.Values{"limit": {fmt.Sprint(maxIndexLimit)}}), &credits)
	found := false
	for _, r := range credits.Rewards {
		if r.Epoch == e.Epoch && r.Validator == valoper && r.Role == "operator" {
			found = r.Amount == own.OperatorShare
		}
	}
	if credits.From == 0 || credits.From > e.Epoch || (own.OperatorShare != "0" && !found) {
		t.Errorf("account %s: recorded from epoch %d, operator credit for epoch %d found %v, want its operator share %s", account, credits.From, e.Epoch, found, own.OperatorShare)
	}
}

// checkRewardRoutesWhenEmpty: before an epoch is recorded the routes still answer, with empty lists.
func checkRewardRoutesWhenEmpty(t *testing.T, valoper, account string) {
	t.Helper()
	var credits struct {
		From    uint64 `json:"recorded_from_epoch"`
		Rewards []any  `json:"rewards"`
	}
	decode(t, read(t, "/index/accounts/"+account+"/rewards", nil), &credits)
	if credits.From != 0 || len(credits.Rewards) != 0 {
		t.Errorf("account %s: recorded from epoch %d with %d credits before any epoch is recorded", account, credits.From, len(credits.Rewards))
	}
	if resp := read(t, "/index/validators/"+valoper+"/rewards", nil); resp.Status != http.StatusOK {
		t.Errorf("validator %s rewards: HTTP %d: %.200s", valoper, resp.Status, resp.Body)
	}
}

// TestExplorerRewards_refusals: the reward routes keep the index's bounds, an epoch that is not
// closed is a 404, and an account that was credited nothing is an empty list, not an error.
func TestExplorerRewards_refusals(t *testing.T) {
	t.Parallel()
	requireIndexer(t)
	c := chain.New(t)
	valoper := c.Valoper(t, c.Validator(t, c.Node(t, 0)))
	account := c.Validator(t, c.Node(t, 0)).Address
	stranger := c.NewKey(t, c.Node(t, 1), "e2e-explorer-rewards-stranger").Address

	for _, tc := range []struct {
		path  string
		query url.Values
		want  int
	}{
		{fmt.Sprintf("/index/epochs/%d/rewards", unknownEpoch), nil, http.StatusNotFound},
		{"/index/epochs/0/rewards", nil, http.StatusNotFound},
		{"/index/epochs/1/rewards", url.Values{"limit": {fmt.Sprint(maxIndexLimit + 1)}}, http.StatusBadRequest},
		{"/index/validators/" + valoper + "/rewards", url.Values{"page": {"0"}}, http.StatusBadRequest},
		{"/index/validators/" + account + "/rewards", nil, http.StatusNotFound},
		{"/index/accounts/" + account + "/rewards", url.Values{"x": {"1"}}, http.StatusBadRequest},
		{"/index/accounts/notanaddress/rewards", nil, http.StatusNotFound},
		{"/index/accounts/" + stranger + "/rewards", nil, http.StatusOK},
	} {
		if resp := read(t, tc.path, tc.query); resp.Status != tc.want {
			t.Errorf("GET %s %v: HTTP %d, want %d: %.200s", tc.path, tc.query, resp.Status, tc.want, resp.Body)
		}
	}
	var none struct {
		Rewards []any `json:"rewards"`
	}
	decode(t, read(t, "/index/accounts/"+stranger+"/rewards", nil), &none)
	if len(none.Rewards) != 0 {
		t.Errorf("an unfunded key was credited %d rewards", len(none.Rewards))
	}
}
