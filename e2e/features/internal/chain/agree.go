//go:build e2e_fleet

package chain

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// sameChainAdvance is how many blocks past the highest head seen, once every
// node answers, all of them must commit before their blocks are compared.
const sameChainAdvance = 2

// quorumNumerator and quorumDenominator are CometBFT's commit threshold: a
// block needs MORE than 2/3 of the voting power.
const (
	quorumNumerator   = 2
	quorumDenominator = 3
)

// validatorSet is CometBFT /validators.
type validatorSet struct {
	Validators []struct {
		VotingPower Int `json:"voting_power"`
	} `json:"validators"`
}

// SurvivorsHaveQuorum reports whether the validators other than victim hold
// more than 2/3 of the voting power, as observer reads the set: whether the
// chain can commit blocks without victim. Three equal bootstrap seats cannot
// (exactly 2/3), five can.
func (c *Chain) SurvivorsHaveQuorum(t testing.TB, observer, victim fleet.Node) bool {
	t.Helper()
	var set validatorSet
	if err := c.Comet(t, observer, "/validators?per_page=100", &set); err != nil {
		t.Fatal(err)
	}
	total := int64(0)
	for _, v := range set.Validators {
		total += v.VotingPower.Int64()
	}
	return hasQuorum(total, total-c.MustStatus(t, victim).VotingPow)
}

// hasQuorum is CometBFT's rule: part is more than 2/3 of total.
func hasQuorum(total, part int64) bool {
	return total > 0 && quorumDenominator*part > quorumNumerator*total
}

// AppHashDisagreement reports the first two nodes that sit at the same height
// with different application hashes ("" when none does). Nodes at different
// heights are not comparable and are left out.
func AppHashDisagreement(statuses map[string]Status) string {
	names := make([]string, 0, len(statuses))
	for name := range statuses {
		names = append(names, name)
	}
	sort.Strings(names)
	byHeight := map[int64]string{}
	for _, name := range names {
		s := statuses[name]
		first, seen := byHeight[s.Height]
		if !seen {
			byHeight[s.Height] = name
			continue
		}
		if other := statuses[first]; other.AppHash != s.AppHash {
			return fmt.Sprintf("height %d: %s has app hash %s, %s has %s", s.Height, first, other.AppHash, name, s.AppHash)
		}
	}
	return ""
}

// RequireSameChain waits (up to budget) until every node in nodes answers, is
// not catching up and has committed sameChainAdvance blocks past the highest
// head seen when all of them first answered, then requires the same block
// hash and header app hash at its last three heights on every node and every
// module's invariants on every validator. A node that computed another app
// hash than the others cannot commit the next block, so a diverged node shows
// as one that does not reach the target.
func (c *Chain) RequireSameChain(t testing.TB, nodes []fleet.Node, budget time.Duration, after string) {
	t.Helper()
	target, reported := int64(0), false
	eventually.Require(t, PollEvery, budget, "every node to commit new blocks "+after, func() (bool, error) {
		statuses := map[string]Status{}
		for _, n := range nodes {
			s, err := c.NodeStatus(t, n)
			if err != nil {
				return false, err
			}
			if s.CatchingUp {
				return false, fmt.Errorf("%s is catching up at %d", n.Name, s.Height)
			}
			statuses[n.Name] = s
		}
		if d := AppHashDisagreement(statuses); d != "" && !reported {
			reported = true
			t.Errorf("the chain diverged %s: %s", after, d)
		}
		if target == 0 {
			for _, s := range statuses {
				target = max(target, s.Height+sameChainAdvance)
			}
		}
		for name, s := range statuses {
			if s.Height < target {
				return false, fmt.Errorf("%s is at %d, want %d", name, s.Height, target)
			}
		}
		return true, nil
	})
	if target == 0 {
		return
	}
	c.requireSameBlocks(t, nodes, target-sameChainAdvance, target, after)
	c.RequireInvariants(t, after)
}

// requireSameBlocks compares the blocks from..to of every node with the first
// node's.
func (c *Chain) requireSameBlocks(t testing.TB, nodes []fleet.Node, from, to int64, after string) {
	t.Helper()
	ref := nodes[0]
	for h := from; h <= to; h++ {
		want, err := c.BlockHeader(t, ref, h)
		if err != nil {
			t.Errorf("%s: %v", after, err)
			continue
		}
		for _, n := range nodes[1:] {
			got, err := c.BlockHeader(t, n, h)
			if err != nil || got.BlockHash != want.BlockHash || got.AppHash != want.AppHash {
				t.Errorf("%s, height %d: %s has %+v (%v), %s has %+v", after, h, n.Name, got, err, ref.Name, want)
			}
		}
	}
}
