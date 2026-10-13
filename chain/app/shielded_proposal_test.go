package app_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/app"
	shieldedante "github.com/DeBrosOfficial/network/chain/x/shielded/ante"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

const signerlessLimit = int(shieldedtypes.DefaultMaxSignerlessPerBlock)

// signerlessTxs builds n signer-less transfers. Each carries a different (never verified) bundle, so
// a proposal check that reached the verifier would be a test failure of its own.
func signerlessTxs(t *testing.T, oramaApp *app.OramaApp, n int) [][]byte {
	t.Helper()
	txs := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		b := oramaApp.TxConfig().NewTxBuilder()
		require.NoError(t, b.SetMsgs(&shieldedtypes.MsgShieldedTransfer{
			Signer: shieldedtypes.SignerlessAddress().String(),
			Bundle: append(shieldedTestBundle(), byte(i)),
		}))
		b.SetGasLimit(250_000)
		raw, err := oramaApp.TxConfig().TxEncoder()(b.GetTx())
		require.NoError(t, err)
		txs = append(txs, raw)
	}
	return txs
}

func countSignerless(t *testing.T, oramaApp *app.OramaApp, txs [][]byte) int {
	t.Helper()
	n := 0
	for _, raw := range txs {
		tx, err := oramaApp.TxConfig().TxDecoder()(raw)
		if err == nil && shieldedante.IsSignerless(tx) {
			n++
		}
	}
	return n
}

func TestProcessProposal_refusesMoreSignerlessTransfersThanTheBlockMayHold(t *testing.T) {
	c := newShieldedChain(t, shieldedApp(t, "", ""), nil)
	process := func(txs [][]byte) abci.ResponseProcessProposal_ProposalStatus {
		resp, err := c.app.ProcessProposal(&abci.RequestProcessProposal{
			Height: c.height + 1, Time: c.blockTime(c.height + 1),
			ProposerAddress: c.committee[0].cons.PubKey().Address(), Txs: txs,
		})
		require.NoError(t, err)
		return resp.Status
	}
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, process(signerlessTxs(t, c.app, signerlessLimit)), "the limit itself fits")
	require.Equal(t, abci.ResponseProcessProposal_REJECT, process(signerlessTxs(t, c.app, signerlessLimit+1)),
		"%d signer-less transfers exceed the %d a block may hold", signerlessLimit+1, signerlessLimit)
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, process(nil), "an empty block")
}

func TestPrepareProposal_buildsNoMoreSignerlessTransfersThanTheBlockMayHold(t *testing.T) {
	c := newShieldedChain(t, shieldedApp(t, "", ""), nil)
	c.fund(t, c.alice, 1_000_000)
	signed := c.signed(t, c.alice, 200_000, &shieldedtypes.MsgShield{Signer: c.aliceAddr().String(), Bundle: shieldedTestBundle()})
	// The signed tx comes last in the mempool: it must not be pushed out by the signer-less ones.
	mempool := append(signerlessTxs(t, c.app, signerlessLimit+10), signed)

	req := &abci.RequestPrepareProposal{
		Height: c.height + 1, Time: c.blockTime(c.height + 1),
		ProposerAddress: c.committee[0].cons.PubKey().Address(), MaxTxBytes: 1 << 20, Txs: mempool,
	}
	prep, err := c.app.PrepareProposal(req)
	require.NoError(t, err)
	require.Equal(t, signerlessLimit, countSignerless(t, c.app, prep.Txs), "the surplus signer-less transfers stay in the mempool")
	require.Contains(t, prep.Txs, signed, "a signed tx is not counted against the signer-less limit")

	resp, err := c.app.ProcessProposal(&abci.RequestProcessProposal{
		Height: req.Height, Time: req.Time, ProposerAddress: req.ProposerAddress, Txs: prep.Txs,
	})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, resp.Status, "an honest proposer never builds a block it would refuse")
}

func TestInclusion_signerlessTransfersAreCountedWithTheListedTransactions(t *testing.T) {
	c := newInclusionChain(t, inclusionEnableHeight)
	tx := c.editTx(1, 1)
	c.starve(tx)
	c.finalize()
	listed := c.extend()
	ec := c.extendedCommit([][]byte{listed, listed, listed})

	block := c.prepare(ec, signerlessTxs(t, c.app, signerlessLimit+10)...)
	require.Equal(t, block[1], tx, "the listed tx still leads the block")
	require.Equal(t, signerlessLimit, countSignerless(t, c.app, block), "with a forced tx in the block the limit still holds")
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, c.process(ec, block))

	over := append(append([][]byte{}, block...), signerlessTxs(t, c.app, 1)...)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(ec, over), "one signer-less transfer more than the block may hold")
}

func TestInclusion_aSignerlessTransferCannotBeListed(t *testing.T) {
	c := newInclusionChain(t, inclusionEnableHeight)
	c.finalize()
	raw := signerlessTxs(t, c.app, 1)[0]
	res, err := c.app.CheckTx(&abci.RequestCheckTx{Tx: raw, Type: abci.CheckTxType_New})
	require.NoError(t, err)
	_ = res // the transfer's own admission is not the question: whatever it answers, it must not be listable
	c.clock = c.clock.Add(app.InclusionIncludeAfter + 1)
	require.Empty(t, c.extend(), "an unsigned transfer has no sender and no sequence, so no list can force it into a block")
}
