//go:build e2e_fleet

package chain

// Funding a run chain (what every chain package assumes; read from code):
//
//   - Genesis supply is zero (chain-deploy.sh funds no account; x/emission's
//     premine gate, docs/whitepaper/technical-reference/vol2/40-economics.md "Genesis starts at exactly zero supply").
//   - x/emission mints only at an epoch close, 60% of the schedule, and hands
//     it to x/power, which credits it to the committee members' EARNINGS
//     accounts (x/fees ledger), force-bonding 50% of a member's share until
//     its self-bond reaches 2x MinSelfBond (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md "Force-bonding").
//     The run's epochs are E2E_EPOCH_DURATION (default 60s) with
//     E2E_EPOCH_MIN_BLOCKS (default 5), so by stage 8 each validator operator
//     key holds thousands of ORAMA of earnings.
//   - Earnings are spendable ONLY for: the base fee of the signer's own
//     transactions (x/fees/keeper/feepay.go SettleFee, never through a fee
//     granter), the signer's own MsgCreateValidator/MsgDelegate bond
//     (x/fees/ante/bond_topup.go), and the signer's own state deposits
//     (x/fees/keeper/deposits.go LockDeposit: token metadata, x/nodes node
//     and cluster rows, x/cnft trees).
//   - Apart from the faucet below, nothing credits a public bank balance
//     within a run: a user-to-user norama send is refused, rewards and tips
//     land in earnings, and an undelegation matures after the staking
//     unbonding time (SDK default, 21 days). So a payment that must come from
//     a BANK balance (a tip, x/token's creation fee
//     (x/token/keeper/create.go), MsgBondNode (x/nodes/keeper/msg.go),
//     MsgLockHouseBond (x/houses/keeper/bond.go), x/storage deal escrow
//     (x/storage/keeper/deals.go), an x/market bid
//     (x/market/keeper/msg_server.go)) is funded through the faucet. The
//     tests written before the faucet existed still assert the refusal the
//     code gives an unfunded payer and report the success paths blocked;
//     they move to NewFundedKey as they are revisited.
//   - A throwaway key (NewKey) has no account at all: its transactions are
//     refused before any message runs ("fee payer address ... does not exist")
//     until it is funded (NewFundedKey).
//
// A faucet exists on a test network: genesis sets x/emission's
// faucet_enabled on every run chain (e2e/scripts/chain-deploy.sh) and on
// stagenet (chain/scripts/stagenet/deploy.sh), and `orama chain faucet` mints
// a bank balance to any address, signing on a node with its operator key
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-chain-faucet). A recipient may be dripped once
// per cooldown, so a test funds a FRESH key, never a shared one.

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
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

// FaucetNode is the validator whose operator key signs every faucet drip: the
// last one, which the chain packages use least (they sign with the operator
// keys of nodes 0..2, and a key signs one transaction per sequence).
func (c *Chain) FaucetNode(t testing.TB) fleet.Node {
	t.Helper()
	return c.Node(t, len(c.Nodes())-1)
}

// Faucet runs `orama chain faucet addr --amount ...` as the operator's runner,
// signing on FaucetNode, and returns the CLI's result whatever its exit code.
func (c *Chain) Faucet(t testing.TB, addr string, amount Int) oramacli.Result {
	t.Helper()
	return infra.Run(t, harness.CLI(t), "chain", "faucet", addr,
		"--env", c.F.State.Env, "--node", c.FaucetNode(t).PublicIP, "--amount", amount.String())
}

// Fund drips amount of norama to addr from the faucet and returns addr's bank
// balance as n reads it once the drip is in a block (the CLI waits for the
// block).
func (c *Chain) Fund(t testing.TB, n fleet.Node, addr string, amount Int) Int {
	t.Helper()
	infra.ExpectExit(t, c.Faucet(t, addr, amount), infra.ExitOK)
	return c.Bank(t, n, addr)
}

// NewFundedKey is NewKey plus a faucet drip of amount: a key whose account
// exists and holds amount of BANK balance, the payer the success paths in
// "Funding a run chain" above need.
func (c *Chain) NewFundedKey(t testing.TB, n fleet.Node, name string, amount Int) Key {
	t.Helper()
	k := c.NewKey(t, n, name)
	if got := c.Fund(t, n, k.Address, amount); got.Cmp(amount) != 0 {
		t.Fatalf("%s: %s holds %s norama after a faucet drip of %s", n.Name, k.Address, got.String(), amount.String())
	}
	return k
}
