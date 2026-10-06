//go:build cgo && orchardffi

package app_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/log/v2"
	"github.com/DeBrosOfficial/network/chain/app"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/pool"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// These tests run the real thing: the orchard library linked through cgo and the separately built
// verifier binary, both checking real Ironwood proofs, through FinalizeBlock. The bundles are the
// committed vectors (chain/x/shielded/orchardffi/testdata): a shield of 5000, a transfer that pays
// a fee of 100 and spends that note, and an unshield of the 4900 change note. Build the binary
// with `make orchard-verifier` and the library with `make orchard-lib`.

const (
	shieldAmount   = 5000
	transferFee    = 100
	unshieldAmount = 4900
	// bundleGas is the test genesis's action gas (10) for a one-action bundle.
	bundleGas = 10
	// txGas is what a signed shielded tx declares; its fee is txGas at the base fee of 1.
	txGas = 1_000_000
)

func realVerifierPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "x", "shielded", "orchardverifier-bin", "target", "release", "orama-orchard-verifier"))
	require.NoError(t, err)
	_, err = os.Stat(path)
	require.NoError(t, err, "build the out-of-process verifier first: make orchard-verifier")
	return path
}

func realChain(t *testing.T, mutate func(*shieldedtypes.GenesisState), opts ...chainOption) *shieldedChain {
	t.Helper()
	vectorChainIDFromFile(t)
	c := newShieldedChain(t, shieldedApp(t, t.TempDir(), realVerifierPath(t)), mutate, opts...)
	c.fund(t, c.alice, 10_000_000)
	c.fund(t, c.bob, 10_000_000)
	return c
}

func (c *shieldedChain) shieldMsg(t *testing.T) []byte {
	t.Helper()
	return c.signed(t, c.alice, txGas, &shieldedtypes.MsgShield{Signer: c.aliceAddr().String(), Bundle: loadVector(t, "ironwood-1-action")})
}

func (c *shieldedChain) transferTx(t *testing.T, raw []byte) []byte {
	t.Helper()
	return c.signerless(t, bundleGas, &shieldedtypes.MsgShieldedTransfer{Signer: shieldedtypes.SignerlessAddress().String(), Bundle: raw})
}

func (c *shieldedChain) unshieldTx(t *testing.T, who sdkKey, msg *shieldedtypes.MsgUnshield) []byte {
	t.Helper()
	return c.signed(t, who, txGas, msg)
}

type sdkKey = cryptotypesPrivKey

func (c *shieldedChain) topup(t *testing.T, raw []byte) *shieldedtypes.MsgUnshield {
	return &shieldedtypes.MsgUnshield{Signer: c.aliceAddr().String(), Bundle: raw, Target: shieldedtypes.UnshieldTargetFeeTopup}
}

func (c *shieldedChain) bond(t *testing.T, raw []byte) *shieldedtypes.MsgUnshield {
	return &shieldedtypes.MsgUnshield{
		Signer: c.aliceAddr().String(), Bundle: raw, Target: shieldedtypes.UnshieldTargetBond, Validator: c.validatorAddr().String(),
	}
}

func (c *shieldedChain) poolBalance(t *testing.T) math.Int {
	t.Helper()
	bal, err := c.app.ShieldedKeeper.Pools.Get(c.app.NewContext(true), poolKey())
	if err != nil {
		return math.ZeroInt()
	}
	return bal
}

func (c *shieldedChain) requireInvariants(t *testing.T) {
	t.Helper()
	ctx := c.app.NewContext(true)
	inv, err := c.app.ShieldedKeeper.CheckInvariants(ctx)
	require.NoError(t, err)
	require.True(t, inv.BalanceMatches && inv.PoolsNonNegative && inv.AccumulatorMatches, inv.Detail)
	feesInv, err := c.app.FeesKeeper.CheckInvariants(ctx)
	require.NoError(t, err)
	require.True(t, feesInv.EarningsMatchModule && feesInv.DepositsMatchModule && feesInv.FeesBalance, feesInv.Detail)
}

// delegated is what alice has delegated to committee member 0, in norama.
func (c *shieldedChain) delegated(t *testing.T) string {
	t.Helper()
	ctx := c.app.NewContext(true)
	delegation, err := c.app.StakingKeeper.GetDelegation(ctx, c.aliceAddr(), c.validatorAddr())
	if err != nil {
		return "0"
	}
	validator, err := c.app.StakingKeeper.GetValidator(ctx, c.validatorAddr())
	require.NoError(t, err)
	return validator.TokensFromShares(delegation.Shares).TruncateInt().String()
}

