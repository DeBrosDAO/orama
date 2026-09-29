package app_test

import (
	"bytes"
	"encoding/json"
	"sort"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	protoio "github.com/cosmos/gogoproto/io"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

const (
	inclusionEnableHeight = 2
	inclusionMaxTxBytes   = 1 << 20
	inclusionValPower     = 100
)

// inclusionChain is a three-member committee chain with vote extensions
// enabled at inclusionEnableHeight. One app plays every validator: they share
// state, and each validator's own mempool is modelled by which of them was
// handed a transaction through CheckTx.
type inclusionChain struct {
	t       *testing.T
	app     *app.OramaApp
	keys    []committeeKey
	genesis time.Time
	clock   time.Time
	height  int64
}

func newInclusionChain(t *testing.T, enableHeight int64) *inclusionChain {
	t.Helper()
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, keys := committeeGenesis(t, oramaApp, 3, 365)
	stateBytes, err := json.Marshal(genState)
	require.NoError(t, err)
	_, err = oramaApp.InitChain(&abci.RequestInitChain{
		ChainId:       testChainID,
		InitialHeight: 1,
		Time:          genesisTime,
		AppStateBytes: stateBytes,
		ConsensusParams: &cmtproto.ConsensusParams{
			Block:     &cmtproto.BlockParams{MaxGas: 100_000_000, MaxBytes: 22_020_096},
			Evidence:  &cmtproto.EvidenceParams{MaxAgeNumBlocks: 100_000, MaxAgeDuration: time.Hour, MaxBytes: 1_048_576},
			Validator: &cmtproto.ValidatorParams{PubKeyTypes: []string{cmted25519.KeyType}},
			Abci:      &cmtproto.ABCIParams{VoteExtensionsEnableHeight: enableHeight},
		},
	})
	require.NoError(t, err)
	c := &inclusionChain{t: t, app: oramaApp, keys: keys, genesis: genesisTime, clock: genesisTime}
	oramaApp.SetInclusionClock(func() time.Time { return c.clock })
	c.finalize() // height 1 closes epoch 1, so every member has earnings to pay fees from.
	return c
}

func (c *inclusionChain) blockTime() time.Time {
	return c.genesis.Add(time.Duration(c.height+1) * 2 * time.Second)
}

func (c *inclusionChain) finalize(txs ...[]byte) *abci.ResponseFinalizeBlock {
	c.t.Helper()
	c.height++
	resp, err := c.app.FinalizeBlock(&abci.RequestFinalizeBlock{Height: c.height, Time: c.blockTime(), Txs: txs})
	require.NoError(c.t, err)
	_, err = c.app.Commit()
	require.NoError(c.t, err)
	return resp
}

func (c *inclusionChain) consAddr(i int) []byte { return c.keys[i].cons.PubKey().Address() }

// editTx is a signed MsgEditValidator from member i, which spends gas, moves
// no coins, and increments i's sequence.
func (c *inclusionChain) editTx(i int, seed int64) []byte {
	c.t.Helper()
	member := sdk.AccAddress(c.keys[i].account.PubKey().Address())
	msg := stakingtypes.NewMsgEditValidator(sdk.ValAddress(member).String(), stakingtypes.Description{Moniker: "renamed"}, nil, nil)
	return signedTx(c.t, c.app, c.keys[i].account, seed, 120_000, msg)
}

func (c *inclusionChain) checkTx(tx []byte) {
	c.t.Helper()
	res, err := c.app.CheckTx(&abci.RequestCheckTx{Tx: tx, Type: abci.CheckTxType_New})
	require.NoError(c.t, err)
	require.Zero(c.t, res.Code, "CheckTx refused the tx: %s", res.Log)
}

// extend has a validator vote at the current height and returns its
// extension, after VerifyVoteExtension accepts it.
func (c *inclusionChain) extend() []byte { return c.extendAt(c.height) }

func (c *inclusionChain) extendAt(height int64) []byte {
	c.t.Helper()
	resp, err := c.app.ExtendVote(nil, &abci.RequestExtendVote{Height: height, Hash: bytes.Repeat([]byte{1}, 32)})
	require.NoError(c.t, err)
	c.verify(height, 0, resp.VoteExtension, abci.ResponseVerifyVoteExtension_ACCEPT)
	return resp.VoteExtension
}

func (c *inclusionChain) verify(height int64, val int, ext []byte, want abci.ResponseVerifyVoteExtension_VerifyStatus) {
	c.t.Helper()
	resp, err := c.app.VerifyVoteExtension(&abci.RequestVerifyVoteExtension{
		Height: height, Hash: bytes.Repeat([]byte{1}, 32), ValidatorAddress: c.consAddr(val), VoteExtension: ext,
	})
	require.NoError(c.t, err)
	require.Equal(c.t, want, resp.Status)
}

// extendedCommit builds the extended commit for the block at c.height from
// each validator's extension (nil for none), signed with its consensus key the
// way CometBFT signs a vote extension.
func (c *inclusionChain) extendedCommit(exts [][]byte) abci.ExtendedCommitInfo {
	c.t.Helper()
	votes := make([]abci.ExtendedVoteInfo, len(c.keys))
	for i, k := range c.keys {
		var sig []byte
		var buf bytes.Buffer
		cve := cmtproto.CanonicalVoteExtension{Extension: exts[i], Height: c.height, Round: 0, ChainId: testChainID}
		require.NoError(c.t, protoio.NewDelimitedWriter(&buf).WriteMsg(&cve))
		sig, err := k.cons.Sign(buf.Bytes())
		require.NoError(c.t, err)
		votes[i] = abci.ExtendedVoteInfo{
			Validator:          abci.Validator{Address: c.consAddr(i), Power: inclusionValPower},
			VoteExtension:      exts[i],
			ExtensionSignature: sig,
			BlockIdFlag:        cmtproto.BlockIDFlagCommit,
		}
	}
	sort.Slice(votes, func(i, j int) bool { return bytes.Compare(votes[i].Validator.Address, votes[j].Validator.Address) < 0 })
	return abci.ExtendedCommitInfo{Round: 0, Votes: votes}
}

func commitInfoOf(ec abci.ExtendedCommitInfo) abci.CommitInfo {
	ci := abci.CommitInfo{Round: ec.Round}
	for _, v := range ec.Votes {
		ci.Votes = append(ci.Votes, abci.VoteInfo{Validator: v.Validator, BlockIdFlag: v.BlockIdFlag})
	}
	return ci
}

func (c *inclusionChain) prepare(ec abci.ExtendedCommitInfo, mempool ...[]byte) [][]byte {
	c.t.Helper()
	resp, err := c.app.PrepareProposal(&abci.RequestPrepareProposal{
		Height: c.height + 1, Time: c.blockTime(), ProposerAddress: c.consAddr(0),
		MaxTxBytes: inclusionMaxTxBytes, Txs: mempool, LocalLastCommit: ec,
	})
	require.NoError(c.t, err)
	return resp.Txs
}

func (c *inclusionChain) process(ec abci.ExtendedCommitInfo, txs [][]byte) abci.ResponseProcessProposal_ProposalStatus {
	c.t.Helper()
	resp, err := c.app.ProcessProposal(&abci.RequestProcessProposal{
		Height: c.height + 1, Time: c.blockTime(), ProposerAddress: c.consAddr(0),
		Txs: txs, ProposedLastCommit: commitInfoOf(ec), Hash: bytes.Repeat([]byte{2}, 32),
	})
	require.NoError(c.t, err)
	return resp.Status
}

// starve makes a tx that validators have held for longer than include_after:
// it goes through CheckTx on this node and the clock then moves past the delay.
func (c *inclusionChain) starve(tx []byte) {
	c.checkTx(tx)
	c.clock = c.clock.Add(app.InclusionIncludeAfter + time.Second)
}

func TestInclusion_listedTxMustBeInTheNextBlock(t *testing.T) {
	c := newInclusionChain(t, inclusionEnableHeight)
	tx := c.editTx(1, 1)
	c.starve(tx)

	c.finalize() // height 2: the block validators extend a vote on; it does not contain tx.
	require.Equal(t, int64(2), c.height)
	listed := c.extend()
	require.NotEmpty(t, listed, "a starved mempool tx must be listed")
	ec := c.extendedCommit([][]byte{listed, listed, nil})

	block := c.prepare(ec) // proposer 0 has nothing of its own in its mempool.
	require.Len(t, block, 2, "the injected commit and the listed tx")
	require.True(t, bytes.HasPrefix(block[0], []byte(app.InjectedCommitMagic)))
	require.Equal(t, tx, block[1], "a tx listed by 2/3 of power leads the block")
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, c.process(ec, block))

	resp := c.finalize(block...)
	require.Len(t, resp.TxResults, 2)
	require.Zero(t, resp.TxResults[0].Code)
	require.Zero(t, resp.TxResults[1].Code, "the listed tx must execute: %s", resp.TxResults[1].Log)
}

