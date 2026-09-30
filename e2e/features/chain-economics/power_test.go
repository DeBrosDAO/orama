//go:build e2e_fleet

package chaineconomics

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// powerParams is orama.power.v1.Params.
type powerParams struct {
	Params struct {
		MinCommitteeSize          chain.Int `json:"min_committee_size"`
		BootstrapExitStake        chain.Int `json:"bootstrap_exit_stake"`
		BootstrapDeadlineEpochs   chain.Int `json:"bootstrap_deadline_epochs"`
		CapFractionNormal         chain.Dec `json:"cap_fraction_normal"`
		CapFractionReduced        chain.Dec `json:"cap_fraction_reduced"`
		CapStepDownValidatorCount chain.Int `json:"cap_step_down_validator_count"`
		CapStepUpValidatorCount   chain.Int `json:"cap_step_up_validator_count"`
		CapHysteresisEpochs       chain.Int `json:"cap_hysteresis_epochs"`
		RampEpochs                chain.Int `json:"ramp_epochs"`
		ForceBondFraction         chain.Dec `json:"force_bond_fraction"`
		SelfBondCapMultiplier     chain.Dec `json:"self_bond_cap_multiplier"`
		MinSelfBond               chain.Int `json:"min_self_bond"`
		CometPowerScale           chain.Int `json:"comet_power_scale"`
		PreGateLambdaCap          chain.Dec `json:"pre_gate_lambda_cap"`
		MinDelegationForRewards   chain.Int `json:"min_delegation_for_rewards"`
	} `json:"params"`
}

// TestPower_paramsAreTheDocumentedDefaults: x/power runs with the documented
// genesis defaults (docs/CHAIN.md "x/power"; x/power/types/params.go), with
// min_committee_size lowered to the run's committee (chain-deploy.sh
// --min-committee-size).
func TestPower_paramsAreTheDocumentedDefaults(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	var p powerParams
	c.Query(t, c.Node(t, 0), &p, "power", "params")
	q := p.Params
	ints := map[string][2]chain.Int{
		"min_committee_size":            {q.MinCommitteeSize, chain.NewInt(int64(len(c.Nodes())))},
		"bootstrap_exit_stake":          {q.BootstrapExitStake, chain.Orama(271_000)},
		"bootstrap_deadline_epochs":     {q.BootstrapDeadlineEpochs, chain.NewInt(365)},
		"cap_step_down_validator_count": {q.CapStepDownValidatorCount, chain.NewInt(60)},
		"cap_step_up_validator_count":   {q.CapStepUpValidatorCount, chain.NewInt(50)},
		"cap_hysteresis_epochs":         {q.CapHysteresisEpochs, chain.NewInt(30)},
		"ramp_epochs":                   {q.RampEpochs, chain.NewInt(30)},
		"min_self_bond":                 {q.MinSelfBond, chain.Orama(1000)},
		"comet_power_scale":             {q.CometPowerScale, chain.NewInt(1_000_000_000)},
		"min_delegation_for_rewards":    {q.MinDelegationForRewards, chain.Orama(1)},
	}
	for name, v := range ints {
		if v[0].Cmp(v[1]) != 0 {
			t.Errorf("%s = %s, want %s", name, v[0].String(), v[1].String())
		}
	}
	decs := map[string][2]float64{
		"cap_fraction_normal":      {q.CapFractionNormal.Float(), 0.05},
		"cap_fraction_reduced":     {q.CapFractionReduced.Float(), 0.03},
		"force_bond_fraction":      {q.ForceBondFraction.Float(), 0.5},
		"self_bond_cap_multiplier": {q.SelfBondCapMultiplier.Float(), 2},
		"pre_gate_lambda_cap":      {q.PreGateLambdaCap.Float(), 0.95},
	}
	for name, v := range decs {
		if v[0] != v[1] {
			t.Errorf("%s = %v, want %v", name, v[0], v[1])
		}
	}
}

// committee is `oramad query power bootstrap-committee`.
type committee struct {
	Members []struct {
		OperatorAddress string `json:"operator_address"`
		Moniker         string `json:"moniker"`
		ConsensusPubkey string `json:"consensus_pubkey"`
	} `json:"members"`
}