func (c *shieldedChain) spent(t *testing.T, vector string) bool {
	t.Helper()
	b, err := bundle.Parse(loadVector(t, vector))
	require.NoError(t, err)
	// A check state sits at the last committed block; NewContext alone would give it height 0.
	ctx := c.app.NewContextLegacy(true, cmtprotoHeader(c.height, c.blockTime(c.height)))
	resp, err := keeper.NewQueryServerImpl(c.app.ShieldedKeeper).NullifierSpent(ctx, &shieldedtypes.QueryNullifierSpentRequest{Nullifier: b.Nullifiers[0][:]})
	require.NoError(t, err)
	return resp.Spent
}

func requireTxOK(t *testing.T, results []*abciResult, i int, what string) {
	t.Helper()
	require.Zero(t, results[i].Code, "%s failed: %s", what, results[i].Log)
}

func TestShieldedReal_shieldTransferUnshieldThroughFinalizeBlock(t *testing.T) {
	c := realChain(t, nil)
	aliceBefore := c.app.BankKeeper.GetBalance(c.app.NewContext(true), c.aliceAddr(), params.BaseDenom).Amount

	// Shield: alice's bank balance funds the pool with the 5000 the bundle's value balance names,
	// and pays the burned nullifier fee (1) on top.
	results := c.block(t, c.shieldMsg(t))
	requireTxOK(t, results, 0, "shield")
	require.Equal(t, "5000", c.poolBalance(t).String())
	aliceAfter := c.app.BankKeeper.GetBalance(c.app.NewContext(true), c.aliceAddr(), params.BaseDenom).Amount
	require.Equal(t, aliceBefore.SubRaw(txGas+shieldAmount+1).String(), aliceAfter.String(), "the tx fee, the amount and one nullifier fee")
	require.True(t, c.spent(t, "ironwood-1-action"))
	c.requireInvariants(t)

	// Transfer: signer-less, no fee declared, exactly the bundle's gas; the fee is the value balance.
	results = c.block(t, c.transferTx(t, loadVector(t, "ironwood-transfer")))
	requireTxOK(t, results, 0, "transfer")
	require.Equal(t, int64(bundleGas), results[0].GasUsed, "a signer-less transfer is charged its fixed gas")
	require.Equal(t, "4900", c.poolBalance(t).String(), "the 100 fee left the pool")
	tip, err := c.app.FeesKeeper.GetEarnings(c.app.NewContext(true), sdk.AccAddress(c.validatorAddr()))
	require.NoError(t, err)
	require.Equal(t, "89", tip.String(), "100 less 10 base fee and 1 nullifier fee, paid to the proposer")
	c.requireInvariants(t)

	// Unshield: a fee top-up to alice's own fee-only balance.
	before, err := c.app.FeesKeeper.GetFeeBalance(c.app.NewContext(true), c.aliceAddr())
	require.NoError(t, err)
	results = c.block(t, c.unshieldTx(t, c.alice, c.topup(t, loadVector(t, "ironwood-unshield"))))
	requireTxOK(t, results, 0, "unshield")
	after, err := c.app.FeesKeeper.GetFeeBalance(c.app.NewContext(true), c.aliceAddr())
	require.NoError(t, err)
	require.Equal(t, "4899", after.Sub(before).String(), "4900 unshielded less one nullifier fee")
	require.True(t, c.poolBalance(t).IsZero(), "the pool is exactly drained: it paid out what the notes were worth")
	c.requireInvariants(t)

	_, count, err := c.app.ShieldedKeeper.NullifierState(c.app.NewContext(true))
	require.NoError(t, err)
	require.Equal(t, uint64(3), count)
	size, err := c.app.ShieldedKeeper.TreeSize.Get(c.app.NewContext(true))
	require.NoError(t, err)
	require.Equal(t, uint64(3), size, "one commitment per bundle")
}