func TestInclusion_proposalThatDropsAListedTxIsRejected(t *testing.T) {
	c := newInclusionChain(t, inclusionEnableHeight)
	tx := c.editTx(1, 1)
	other := c.editTx(2, 2)
	c.starve(tx)
	c.finalize()
	listed := c.extend()
	ec := c.extendedCommit([][]byte{listed, listed, listed})
	block := c.prepare(ec, other)
	require.Equal(t, [][]byte{block[0], tx, other}, block, "listed tx first, then the proposer's own")
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, c.process(ec, block))

	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(ec, [][]byte{block[0], other}), "dropping the listed tx")
	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(ec, [][]byte{block[0], other, tx}), "the proposer's tx ahead of the listed one")
	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(ec, block[1:]), "no injected commit")
	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(ec, nil), "an empty block")
	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(ec, append(append([][]byte{}, block...), block[0])), "a second injected commit")
}

func TestInclusion_badCommitIsRejected(t *testing.T) {
	c := newInclusionChain(t, inclusionEnableHeight)
	c.finalize()
	ec := c.extendedCommit([][]byte{nil, nil, nil})
	block := c.prepare(ec)
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, c.process(ec, block))

	forged := c.extendedCommit([][]byte{nil, nil, nil})
	forged.Votes[0].ExtensionSignature = bytes.Repeat([]byte{7}, 64)
	injected, err := app.EncodeInjectedCommitForTest(forged)
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(forged, [][]byte{injected}), "a bad extension signature")

	short := c.extendedCommit([][]byte{nil, nil, nil})
	short.Votes = short.Votes[:2]
	injected, err = app.EncodeInjectedCommitForTest(short)
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(ec, [][]byte{injected}), "a commit that drops a validator")

	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(ec, [][]byte{[]byte(app.InjectedCommitMagic + "garbage")}))
}