// TestPower_bootstrapCommitteeIsGenesis: the committee is exactly the run's
// validators as genesis declared them: each node's operator key, moniker the
// node name, consensus key the one its CometBFT signs with, and every node
// has CometBFT power equal to x/power's last assigned power
// (validator-power comet_power; its share fields are documented as always
// zero). A non-member has power 0; an account address is not a valoper.
func TestPower_bootstrapCommitteeIsGenesis(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n0 := c.Node(t, 0)
	var com committee
	c.Query(t, n0, &com, "power", "bootstrap-committee")
	if len(com.Members) != len(c.Nodes()) {
		t.Fatalf("committee has %d members, want %d", len(com.Members), len(c.Nodes()))
	}
	byAddr := map[string]int{}
	for i, m := range com.Members {
		byAddr[m.OperatorAddress] = i
	}
	for _, n := range c.Nodes() {
		k := c.Validator(t, n)
		i, ok := byAddr[k.Address]
		if !ok {
			t.Errorf("%s's operator %s is not in the committee", n.Name, k.Address)
			continue
		}
		m := com.Members[i]
		pub := consensusPubkey(t, c, n)
		if m.Moniker != n.Name || m.ConsensusPubkey != pub {
			t.Errorf("%s: member moniker %q key %s, want %q %s", n.Name, m.Moniker, m.ConsensusPubkey, n.Name, pub)
		}
		vp := requireCometPowerMatches(t, c, n0, n, c.Valoper(t, k))
		for name, d := range map[string]chain.Dec{"bootstrap_share": vp.BootstrapShare, "capped_share": vp.CappedShare, "power_share": vp.PowerShare} {
			if d.Float() != 0 {
				t.Errorf("%s: %s = %v, documented as always zero", n.Name, name, d.Float())
			}
		}
	}
	stranger := c.NewKey(t, n0, "e2e-stranger")
	if vp := validatorPower(t, c, n0, c.Valoper(t, stranger)); vp.CometPower.Int64() != 0 {
		t.Errorf("a key that is no validator has comet_power %d", vp.CometPower.Int64())
	}
	// An account address is not a valoper: ValAddressFromBech32 refuses its
	// prefix (x/power/keeper/grpc_query.go ValidatorPower, InvalidArgument).
	if out := c.QueryFails(t, n0, "power", "validator-power", stranger.Address); !strings.Contains(out, "InvalidArgument") ||
		!strings.Contains(out, "expected oramavaloper") {
		t.Errorf("validator-power of an account address: want InvalidArgument naming the oramavaloper prefix, got %s", out)
	}
}

// powerLagBudget bounds how long x/power's assigned power and CometBFT's
// voting power may disagree: an update from an epoch close reaches the
// CometBFT validator set blocks later (EndBlock at H applies at H+2).
const powerLagBudget = time.Minute

// requireCometPowerMatches waits until n's CometBFT voting power equals
// x/power's comet_power for valoper (read on reader), both positive, and
// returns the last validator-power read (Eventually reports a disagreement).
func requireCometPowerMatches(t *testing.T, c *chain.Chain, reader, n fleet.Node, valoper string) valPower {
	t.Helper()
	var vp valPower
	eventually.Eventually(t, chain.PollEvery, powerLagBudget, n.Name+" CometBFT power to equal x/power comet_power", func() (bool, error) {
		vp = validatorPower(t, c, reader, valoper)
		st, err := c.NodeStatus(t, n)
		if err != nil {
			return false, err
		}
		got := vp.CometPower.Int64()
		return got > 0 && got == st.VotingPow, fmt.Errorf("comet_power %d, CometBFT voting power %d", got, st.VotingPow)
	})
	return vp
}

type valPower struct {
	OperatorAddress string    `json:"operator_address"`
	BootstrapShare  chain.Dec `json:"bootstrap_share"`
	CappedShare     chain.Dec `json:"capped_share"`
	PowerShare      chain.Dec `json:"power_share"`
	CometPower      chain.Int `json:"comet_power"`
}

func validatorPower(t *testing.T, c *chain.Chain, n fleet.Node, valoper string) valPower {
	t.Helper()
	var vp valPower
	c.Query(t, n, &vp, "power", "validator-power", valoper)
	return vp
}