func TestShieldedReal_replayedAndTamperedBundlesAreRefused(t *testing.T) {
	c := realChain(t, nil)
	requireTxOK(t, c.block(t, c.shieldMsg(t)), 0, "shield")

	tampered := loadVector(t, "ironwood-transfer")
	tampered[1+bundle.ActionLen+bundle.HeaderLen+3+200] ^= 1 // a byte of the Halo 2 proof
	results := c.block(t, c.transferTx(t, tampered))
	require.NotZero(t, results[0].Code)
	require.Contains(t, results[0].Log, "proof rejected")
	require.False(t, c.spent(t, "ironwood-transfer"), "a refused bundle spends nothing")

	good := c.transferTx(t, loadVector(t, "ironwood-transfer"))
	requireTxOK(t, c.block(t, good), 0, "the untampered bundle")
	require.True(t, c.spent(t, "ironwood-transfer"))

	results = c.block(t, good)
	require.NotZero(t, results[0].Code, "replay in a later block")
	require.Contains(t, results[0].Log, "nullifier")

	// Twice in one block: the first is accepted and the second is refused.
	c2 := realChain(t, nil)
	requireTxOK(t, c2.block(t, c2.shieldMsg(t)), 0, "shield")
	tx := c2.transferTx(t, loadVector(t, "ironwood-transfer"))
	results = c2.block(t, tx, tx)
	require.Zero(t, results[0].Code, results[0].Log)
	require.NotZero(t, results[1].Code, "the second copy in the same block")
	require.Contains(t, results[1].Log, "nullifier")
}

func TestShieldedReal_anAnchorTheChainNeverHadIsRefused(t *testing.T) {
	c := realChain(t, nil)
	requireTxOK(t, c.block(t, c.shieldMsg(t)), 0, "shield")
	// The unshield vector was proven against the tree after the transfer, which has not happened.
	results := c.block(t, c.unshieldTx(t, c.alice, c.topup(t, loadVector(t, "ironwood-unshield"))))
	require.NotZero(t, results[0].Code)
	require.Contains(t, results[0].Log, "anchor")
	require.True(t, c.poolBalance(t).Equal(math.NewInt(shieldAmount)))
}

func TestShieldedReal_feeTooLowIsRefused(t *testing.T) {
	// Action gas 100 at a base fee of 1 makes the fee floor 101, and the vector pays 100.
	c := realChain(t, func(gs *shieldedtypes.GenesisState) { gs.Params.ActionGas = 100 })
	requireTxOK(t, c.block(t, c.shieldMsg(t)), 0, "shield")
	tx := c.signerless(t, 100, &shieldedtypes.MsgShieldedTransfer{
		Signer: shieldedtypes.SignerlessAddress().String(), Bundle: loadVector(t, "ironwood-transfer"),
	})
	results := c.block(t, tx)
	require.NotZero(t, results[0].Code)
	require.Contains(t, results[0].Log, "fee")
	require.False(t, c.spent(t, "ironwood-transfer"))
}

func TestShieldedReal_wrongGasOrAFeeOrASignatureOnASignerlessTxIsRefused(t *testing.T) {
	c := realChain(t, nil)
	requireTxOK(t, c.block(t, c.shieldMsg(t)), 0, "shield")
	msg := &shieldedtypes.MsgShieldedTransfer{Signer: shieldedtypes.SignerlessAddress().String(), Bundle: loadVector(t, "ironwood-transfer")}
	for _, gas := range []uint64{bundleGas - 1, bundleGas + 1, 1_000_000} {
		results := c.block(t, c.signerless(t, gas, msg))
		require.NotZero(t, results[0].Code, "gas %d", gas)
		require.Contains(t, results[0].Log, "must declare")
	}
	require.False(t, c.spent(t, "ironwood-transfer"))

	// A timeout height in the past is honoured like on any tx.
	expired := c.app.TxConfig().NewTxBuilder()
	require.NoError(t, expired.SetMsgs(msg))
	expired.SetGasLimit(bundleGas)
	expired.SetTimeoutHeight(1)
	expiredTx, err := c.app.TxConfig().TxEncoder()(expired.GetTx())
	require.NoError(t, err)
	results := c.block(t, expiredTx)
	require.NotZero(t, results[0].Code)
	require.False(t, c.spent(t, "ironwood-transfer"))

	requireTxOK(t, c.block(t, c.signerless(t, bundleGas, msg)), 0, "exactly the bundle's gas")
}

