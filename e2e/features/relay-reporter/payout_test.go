//go:build e2e_fleet

package relayreporter

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// settledEpochSearch is how many closed epochs back from the current one the test looks for one that paid.
const settledEpochSearch = 60

// paidEpoch finds the newest epoch x/relay settled with something minted, or says the test does not apply:
// a chain pays only after its reporters (a quorum of three) have reported, which needs the reporter set,
// the directory authorities' votes and bandwidth measurements this target may not have.
func paidEpoch(t *testing.T, c *chain.Chain) (uint64, *big.Int) {
	t.Helper()
	n := c.Node(t, chain.OperatorNode)
	state := c.Epoch(t, n, 0)
	current := state.CurrentEpoch.Uint64()
	for epoch := current; epoch > 0 && current-epoch < settledEpochSearch; epoch-- {
		out := c.QueryOut(t, n, "relay", "epoch", strconv.FormatUint(epoch, 10))
		if out.Exit != 0 {
			continue
		}
		var v struct {
			Result struct {
				Minted chain.Int `json:"minted"`
			} `json:"result"`
		}
		c.Query(t, n, &v, "relay", "epoch", strconv.FormatUint(epoch, 10))
		if !v.Result.Minted.IsZero() {
			return epoch, &v.Result.Minted.Int
		}
	}
	harness.SkipNotApplicable(t, fmt.Sprintf("none of the last %d epochs of this chain paid a relay: relay pay needs the reporter quorum to have reported a closed epoch (website/src/docs/blockchain/relay-rewards.mdx)", settledEpochSearch))
	return 0, nil
}

// A settled epoch's payouts are the rows x/relay's mint was split into: they add up to what the epoch
// minted, each belongs to a relay that is registered under the operator the row names, and that
// operator has earnings (x/relay credits the operator's earnings with 90% of its total, after the
// burn and the archive share).
func TestRelay_aClosedEpochPaidRegisteredRelaysAndCreditedTheirOperators(t *testing.T) {
	c := chain.New(t)
	epoch, minted := paidEpoch(t, c)
	n := c.Node(t, chain.OperatorNode)

	var v struct {
		Payouts []struct {
			RSAFingerprint string    `json:"rsa_fingerprint"`
			Operator       string    `json:"operator"`
			Amount         chain.Int `json:"amount"`
		} `json:"payouts"`
	}
	c.Query(t, n, &v, "relay", "payouts", strconv.FormatUint(epoch, 10))
	if len(v.Payouts) == 0 {
		t.Fatalf("epoch %d minted %s and listed no payout", epoch, minted)
	}
	sum := new(big.Int)
	for _, p := range v.Payouts {
		sum.Add(sum, &p.Amount.Int)
		raw, err := base64.StdEncoding.DecodeString(p.RSAFingerprint)
		if err != nil {
			t.Fatalf("epoch %d payout fingerprint %q: %v", epoch, p.RSAFingerprint, err)
		}
		var r struct {
			Relay chainRelay `json:"relay"`
		}
		c.Query(t, n, &r, "relay", "relay", hex.EncodeToString(raw))
		if r.Relay.Operator != p.Operator {
			t.Errorf("epoch %d paid %s to %s, but the relay is registered under %s", epoch, p.Amount, p.Operator, r.Relay.Operator)
		}
		if c.Earnings(t, n, p.Operator).IsZero() {
			t.Errorf("epoch %d paid %s to %s, whose earnings are zero: the payout was not credited, or the operator withdrew all of it (a relay operator of the run does not withdraw)", epoch, p.Amount, p.Operator)
		}
	}
	if sum.Cmp(minted) != 0 {
		t.Errorf("epoch %d minted %s, its payouts add up to %s", epoch, minted, sum)
	}
	c.RequireInvariants(t, "a relay payout")
}