// consensusPubkey is the base64 ed25519 key CometBFT reports for n.
func consensusPubkey(t *testing.T, c *chain.Chain, n fleet.Node) string {
	t.Helper()
	var r struct {
		ValidatorInfo struct {
			PubKey struct {
				Value string `json:"value"`
			} `json:"pub_key"`
		} `json:"validator_info"`
	}
	if err := c.Comet(t, n, "/status", &r); err != nil {
		t.Fatal(err)
	}
	if raw, err := base64.StdEncoding.DecodeString(r.ValidatorInfo.PubKey.Value); err != nil || len(raw) != 32 {
		t.Fatalf("%s: consensus key %q is not a 32-byte ed25519 key", n.Name, r.ValidatorInfo.PubKey.Value)
	}
	return r.ValidatorInfo.PubKey.Value
}

// lambdaView is `oramad query power lambda`.
type lambdaView struct {
	Lambda           chain.Dec `json:"lambda"`
	LastUpdatedEpoch chain.Int `json:"last_updated_epoch"`
	CurrentCapBps    chain.Int `json:"current_cap_bps"`
}

// TestPower_lambdaIsMonotonicAndCapped: lambda never falls across epoch
// closes, stays within [0, 1], and while three validators are far below the
// hand-over gate it stays at or under pre_gate_lambda_cap (0.95); it is
// recomputed only at an epoch close (last_updated_epoch never ahead of the
// current epoch); the cap is the normal 5% (500 bps) with fewer than 60
// validators (docs/CHAIN.md "The power formula").
func TestPower_lambdaIsMonotonicAndCapped(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	var prev lambdaView
	c.Query(t, n, &prev, "power", "lambda")
	for i := 0; i < 2; i++ {
		start := c.Epoch(t, n, 0).CurrentEpoch
		waitEpochAfter(t, c, start)
		var cur lambdaView
		c.Query(t, n, &cur, "power", "lambda")
		checkLambda(t, c, prev, cur)
		prev = cur
	}
}

func checkLambda(t *testing.T, c *chain.Chain, prev, cur lambdaView) {
	t.Helper()
	epoch := c.Epoch(t, c.Node(t, 0), 0).CurrentEpoch
	switch l := cur.Lambda.Float(); {
	case l < prev.Lambda.Float():
		t.Errorf("lambda fell from %v to %v", prev.Lambda.Float(), l)
	case l < 0 || l > 0.95:
		t.Errorf("lambda %v outside [0, 0.95] before the hand-over gate", l)
	}
	if cur.LastUpdatedEpoch.Cmp(epoch) > 0 {
		t.Errorf("lambda updated at epoch %s, ahead of the current %s", cur.LastUpdatedEpoch.String(), epoch.String())
	}
	if cur.CurrentCapBps.Int64() != 500 {
		t.Errorf("current cap %d bps, want 500 (5%%) below 60 validators", cur.CurrentCapBps.Int64())
	}
}

// TestPower_rewardsReachEveryMemberEachEpoch: every committee member's
// earnings (or, while its self-bond is below 2x min_self_bond, its
// force-bonded self-delegation) grows across an epoch close: the 60%
// validator share is paid on capped power to all three seats.
func TestPower_rewardsReachEveryMemberEachEpoch(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	before := map[string]chain.Int{}
	for _, n := range c.Nodes() {
		k := c.Validator(t, n)
		before[n.Name] = c.Earnings(t, n, k.Address)
	}
	start := c.Epoch(t, c.Node(t, 0), 0).CurrentEpoch
	waitEpochAfter(t, c, start)
	for _, n := range c.Nodes() {
		k := c.Validator(t, n)
		eventually.Require(t, chain.PollEvery, chain.EpochBudget, n.Name+" to be paid", func() (bool, error) {
			if got := c.Earnings(t, n, k.Address); got.Cmp(before[n.Name]) <= 0 {
				return false, fmt.Errorf("earnings %s, was %s", got.String(), before[n.Name].String())
			}
			return true, nil
		})
	}
	c.RequireInvariants(t, "an epoch payout")
}