// The unshield vectors are signed for one signer and one target. Anyone who copies the bundle out of
// the mempool and submits it as their own, or as another target, fails the signatures.
func TestShieldedReal_anUnshieldCannotBeRedirected(t *testing.T) {
	c := realChain(t, nil)
	requireTxOK(t, c.block(t, c.shieldMsg(t)), 0, "shield")
	requireTxOK(t, c.block(t, c.transferTx(t, loadVector(t, "ironwood-transfer"))), 0, "transfer")
	topup, bondVec := loadVector(t, "ironwood-unshield"), loadVector(t, "ironwood-unshield-bond")

	thief := c.topup(t, topup)
	thief.Signer = addr(c.bob).String()
	// Each tx is built just before its block: a tx that fails in its message still spends its
	// signer's sequence number.
	cases := map[string]func() []byte{
		"a different signer": func() []byte { return c.unshieldTx(t, c.bob, thief) },
		"the same bundle to another target": func() []byte {
			return c.unshieldTx(t, c.alice, &shieldedtypes.MsgUnshield{Signer: c.aliceAddr().String(), Bundle: topup, Target: shieldedtypes.UnshieldTargetBond, Validator: c.validatorAddr().String()})
		},
		"the bond bundle as a fee top-up": func() []byte { return c.unshieldTx(t, c.alice, c.topup(t, bondVec)) },
		"the bond bundle to another validator": func() []byte {
			return c.unshieldTx(t, c.alice, &shieldedtypes.MsgUnshield{Signer: c.aliceAddr().String(), Bundle: bondVec, Target: shieldedtypes.UnshieldTargetBond, Validator: sdk.ValAddress(addr(c.bob)).String()})
		},
		"the bond bundle as a node bond": func() []byte {
			return c.unshieldTx(t, c.alice, &shieldedtypes.MsgUnshield{Signer: c.aliceAddr().String(), Bundle: bondVec, Target: shieldedtypes.UnshieldTargetNodeBond, NodeId: "node-1", Role: 3})
		},
	}
	for name, build := range cases {
		results := c.block(t, build())
		require.NotZero(t, results[0].Code, name)
		require.Contains(t, results[0].Log, "signature rejected", name)
	}
	require.False(t, c.spent(t, "ironwood-unshield"))
	require.Equal(t, "4900", c.poolBalance(t).String(), "nothing left the pool")

	requireTxOK(t, c.block(t, c.unshieldTx(t, c.alice, c.topup(t, topup))), 0, "the unshield as its owner signed it")
}

func TestShieldedReal_overTheCapAFeeTopupFailsAtomicallyAndABondQueuesAndIsPaidNextWindow(t *testing.T) {
	// The floor is 1000 and 2% of the 4900 pool is 98, so the cap is 1000 against a 4899 unshield.
	// The transfer's 89 tip already counted against it, so a window has 911 left.
	c := realChain(t, func(gs *shieldedtypes.GenesisState) { gs.Params.UnshieldFloor = math.NewInt(1000) })
	requireTxOK(t, c.block(t, c.shieldMsg(t)), 0, "shield")
	requireTxOK(t, c.block(t, c.transferTx(t, loadVector(t, "ironwood-transfer"))), 0, "transfer")

	results := c.block(t, c.unshieldTx(t, c.alice, c.topup(t, loadVector(t, "ironwood-unshield"))))
	require.NotZero(t, results[0].Code, "a fee top-up over the cap is refused, not queued")
	require.Contains(t, results[0].Log, "cap exhausted")
	require.Equal(t, "4900", c.poolBalance(t).String())
	require.False(t, c.spent(t, "ironwood-unshield"), "the failed tx spent no nullifier")

	// The alternative spends the same note into a bond: over the cap, it queues.
	results = c.block(t, c.unshieldTx(t, c.alice, c.bond(t, loadVector(t, "ironwood-unshield-bond"))))
	requireTxOK(t, results, 0, "bond unshield")
	ctx := c.app.NewContext(true)
	require.True(t, c.poolBalance(t).IsZero())
	gs, err := c.app.ShieldedKeeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, gs.Queue, 1)
	// The block that queued it ends by serving its window: what the cap has left (911) is paid at
	// once, the remaining 3988 waits.
	require.Equal(t, "3988", gs.Queue[0].Amount.String())
	held := c.app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(shieldedtypes.ModuleName), params.BaseDenom).Amount
	require.Equal(t, "3988", held.String(), "the queued coins stay in the module account")
	require.Equal(t, "911", c.delegated(t), "paid through the real staking module, up to the cap")
	c.requireInvariants(t)

	c.block(t)
	require.Equal(t, "911", c.delegated(t), "the window is served once, however many blocks it has")

	// A day later the next window pays the whole cap.
	c.skew = pool.Window + time.Hour
	c.block(t)
	require.Equal(t, "1911", c.delegated(t))
	gs, err = c.app.ShieldedKeeper.ExportGenesis(c.app.NewContext(true))
	require.NoError(t, err)
	require.Equal(t, "2988", gs.Queue[0].Amount.String())
	c.requireInvariants(t)
}

