//go:build e2e_fleet

package chain

// Funding a run chain (what every chain package assumes; read from code):
//
//   - Genesis supply is zero (chain-deploy.sh funds no account; x/emission's
//     premine gate, docs/CHAIN.md "Genesis starts at exactly zero supply").
//   - x/emission mints only at an epoch close, 60% of the schedule, and hands
//     it to x/power, which credits it to the committee members' EARNINGS
//     accounts (x/fees ledger), force-bonding 50% of a member's share until
//     its self-bond reaches 2x MinSelfBond (docs/CHAIN.md "Force-bonding").
//     The run's epochs are E2E_EPOCH_DURATION (default 60s) with
//     E2E_EPOCH_MIN_BLOCKS (default 5), so by stage 8 each validator operator
//     key holds thousands of ORAMA of earnings.
//   - Earnings are spendable ONLY for: the base fee of the signer's own
//     transactions (x/fees/keeper/feepay.go SettleFee, never through a fee
//     granter), the signer's own MsgCreateValidator/MsgDelegate bond
//     (x/fees/ante/bond_topup.go), and the signer's own state deposits
//     (x/fees/keeper/deposits.go LockDeposit: token metadata, x/nodes node
//     and cluster rows, x/cnft trees).
//   - Nothing credits a public bank balance within a run: a user-to-user
//     norama send is refused, rewards and tips land in earnings, and an
//     undelegation matures after the staking unbonding time (SDK default,
//     21 days). So every payment that must come from a BANK balance cannot
//     be funded: a tip, x/token's creation fee (x/token/keeper/create.go),
//     MsgBondNode (x/nodes/keeper/msg.go), MsgLockHouseBond
//     (x/houses/keeper/bond.go), x/storage deal escrow
//     (x/storage/keeper/deals.go) and an x/market bid
//     (x/market/keeper/msg_server.go). Tests of those assert the refusal the
//     code gives an unfunded payer; the success paths are reported blocked.
//   - A throwaway key (NewKey) has no account at all: its transactions are
//     refused before any message runs ("fee payer address ... does not exist").
//
// No faucet is invented: nothing here moves value except through the
// module paths above.

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Earnings is addr's x/fees earnings balance.
func (c *Chain) Earnings(t testing.TB, n fleet.Node, addr string) Int {
	t.Helper()
	var r struct {
		Balance Int `json:"balance"`
	}
	c.Query(t, n, &r, "fees", "earnings", addr)
	return r.Balance
}

// BaseFee is the x/fees base fee, norama per gas unit.
func (c *Chain) BaseFee(t testing.TB, n fleet.Node) Int {
	t.Helper()
	var r struct {
		BaseFee Int `json:"base_fee"`
	}
	c.Query(t, n, &r, "fees", "base-fee")
	return r.BaseFee
}

// Bank is addr's bank balance of norama.
func (c *Chain) Bank(t testing.TB, n fleet.Node, addr string) Int {
	t.Helper()
	var r struct {
		Balance struct {
			Amount Int `json:"amount"`
		} `json:"balance"`
	}
	c.Query(t, n, &r, "bank", "balance", addr, Denom)
	return r.Balance.Amount
}

// Supply is the bank's total norama supply, at height when height > 0.
func (c *Chain) Supply(t testing.TB, n fleet.Node, height int64) Int {
	t.Helper()
	var r struct {
		Amount struct {
			Amount Int `json:"amount"`
		} `json:"amount"`
	}
	args := []string{"bank", "total-supply-of", Denom}
	if height > 0 {
		c.QueryAt(t, n, height, &r, args...)
	} else {
		c.Query(t, n, &r, args...)
	}
	return r.Amount.Amount
}

// FundedValidator returns the validator key of node i once its earnings
// reach at least min norama, waiting for epoch closes if they do not yet.
func (c *Chain) FundedValidator(t testing.TB, i int, min Int) Key {
	t.Helper()
	k := c.Validator(t, c.Node(t, i))
	eventually.Require(t, PollEvery, EpochBudget, fmt.Sprintf("%s earnings to reach %s norama", k.Node.Name, min.String()), func() (bool, error) {
		got := c.Earnings(t, k.Node, k.Address)
		if got.Cmp(min) < 0 {
			return false, fmt.Errorf("earnings %s", got.String())
		}
		return true, nil
	})
	return k
}
