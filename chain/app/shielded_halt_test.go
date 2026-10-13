//go:build cgo && orchardffi

package app_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/client/flags"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"

	"github.com/DeBrosOfficial/network/chain/app"
	shieldedkeeper "github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// diesOnFirstRequest is a verifier binary that says it is ready and then exits at the first request
// it is given, as a process killed by the OOM killer or by an operator does. The first verifier (the
// linked library) accepts the committed vector, so the fault is the second verifier's alone.
const diesOnFirstRequest = "#!/bin/sh\n" +
	"printf '\\000\\000\\000\\011\\377\\377\\377\\377\\377\\377\\377\\377\\000'\n" +
	"head -c 1 >/dev/null\n" +
	"exit 1\n"

func faultingVerifier(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orama-orchard-verifier")
	require.NoError(t, os.WriteFile(path, []byte(diesOnFirstRequest), 0o755))
	return path
}

// fundedChain is realChain over a given application database and home, with the verifier at path.
func fundedChain(t *testing.T, db dbm.DB, home, verifier string) *shieldedChain {
	t.Helper()
	c := newShieldedChain(t, shieldedAppOn(t, db, home, verifier), nil)
	c.fund(t, c.alice, 10_000_000)
	return c
}

func haltOfBlock(t *testing.T, fn func()) (halt shieldedkeeper.VerifierHalt) {
	t.Helper()
	defer func() {
		r := recover()
		require.NotNil(t, r, "FinalizeBlock must stop the node when the verifier fails, not record a result")
		var ok bool
		halt, ok = r.(shieldedkeeper.VerifierHalt)
		require.True(t, ok, "the panic is the verifier halt, got %T: %v", r, r)
	}()
	fn()
	return halt
}

// The chain-chaos verifier test's finding, reproduced through the real app: a node whose verifier
// process dies in the middle of a block must not record a result of its own. It stops with the
// documented error, nothing of the block is committed, and after the restart the same block gives
// the result and the app hash of a node whose verifier never failed.
func TestShieldedReal_aVerifierThatDiesInABlockHaltsTheNodeAndTheRestartAgreesWithItsPeers(t *testing.T) {
	vectorChainIDFromFile(t)

	healthy := fundedChain(t, dbm.NewMemDB(), t.TempDir(), realVerifierPath(t))
	tx := healthy.shieldMsg(t)
	want := healthy.finalize(t, tx)
	require.Zero(t, want.TxResults[0].Code, want.TxResults[0].Log)

	db, home := dbm.NewMemDB(), t.TempDir()
	faulty := fundedChain(t, db, home, faultingVerifier(t))
	require.Equal(t, tx, faulty.shieldMsg(t), "both nodes sign the same transaction")
	committed := faulty.height
	exitCode := -1
	old := app.VerifierHaltExit
	app.VerifierHaltExit = func(code int) { exitCode = code }
	t.Cleanup(func() { app.VerifierHaltExit = old })
	halt := haltOfBlock(t, func() { faulty.finalize(t, tx) })
	require.Equal(t, app.ExitVerifierHalt, exitCode, "the node ends the process with the verifier halt status, not only its consensus routine")
	require.NotEmpty(t, halt.BundleHash)
	require.ErrorIs(t, halt, verify.ErrVerifierFault)
	require.Contains(t, halt.Error(), "--shielded-verifier")
	require.Contains(t, halt.Error(), "executes the block again")
	require.Equal(t, committed, faulty.app.LastBlockHeight(), "nothing of the halted block is committed")
	require.NoError(t, faulty.app.Close())

	// The restart: the verifier works now, the same data, the same block.
	restarted := shieldedAppOn(t, db, home, realVerifierPath(t))
	faulty.app, faulty.height = restarted, committed
	got := faulty.finalize(t, tx)
	require.Zero(t, got.TxResults[0].Code, got.TxResults[0].Log)
	require.Equal(t, want.TxResults[0].Code, got.TxResults[0].Code)
	require.Equal(t, want.AppHash, got.AppHash, "the restarted node reaches the app hash of the node that never faulted")
}

// A bundle the verifiers reject is rejected by every node with the same result, a node whose verifier
// is healthy halts on nothing: the verdict depends on the bytes alone.
func TestShieldedReal_aRejectedBundleGivesTheSameResultOnEveryNode(t *testing.T) {
	vectorChainIDFromFile(t)
	var hashes [][]byte
	var logs []string
	for node := 0; node < 2; node++ {
		c := fundedChain(t, dbm.NewMemDB(), t.TempDir(), realVerifierPath(t))
		raw := loadVector(t, "ironwood-1-action")
		raw[len(raw)-1] ^= 1 // inside the binding signature
		tx := c.signed(t, c.alice, txGas, &shieldedtypes.MsgShield{Signer: c.aliceAddr().String(), Bundle: raw})
		resp := c.finalize(t, tx)
		require.NotZero(t, resp.TxResults[0].Code)
		hashes = append(hashes, resp.AppHash)
		logs = append(logs, resp.TxResults[0].Log)
	}
	require.Equal(t, hashes[0], hashes[1])
	require.Equal(t, logs[0], logs[1])
}

