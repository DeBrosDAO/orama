package app

import (
	"bytes"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

func TestInclusionPool_eligibleAfterTheDelay(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newInclusionPool(1024, time.Hour)
	require.True(t, p.Add([]byte("a"), now))
	require.True(t, p.Add([]byte("b"), now.Add(8*time.Second)))
	require.True(t, p.Add([]byte("a"), now.Add(9*time.Second)), "a repeat keeps its first-seen time")

	got := p.Eligible(now.Add(10*time.Second), 10*time.Second)
	require.Equal(t, [][]byte{[]byte("a")}, got)
	require.Len(t, p.Eligible(now.Add(20*time.Second), 10*time.Second), 2)
	require.Empty(t, p.Eligible(now.Add(5*time.Second), 10*time.Second))
}

func TestInclusionPool_removeAndBounds(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newInclusionPool(10, time.Minute)
	require.False(t, p.Add(nil, now), "an empty tx is not tracked")
	require.False(t, p.Add(make([]byte, inclusion.DefaultListMaxBytes+1), now), "a tx too large to list is not tracked")
	require.True(t, p.Add(bytes.Repeat([]byte{1}, 6), now))
	require.False(t, p.Add(bytes.Repeat([]byte{2}, 6), now), "the byte bound stops tracking")
	require.Equal(t, 1, p.Len())

	p.Remove(bytes.Repeat([]byte{1}, 6), []byte("never added"))
	require.Zero(t, p.Len())
	require.True(t, p.Add(bytes.Repeat([]byte{2}, 6), now), "removal frees the bytes")
}

func TestInclusionPool_expiresOldEntries(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newInclusionPool(1024, time.Minute)
	require.True(t, p.Add([]byte("old"), now))
	require.Empty(t, p.Eligible(now.Add(2*time.Minute), 0))
	require.Zero(t, p.Len(), "an entry past the max age is dropped")
}

func TestInjectedCommit_roundTripAndRejects(t *testing.T) {
	ec := abci.ExtendedCommitInfo{Round: 3, Votes: []abci.ExtendedVoteInfo{{
		Validator:     abci.Validator{Address: []byte("addr"), Power: 5},
		VoteExtension: []byte("ext"),
		BlockIdFlag:   cmtproto.BlockIDFlagCommit,
	}}}
	tx, err := encodeInjectedCommit(ec)
	require.NoError(t, err)
	require.True(t, isInjectedCommit(tx))

	got, err := decodeInjectedCommit(tx)
	require.NoError(t, err)
	require.Equal(t, ec.Round, got.Round)
	require.Equal(t, ec.Votes[0].VoteExtension, got.Votes[0].VoteExtension)

	_, err = decodeInjectedCommit([]byte("plain tx bytes"))
	require.Error(t, err)
	_, err = decodeInjectedCommit([]byte(injectedCommitMagic + "\xff\xff\xff"))
	require.Error(t, err, "garbage after the magic")
	_, err = decodeInjectedCommit(append(append([]byte{}, tx...), 0x00))
	require.Error(t, err, "a trailing byte is not canonical")

	empty, err := encodeInjectedCommit(abci.ExtendedCommitInfo{})
	require.NoError(t, err)
	_, err = decodeInjectedCommit(empty)
	require.NoError(t, err, "an empty commit is a legal encoding")
}

func TestInjectedCommit_neverDecodesAsAChainTx(t *testing.T) {
	tx, err := encodeInjectedCommit(abci.ExtendedCommitInfo{Round: 1})
	require.NoError(t, err)
	app := newBareApp(t)
	_, err = app.txConfig.TxDecoder()(tx)
	require.Error(t, err)
}

func TestCommitOf_dropsBadExtensionsButCountsTheirPower(t *testing.T) {
	sender := []byte("sender")
	good, err := inclusion.EncodeTx(sender, 0, 5, []byte("p"))
	require.NoError(t, err)
	goodExt, err := inclusion.EncodeList([][]byte{good})
	require.NoError(t, err)
	junkExt, err := inclusion.EncodeList([][]byte{[]byte("junk")})
	require.NoError(t, err)

	view := inclusion.View{BaseFee: 1, Params: inclusion.DefaultParams(), Authenticated: true}
	ec := abci.ExtendedCommitInfo{Round: 2, Votes: []abci.ExtendedVoteInfo{
		{Validator: abci.Validator{Address: []byte("v1"), Power: 10}, VoteExtension: goodExt, BlockIdFlag: cmtproto.BlockIDFlagCommit},
		{Validator: abci.Validator{Address: []byte("v2"), Power: 20}, VoteExtension: junkExt, BlockIdFlag: cmtproto.BlockIDFlagCommit},
		{Validator: abci.Validator{Address: []byte("v3"), Power: 30}, VoteExtension: []byte("garbage"), BlockIdFlag: cmtproto.BlockIDFlagCommit},
		{Validator: abci.Validator{Address: []byte("v4"), Power: 40}, BlockIdFlag: cmtproto.BlockIDFlagNil},
		{Validator: abci.Validator{Address: []byte("v5"), Power: 50}, BlockIdFlag: cmtproto.BlockIDFlagCommit},
	}}
	c, total := commitOf(view, 7, ec)
	require.Equal(t, int64(150), total)
	require.Len(t, c.Extensions, 2, "the valid list and the empty one")
	require.Equal(t, int64(6), c.Extensions[0].Height)
	require.Equal(t, int32(2), c.Extensions[0].Round)
	require.Equal(t, [][]byte{good}, c.Extensions[0].Txs)
	require.Empty(t, c.Extensions[1].Txs, "an empty extension is an empty list")
}

func TestInclusionHandlers_paramsBudget(t *testing.T) {
	h := &inclusionHandlers{}
	ctx := bareContext(t, 22_020_096)
	p := h.params(ctx, 1000)
	require.Equal(t, 22_020_096-blockOverheadReserve-1000, p.MaxBlockBytes)
	require.NoError(t, p.Validate())

	small := h.params(bareContext(t, 100), 50)
	require.Equal(t, 1, small.MaxBlockBytes, "a block smaller than the reserve fits nothing but stays valid")
	require.NoError(t, small.Validate())

	unlimited := h.params(bareContext(t, -1), 0)
	require.Equal(t, inclusion.DefaultMaxBlockBytes-blockOverheadReserve, unlimited.MaxBlockBytes)
}

func newBareApp(t *testing.T) *OramaApp {
	t.Helper()
	SetAddressPrefixes()
	return NewOramaApp(log.NewNopLogger(), dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{}, baseapp.SetChainID("orama-localnet-unit-1"))
}

func bareContext(t *testing.T, maxBytes int64) sdk.Context {
	t.Helper()
	return sdk.Context{}.WithConsensusParams(cmtproto.ConsensusParams{Block: &cmtproto.BlockParams{MaxBytes: maxBytes}})
}

func TestInclusionHandlers_inertWhenDisabled(t *testing.T) {
	h := &inclusionHandlers{pool: newInclusionPool(1024, time.Hour), now: time.Now}
	require.True(t, h.pool.Add([]byte("tx"), time.Unix(0, 0)))
	ctx := bareContext(t, 22_020_096) // no ABCI params: extensions off.

	ext, err := h.ExtendVote(ctx, &abci.RequestExtendVote{Height: 5})
	require.NoError(t, err)
	require.Empty(t, ext.VoteExtension)

	ok, err := h.VerifyVoteExtension(ctx, &abci.RequestVerifyVoteExtension{Height: 5})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseVerifyVoteExtension_ACCEPT, ok.Status)
	bad, err := h.VerifyVoteExtension(ctx, &abci.RequestVerifyVoteExtension{Height: 5, VoteExtension: []byte("x")})
	require.NoError(t, err)
	require.Equal(t, abci.ResponseVerifyVoteExtension_REJECT, bad.Status)

	require.False(t, commitInjected(ctx, 5))
	enabled := ctx.WithConsensusParams(cmtproto.ConsensusParams{Abci: &cmtproto.ABCIParams{VoteExtensionsEnableHeight: 5}})
	require.False(t, commitInjected(enabled, 5), "the enable height itself carries no commit")
	require.True(t, commitInjected(enabled, 6))
	require.True(t, voteExtensionActive(enabled, 5))
	require.False(t, voteExtensionActive(enabled, 4))
}