func TestShieldedReal_shieldingFromEarningsAndTheTwoActionVector(t *testing.T) {
	c := realChain(t, nil)
	ctx := c.app.NewNextBlockContext(cmtprotoHeader(c.height+1, c.blockTime(c.height+1)))
	credit := math.NewInt(shieldAmount + 2)
	require.NoError(t, c.app.BankKeeper.MintCoins(ctx, "emission", sdk.NewCoins(sdk.NewCoin(params.BaseDenom, credit))))
	require.NoError(t, c.app.FeesKeeper.CreditEarnings(ctx, "emission", c.aliceAddr(), sdk.NewCoin(params.BaseDenom, credit)))
	writeCache(t, ctx)
	_, err := c.app.Commit()
	require.NoError(t, err)
	c.height++

	msg := &shieldedtypes.MsgShieldEarnings{Signer: c.aliceAddr().String(), Bundle: loadVector(t, "ironwood-2-action")}
	requireTxOK(t, c.block(t, c.signed(t, c.alice, txGas, msg)), 0, "shield earnings")
	require.Equal(t, "5000", c.poolBalance(t).String())
	left, err := c.app.FeesKeeper.GetEarnings(c.app.NewContext(true), c.aliceAddr())
	require.NoError(t, err)
	require.True(t, left.LT(math.NewInt(2)), "the 5000 and two nullifier fees left the earnings, and the tx fee came from the bank: %s left", left)
	c.requireInvariants(t)
}

// A node that stops after FinalizeBlock and before Commit runs the block again on restart, over its
// own database and the nullifier database it left behind. The replay must reach the same app hash.
func TestShieldedReal_aBlockReplayedAfterACrashIsIdentical(t *testing.T) {
	vectorChainIDFromFile(t)
	home := t.TempDir()
	// The application database stays in memory across the "restart"; the nullifier database is a
	// real file under home, which is the part that has to survive.
	db := dbm.NewMemDB()
	open := func() dbm.DB { return db }
	first := shieldedAppOn(t, open(), home, realVerifierPath(t))
	c := newShieldedChain(t, first, nil)
	c.fund(t, c.alice, 10_000_000)
	requireTxOK(t, c.block(t, c.shieldMsg(t)), 0, "shield")
	tx := c.transferTx(t, loadVector(t, "ironwood-transfer"))

	height := c.height
	resp := c.finalize(t, tx) // the block runs and writes the nullifiers, but never commits
	require.Zero(t, resp.TxResults[0].Code, resp.TxResults[0].Log)
	require.NoError(t, first.Close())

	// Restart on the same data: the uncommitted block is gone from state, but its nullifiers are
	// in the outside database.
	second := shieldedAppOn(t, open(), home, realVerifierPath(t))
	c.app = second
	c.height = height
	replay := c.finalize(t, tx)
	require.Zero(t, replay.TxResults[0].Code, "the replayed block must accept what the first attempt accepted: %s", replay.TxResults[0].Log)
	require.Equal(t, resp.AppHash, replay.AppHash, "the same block must give the same app hash")
	_, err := second.Commit()
	require.NoError(t, err)
	require.True(t, c.spent(t, "ironwood-transfer"))
	c.requireInvariants(t)
	require.NoError(t, second.Close())

	// And a restart after the commit keeps the nullifier: a replay of the transfer is refused.
	third := shieldedAppOn(t, open(), home, realVerifierPath(t))
	c.app = third
	results := c.block(t, tx)
	require.NotZero(t, results[0].Code)
	require.Contains(t, results[0].Log, "nullifier")
}

func TestShieldedReal_aKilledVerifierProcessIsRestartedForTheNextBlock(t *testing.T) {
	c := realChain(t, nil)
	requireTxOK(t, c.block(t, c.shieldMsg(t)), 0, "shield") // starts the process
	require.True(t, killVerifierProcesses(t), "the out-of-process verifier was running")
	requireTxOK(t, c.block(t, c.transferTx(t, loadVector(t, "ironwood-transfer"))), 0, "transfer after the kill")
}