// Only Linux starts the verifier from the file whose hash it checked, so with the library and the
// binary both in place a host that cannot is still refused on a chain that must verify, and a
// localnet or a scripted devnet may run anywhere.
func TestCheckNodeCanVerify_aHostThatCannotStartFromTheCheckedFileIsRefused(t *testing.T) {
	opts := simtestutil.AppOptionsMap{flags.FlagHome: t.TempDir(), app.FlagShieldedVerifier: realVerifierPath(t)}
	err := app.CheckNodeCanVerify("orama-stagenet-6", opts)
	if runtime.GOOS == "linux" {
		require.NoError(t, err)
	} else {
		require.ErrorContains(t, err, "only Linux can do")
		require.ErrorContains(t, err, runtime.GOOS)
	}
	require.NoError(t, app.CheckNodeCanVerify("orama-localnet-1", opts))
}

// mempoolVerifierReady waits for the mempool's verifier process, which starts in the background.
func mempoolVerifierReady(t *testing.T, a *app.OramaApp) {
	t.Helper()
	for _, set := range [][]verify.Verifier{a.ShieldedMempoolVerifiers, a.ShieldedProposalVerifiers} {
		ready, ok := set[0].(interface{ Ready() bool })
		require.True(t, ok)
		require.Eventually(t, ready.Ready, 30*time.Second, 20*time.Millisecond, "a verifier process did not become ready")
	}
}

func (c *shieldedChain) process(t *testing.T, txs ...[]byte) abci.ResponseProcessProposal_ProposalStatus {
	t.Helper()
	resp, err := c.app.ProcessProposal(&abci.RequestProcessProposal{
		Height: c.height + 1, Time: c.blockTime(c.height + 1), ProposerAddress: c.committee[0].cons.PubKey().Address(),
		Txs: txs, Hash: bytes.Repeat([]byte{2}, 32),
	})
	require.NoError(t, err)
	return resp.Status
}

func (c *shieldedChain) prepare(t *testing.T, txs ...[]byte) [][]byte {
	t.Helper()
	resp, err := c.app.PrepareProposal(&abci.RequestPrepareProposal{
		Height: c.height + 1, Time: c.blockTime(c.height + 1), ProposerAddress: c.committee[0].cons.PubKey().Address(),
		MaxTxBytes: 4 << 20, Txs: txs,
	})
	require.NoError(t, err)
	return resp.Txs
}

// A block proposal is checked before it is voted for: an honest bundle is accepted, a tampered one
// is refused (a verdict every validator reaches), and PrepareProposal leaves the tampered one out.
func TestShieldedReal_aProposalWithATamperedBundleIsRefusedAndAnHonestOneAccepted(t *testing.T) {
	c := realChain(t, nil)
	mempoolVerifierReady(t, c.app)
	honest := c.shieldMsg(t)
	raw := loadVector(t, "ironwood-1-action")
	raw[len(raw)-1] ^= 1
	tampered := c.signed(t, c.alice, txGas, &shieldedtypes.MsgShield{Signer: c.aliceAddr().String(), Bundle: raw})

	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, c.process(t, honest))
	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(t, tampered))
	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(t, honest, tampered), "one bad bundle refuses the whole proposal")
	require.Equal(t, [][]byte{honest}, c.prepare(t, tampered, honest), "PrepareProposal leaves the failing bundle out")
}

// A bundle that kills the verifier is a nil vote, never a halt: the proposal is refused and the node
// neither exits nor stops, and FinalizeBlock is not reached.
func TestShieldedReal_aProposalWithABundleThatFaultsTheVerifierIsRefusedAndNeverHalts(t *testing.T) {
	vectorChainIDFromFile(t)
	exited := -1
	old := app.VerifierHaltExit
	app.VerifierHaltExit = func(code int) { exited = code }
	t.Cleanup(func() { app.VerifierHaltExit = old })

	c := fundedChain(t, dbm.NewMemDB(), t.TempDir(), faultingVerifier(t))
	mempoolVerifierReady(t, c.app)
	tx := c.shieldMsg(t)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(t, tx), "the mempool's child dies on the bundle: a fault is a nil vote")
	require.Equal(t, -1, exited, "ProcessProposal never halts the node")
	require.Empty(t, c.prepare(t, tx), "PrepareProposal leaves a bundle it cannot check out of the block")
	require.Equal(t, -1, exited)
}

// FinalizeBlock still verifies, whatever ProcessProposal said, and still halts on a fault: a
// proposal that was accepted and a verifier that then fails is the case the halt is for.
func TestShieldedReal_finalizeBlockStillVerifiesAndHaltsOnAFaultAfterAnAcceptedProposal(t *testing.T) {
	vectorChainIDFromFile(t)
	exited := -1
	old := app.VerifierHaltExit
	app.VerifierHaltExit = func(code int) { exited = code }
	t.Cleanup(func() { app.VerifierHaltExit = old })

	c := fundedChain(t, dbm.NewMemDB(), t.TempDir(), faultingVerifier(t))
	tx := c.shieldMsg(t)
	haltOfBlock(t, func() { c.finalize(t, tx) })
	require.Equal(t, app.ExitVerifierHalt, exited)
}