func TestInclusion_listedTxThatConflictsWithTheBlockIsNotRequired(t *testing.T) {
	c := newInclusionChain(t, inclusionEnableHeight)
	tx := c.editTx(1, 1)
	rival := c.editTx(1, 2) // same signer and sequence as tx.
	c.starve(tx)
	listed := c.extendAt(2) // voted against the state committed at height 1, where tx is valid.
	c.finalize(rival)       // height 2 spends that sequence with the rival.
	require.Equal(t, int64(2), c.height)

	ec := c.extendedCommit([][]byte{listed, listed, listed})
	block := c.prepare(ec)
	require.Len(t, block, 1, "tx is invalid against the state after height 2, so it is not required")
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, c.process(ec, block))
}

func TestInclusion_verifyVoteExtensionRejectsBadLists(t *testing.T) {
	c := newInclusionChain(t, inclusionEnableHeight)
	tx := c.editTx(1, 1)
	good, err := inclusion.EncodeList([][]byte{tx})
	require.NoError(t, err)

	c.verify(2, 0, nil, abci.ResponseVerifyVoteExtension_ACCEPT)
	c.verify(2, 0, good, abci.ResponseVerifyVoteExtension_ACCEPT)
	c.verify(2, 0, []byte("not a list"), abci.ResponseVerifyVoteExtension_REJECT)
	c.verify(2, 0, append(append([]byte{}, good...), 0), abci.ResponseVerifyVoteExtension_REJECT)

	junk, err := inclusion.EncodeList([][]byte{[]byte("not a transaction")})
	require.NoError(t, err)
	c.verify(2, 0, junk, abci.ResponseVerifyVoteExtension_REJECT)

	twice, err := inclusion.EncodeList([][]byte{tx, tx})
	require.NoError(t, err)
	c.verify(2, 0, twice, abci.ResponseVerifyVoteExtension_REJECT)

	huge := bytes.Repeat([]byte{9}, inclusion.DefaultListMaxBytes+1)
	oversize, err := inclusion.EncodeList([][]byte{huge})
	require.NoError(t, err)
	c.verify(2, 0, oversize, abci.ResponseVerifyVoteExtension_REJECT)
}

func TestInclusion_disabledBehavesAsBefore(t *testing.T) {
	for name, enable := range map[string]int64{"off": 0, "not yet": 50} {
		t.Run(name, func(t *testing.T) {
			c := newInclusionChain(t, enable)
			tx := c.editTx(1, 1)
			c.starve(tx)
			c.finalize()
			c.finalize()

			ec := abci.ExtendedCommitInfo{}
			block := c.prepare(ec, tx)
			require.Equal(t, [][]byte{tx}, block, "no injected commit, the mempool passes through")
			require.Equal(t, abci.ResponseProcessProposal_ACCEPT, c.process(ec, block))
			require.Equal(t, abci.ResponseProcessProposal_ACCEPT, c.process(ec, nil))
			injected := []byte(app.InjectedCommitMagic + "x")
			require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(ec, [][]byte{injected}))

			_, err := c.app.VerifyVoteExtension(&abci.RequestVerifyVoteExtension{Height: c.height, VoteExtension: nil})
			require.Error(t, err, "BaseApp refuses vote extension calls while they are disabled")
			_, err = c.app.ExtendVote(nil, &abci.RequestExtendVote{Height: c.height})
			require.Error(t, err)

			resp := c.finalize(block...)
			require.Len(t, resp.TxResults, 1)
			require.Zero(t, resp.TxResults[0].Code, resp.TxResults[0].Log)
		})
	}
}