// killVerifierProcesses kills every verifier child of this test binary.
func killVerifierProcesses(t *testing.T) bool {
	t.Helper()
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid()), "-f", "orama-orchard-verifier").Output()
	if err != nil {
		return false
	}
	killed := false
	for _, pid := range strings.Fields(string(out)) {
		if exec.Command("kill", "-9", pid).Run() == nil {
			killed = true
		}
	}
	time.Sleep(200 * time.Millisecond) // let the exit be noticed
	return killed
}

// A delegation the shielded module makes is held to the minimum x/power sets for a signed
// MsgDelegate. Here it is 1000 and the cap pays 100 a window, so a bond would be dust: the payment
// is refused each window and the request stays queued.
func TestShieldedReal_aBondThatWouldBeDustIsNotPaid(t *testing.T) {
	c := realChain(t, func(gs *shieldedtypes.GenesisState) { gs.Params.UnshieldFloor = math.NewInt(100) }, withMinDelegation(1000))
	requireTxOK(t, c.block(t, c.shieldMsg(t)), 0, "shield")
	requireTxOK(t, c.block(t, c.transferTx(t, loadVector(t, "ironwood-transfer"))), 0, "transfer")
	requireTxOK(t, c.block(t, c.unshieldTx(t, c.alice, c.bond(t, loadVector(t, "ironwood-unshield-bond")))), 0, "bond unshield")

	require.Equal(t, "0", c.delegated(t), "100 is below the 1000 minimum")
	gs, err := c.app.ShieldedKeeper.ExportGenesis(c.app.NewContext(true))
	require.NoError(t, err)
	require.Len(t, gs.Queue, 1)
	require.Equal(t, "4899", gs.Queue[0].Amount.String(), "nothing was paid, nothing was lost")
	c.skew = pool.Window + time.Hour
	c.block(t)
	require.Equal(t, "0", c.delegated(t))
	c.requireInvariants(t)
}

func panicMessage(t *testing.T, fn func()) (msg string) {
	t.Helper()
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected the node to refuse to start")
		if err, ok := r.(error); ok {
			msg = err.Error()
			return
		}
		msg = fmt.Sprint(r)
	}()
	fn()
	return ""
}

// A node whose nullifier database does not fold to the committed state would accept a spent
// nullifier: it refuses to start, and says where the database is and what to do.
func TestShieldedReal_aNodeRefusesToStartWithAMissingOrShortNullifierDatabase(t *testing.T) {
	vectorChainIDFromFile(t)
	home := t.TempDir()
	db := dbm.NewMemDB()
	first := shieldedAppOn(t, db, home, realVerifierPath(t))
	c := newShieldedChain(t, first, nil)
	c.fund(t, c.alice, 10_000_000)
	requireTxOK(t, c.block(t, c.shieldMsg(t)), 0, "shield")
	require.NoError(t, first.Close())

	// The same data starts.
	again := shieldedAppOn(t, db, home, realVerifierPath(t))
	require.NoError(t, again.Close())

	// The nullifier database is gone, as after restoring only application.db.
	require.NoError(t, os.RemoveAll(filepath.Join(home, "data", "shielded_nullifiers.db")))
	msg := panicMessage(t, func() { shieldedAppOn(t, db, home, realVerifierPath(t)) })
	require.Contains(t, msg, "shielded_nullifiers.db")
	require.Contains(t, msg, "does not match the chain state")
}

// The verifier binary is pinned by hash: a different file, or no pin at all, stops the node.
func TestShieldedReal_aNodeRefusesToStartWithAVerifierBinaryThatIsNotItsPin(t *testing.T) {
	vectorChainIDFromFile(t)
	path := realVerifierPath(t)
	open := func(pin string) {
		opts := simtestutil.AppOptionsMap{
			flags.FlagHome: t.TempDir(), app.FlagShieldedVerifier: path, app.FlagShieldedVerifierSHA256: pin,
		}
		a := app.NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, opts, baseapp.SetChainID(vectorChainID))
		_ = a.Close()
	}
	require.Contains(t, panicMessage(t, func() { open("") }), "no sha256 pin")
	require.Contains(t, panicMessage(t, func() { open(strings.Repeat("0", 64)) }), "does not match its pin")
	good, err := app.FileSHA256(path)
	require.NoError(t, err)
	open(good) // a matching pin starts
}