// TestInclusion_severalBlocksThroughFinalizeBlock runs the full per-height
// cycle (extend, commit, prepare, process, finalize) for five heights, with a
// starved tx appearing at each, and checks each is in the very next block and
// that every block's first result is the injected commit's.
func TestInclusion_severalBlocksThroughFinalizeBlock(t *testing.T) {
	c := newInclusionChain(t, inclusionEnableHeight)
	c.finalize() // height 2, the first with extensions.
	for round := int64(0); round < 5; round++ {
		tx := c.editTx(int(round%3), 100+round)
		c.starve(tx)
		listed := c.extend()
		require.NotEmpty(t, listed)
		ec := c.extendedCommit([][]byte{listed, nil, listed})

		block := c.prepare(ec)
		require.Equal(t, abci.ResponseProcessProposal_ACCEPT, c.process(ec, block))
		require.Equal(t, tx, block[1], "round %d: the starved tx leads the next block", round)

		resp := c.finalize(block...)
		require.Len(t, resp.TxResults, len(block), "one result per block tx, including the injected commit")
		require.Zero(t, resp.TxResults[0].Code)
		require.Zero(t, resp.TxResults[1].Code, "round %d: %s", round, resp.TxResults[1].Log)
		require.Zero(t, c.app.InclusionPoolLen(), "included txs leave the seen-set")
	}
}

// A transaction that names a sender but carries a signature that does not
// verify is not required, and the walk never runs it through the ante chain.
func TestInclusion_forgedSignatureIsNotRequired(t *testing.T) {
	c := newInclusionChain(t, inclusionEnableHeight)
	real := c.editTx(1, 1)
	forged := append([]byte(nil), real...)
	forged[len(forged)-1] ^= 0xff // the signature is the last field of the tx.
	require.NotEqual(t, real, forged)

	c.finalize()
	list, err := inclusion.EncodeList([][]byte{forged})
	require.NoError(t, err)
	c.verify(2, 0, list, abci.ResponseVerifyVoteExtension_ACCEPT) // framing and fee are all VerifyVoteExtension can see.
	ec := c.extendedCommit([][]byte{list, list, list})

	block := c.prepare(ec)
	require.Len(t, block, 1, "only the injected commit: a forged signature is skipped, not required")
	require.Equal(t, abci.ResponseProcessProposal_ACCEPT, c.process(ec, block))
}

// An injected commit over the extension budget is refused by ProcessProposal
// and cannot be built by PrepareProposal, both deterministically.
func TestInclusion_oversizedInjectedCommit(t *testing.T) {
	c := newInclusionChain(t, inclusionEnableHeight)
	c.finalize()
	big := bytes.Repeat([]byte{1}, 10<<20) // past half the 20 MiB block budget.
	ec := c.extendedCommit([][]byte{big, nil, nil})
	injected, err := app.EncodeInjectedCommitForTest(ec)
	require.NoError(t, err)
	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(ec, [][]byte{injected}))

	resp, err := c.app.PrepareProposal(&abci.RequestPrepareProposal{
		Height: c.height + 1, Time: c.blockTime(), ProposerAddress: c.consAddr(0),
		MaxTxBytes: 22_020_096, LocalLastCommit: ec,
	})
	require.NoError(t, err)
	for _, tx := range resp.Txs {
		require.False(t, bytes.HasPrefix(tx, []byte(app.InjectedCommitMagic)), "an over-budget commit is not injected")
	}
}

// A commit that fits the budget but not the request's MaxTxBytes is not
// injected either; the fallback block is then refused by ProcessProposal.
func TestInclusion_injectedCommitOverMaxTxBytes(t *testing.T) {
	c := newInclusionChain(t, inclusionEnableHeight)
	c.finalize()
	ec := c.extendedCommit([][]byte{bytes.Repeat([]byte{1}, 64<<10), nil, nil})
	resp, err := c.app.PrepareProposal(&abci.RequestPrepareProposal{
		Height: c.height + 1, Time: c.blockTime(), ProposerAddress: c.consAddr(0),
		MaxTxBytes: 32 << 10, LocalLastCommit: ec,
	})
	require.NoError(t, err)
	for _, tx := range resp.Txs {
		require.False(t, bytes.HasPrefix(tx, []byte(app.InjectedCommitMagic)))
	}
	require.Equal(t, abci.ResponseProcessProposal_REJECT, c.process(ec, resp.Txs))
}
