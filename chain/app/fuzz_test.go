package app_test

import (
	"bytes"
	"context"
	"sort"
	"sync/atomic"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	protoio "github.com/cosmos/gogoproto/io"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

// panicLogger counts the "panic recovered" lines BaseApp logs. BaseApp turns a panic inside a
// proposal or vote-extension handler into a REJECT or an error, which hides the bug from a test
// that only reads the answer; the fuzz targets read this count too.
type panicLogger struct {
	log.Logger
	panics *atomic.Int64
}

func (l panicLogger) With(keyVals ...any) log.Logger {
	return panicLogger{Logger: l.Logger.With(keyVals...), panics: l.panics}
}

func (l panicLogger) note(msg string) {
	if len(msg) >= 15 && msg[:15] == "panic recovered" {
		l.panics.Add(1)
	}
}

func (l panicLogger) Error(msg string, keyVals ...any) {
	l.note(msg)
	l.Logger.Error(msg, keyVals...)
}

func (l panicLogger) ErrorContext(ctx context.Context, msg string, keyVals ...any) {
	l.note(msg)
	l.Logger.ErrorContext(ctx, msg, keyVals...)
}

// buildWatchedApp is buildTestApp with a logger that counts recovered panics.
func buildWatchedApp(tb testing.TB) (*app.OramaApp, *atomic.Int64) {
	tb.Helper()
	app.SetAddressPrefixes()
	panics := new(atomic.Int64)
	logger := panicLogger{Logger: log.NewNopLogger(), panics: panics}
	a := app.NewOramaApp(logger, dbm.NewMemDB(), true, simtestutil.EmptyAppOptions{}, baseapp.SetChainID(testChainID))
	tb.Cleanup(func() { _ = a.Close() })
	return a, panics
}

// fuzzChain is the three-member inclusion chain at height 2, with a starved transaction listed by
// two of the three validators: the state in which a real proposal carries the injected commit and
// a required transaction.
type fuzzChain struct {
	*inclusionChain
	panics *atomic.Int64
	ec     abci.ExtendedCommitInfo
	block  [][]byte
	signed []byte
}

func newFuzzChain(tb testing.TB) *fuzzChain {
	tb.Helper()
	a, panics := buildWatchedApp(tb)
	c := newInclusionChainOn(tb, a, inclusionEnableHeight)
	tx := c.editTx(1, 1)
	c.starve(tx)
	c.finalize()
	listed := c.extend()
	ec := c.extendedCommit([][]byte{listed, listed, nil})
	block := c.prepare(ec)
	if len(block) != 2 {
		tb.Fatalf("the seed block has %d transactions, want the injected commit and one listed", len(block))
	}
	return &fuzzChain{inclusionChain: c, panics: panics, ec: ec, block: block, signed: tx}
}

// FuzzProcessProposal gives ProcessProposal arbitrary blocks at a height where the inclusion rules
// are on. The handler never fails or panics, answers the same twice, and accepts only a block whose
// first transaction is a canonically encoded injected commit and whose other transactions all
// decode as chain transactions.
func FuzzProcessProposal(f *testing.F) {
	c := newFuzzChain(f)
	f.Add(c.block[0], c.block[1], []byte(nil), uint8(2))
	f.Add(c.block[0], []byte(nil), []byte(nil), uint8(1))
	f.Add(c.block[0], c.block[1], c.block[1], uint8(3))
	f.Add(c.block[1], c.block[0], []byte(nil), uint8(2))
	f.Add([]byte(app.InjectedCommitMagic), []byte(nil), []byte(nil), uint8(1))
	f.Add([]byte{}, []byte{}, []byte{}, uint8(0))
	f.Fuzz(func(t *testing.T, a, b, d []byte, n uint8) {
		txs := [][]byte{a, b, d}[:int(n)%4]
		req := &abci.RequestProcessProposal{
			Height: c.height + 1, Time: c.blockTime(), ProposerAddress: c.consAddr(0),
			Txs: txs, ProposedLastCommit: commitInfoOf(c.ec), Hash: bytes.Repeat([]byte{2}, 32),
		}
		before := c.panics.Load()
		first, err := c.app.ProcessProposal(req)
		if err != nil {
			t.Fatalf("ProcessProposal failed: %v", err)
		}
		second, err := c.app.ProcessProposal(req)
		if err != nil || second.Status != first.Status {
			t.Fatalf("ProcessProposal is not deterministic: %v, %v (%v)", first.Status, second.Status, err)
		}
		if c.panics.Load() != before {
			t.Fatal("ProcessProposal panicked (BaseApp recovered it)")
		}
		if first.Status != abci.ResponseProcessProposal_ACCEPT {
			return
		}
		if len(txs) == 0 {
			t.Fatal("an empty block was accepted with the inclusion rules on")
		}
		if _, err := app.DecodeInjectedCommitForTest(txs[0]); err != nil {
			t.Fatalf("accepted a block whose first transaction is not a canonical injected commit: %v", err)
		}
		for i, tx := range txs[1:] {
			if _, err := c.app.TxConfig().TxDecoder()(tx); err != nil {
				t.Fatalf("accepted a block with an undecodable transaction %d: %v", i+1, err)
			}
		}
	})
}

// signedCommit is inclusionChain.extendedCommit for use inside a fuzz function, where only the inner
// *testing.T may be used: every validator signs the extension the way CometBFT signs a vote extension.
func (c *fuzzChain) signedCommit(t *testing.T, exts [][]byte) abci.ExtendedCommitInfo {
	t.Helper()
	votes := make([]abci.ExtendedVoteInfo, len(c.keys))
	for i, k := range c.keys {
		var buf bytes.Buffer
		cve := cmtproto.CanonicalVoteExtension{Extension: exts[i], Height: c.height, Round: 0, ChainId: testChainID}
		if err := protoio.NewDelimitedWriter(&buf).WriteMsg(&cve); err != nil {
			t.Fatal(err)
		}
		sig, err := k.cons.Sign(buf.Bytes())
		if err != nil {
			t.Fatal(err)
		}
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

// FuzzPrepareProposal gives PrepareProposal a properly signed extended commit whose vote extensions
// are arbitrary bytes, and an arbitrary mempool. It never fails or panics, and the block it builds
// is one every other validator accepts: what a proposer assembles from a commit is exactly what
// ProcessProposal demands of it, whatever the lists in the commit hold.
func FuzzPrepareProposal(f *testing.F) {
	c := newFuzzChain(f)
	listed, err := inclusion.EncodeList([][]byte{c.signed})
	if err != nil {
		f.Fatal(err)
	}
	noFee, err := inclusion.EncodeList([][]byte{txWithoutAFee(f)})
	if err != nil {
		f.Fatal(err)
	}
	noAmount, err := inclusion.EncodeList([][]byte{txWithAnAmountlessFeeCoin(f)})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(listed, listed, []byte(nil), c.signed, []byte(nil))
	f.Add(listed, noFee, noAmount, []byte(nil), c.signed)
	f.Add([]byte(nil), []byte(nil), []byte(nil), []byte{1, 2, 3}, c.signed)
	f.Fuzz(func(t *testing.T, e0, e1, e2, m0, m1 []byte) {
		ec := c.signedCommit(t, [][]byte{e0, e1, e2})
		var mempool [][]byte
		for _, tx := range [][]byte{m0, m1} {
			if len(tx) > 0 {
				mempool = append(mempool, tx)
			}
		}
		before := c.panics.Load()
		resp, err := c.app.PrepareProposal(&abci.RequestPrepareProposal{
			Height: c.height + 1, Time: c.blockTime(), ProposerAddress: c.consAddr(0),
			MaxTxBytes: inclusionMaxTxBytes, Txs: mempool, LocalLastCommit: ec,
		})
		if err != nil {
			t.Fatalf("PrepareProposal failed: %v", err)
		}
		if c.panics.Load() != before {
			t.Fatal("PrepareProposal panicked (BaseApp recovered it)")
		}
		if len(resp.Txs) == 0 || !bytes.HasPrefix(resp.Txs[0], []byte(app.InjectedCommitMagic)) {
			return
		}
		verdict, err := c.app.ProcessProposal(&abci.RequestProcessProposal{
			Height: c.height + 1, Time: c.blockTime(), ProposerAddress: c.consAddr(0),
			Txs: resp.Txs, ProposedLastCommit: commitInfoOf(ec), Hash: bytes.Repeat([]byte{2}, 32),
		})
		if err != nil || verdict.Status != abci.ResponseProcessProposal_ACCEPT {
			t.Fatalf("a block PrepareProposal built is refused by ProcessProposal: %v, %v", verdict, err)
		}
		if c.panics.Load() != before {
			t.Fatal("ProcessProposal panicked on the block PrepareProposal built")
		}
	})
}

// FuzzVerifyVoteExtension gives VerifyVoteExtension arbitrary extension bytes. It never fails or
// panics, answers the same twice, and accepts only an extension DecodeList reads whose list passes
// ValidateList: every validator must reach the same verdict on the same bytes.
func FuzzVerifyVoteExtension(f *testing.F) {
	c := newFuzzChain(f)
	good, err := inclusion.EncodeList([][]byte{c.signed})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	noFee, err := inclusion.EncodeList([][]byte{txWithoutAFee(f)})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(noFee)
	noAmount, err := inclusion.EncodeList([][]byte{txWithAnAmountlessFeeCoin(f)})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(noAmount)
	f.Add([]byte(nil))
	f.Add(c.extend())
	f.Add(append(bytes.Clone(good), 0))
	f.Add([]byte("ORIL\x01\x00\x00\x00\x01\x00\x00\x00\x01x"))
	f.Fuzz(func(t *testing.T, ext []byte) {
		req := &abci.RequestVerifyVoteExtension{
			Height: c.height, Hash: bytes.Repeat([]byte{1}, 32), ValidatorAddress: c.consAddr(0), VoteExtension: ext,
		}
		before := c.panics.Load()
		first, err := c.app.VerifyVoteExtension(req)
		if err != nil {
			t.Fatalf("VerifyVoteExtension failed: %v", err)
		}
		second, err := c.app.VerifyVoteExtension(req)
		if err != nil || second.Status != first.Status {
			t.Fatalf("VerifyVoteExtension is not deterministic (%v)", err)
		}
		if c.panics.Load() != before {
			t.Fatal("VerifyVoteExtension panicked (BaseApp recovered it)")
		}
		if first.Status == abci.ResponseVerifyVoteExtension_ACCEPT {
			if _, err := inclusion.DecodeList(ext); err != nil {
				t.Fatalf("accepted an extension DecodeList refuses: %v", err)
			}
		}
	})
}

// FuzzAnteTxDecode runs arbitrary transaction bytes through the SDK decoder the ante chain, the
// mempool and the indexer share. It never panics, and a transaction it accepts re-encodes to the
// very same bytes (when it carries a message): two encodings of one transaction would be two
// hashes for one effect.
func FuzzAnteTxDecode(f *testing.F) {
	c := newFuzzChain(f)
	f.Add(c.signed)
	f.Add(c.editTx(2, 9))
	f.Add([]byte{})
	f.Add([]byte{0x0a, 0x00})
	f.Fuzz(func(t *testing.T, raw []byte) {
		cfg := c.app.TxConfig()
		tx, err := cfg.TxDecoder()(raw)
		if err != nil {
			return
		}
		// SDK decoding accepts a TxRaw field written out empty, which encodes back to nothing; only
		// a transaction with no message at all can differ that way, and none of those can execute.
		if len(tx.GetMsgs()) == 0 {
			return
		}
		back, err := cfg.TxEncoder()(tx)
		if err != nil || !bytes.Equal(back, raw) {
			t.Fatalf("a decoded transaction does not re-encode to its bytes (%v): %x vs %x", err, raw, back)
		}
	})
}

// statesAFee reports whether raw's auth info carries a fee with an amount on every coin. The SDK
// decodes a transaction without one, and the ante chain's first decorator then dereferences the
// missing field: BaseApp recovers that panic per transaction and answers the panic code, in CheckTx
// and in a block alike, so the verdict is the same on every node and no state is touched. It is the
// SDK's own behaviour and changing it would change the result code of such a transaction in a block,
// so FuzzCheckTx tolerates it for exactly those transactions and for no other.
func statesAFee(raw []byte) bool {
	var txRaw txtypes.TxRaw
	if txRaw.Unmarshal(raw) != nil {
		return false
	}
	var auth txtypes.AuthInfo
	if auth.Unmarshal(txRaw.AuthInfoBytes) != nil || auth.Fee == nil {
		return false
	}
	for _, coin := range auth.Fee.Amount {
		if coin.Amount.IsNil() {
			return false
		}
	}
	return true
}

// FuzzCheckTx sends arbitrary bytes to CheckTx, the front door of the ante chain. It never fails, a
// transaction that was refused for its bytes is refused again for the same bytes, and a transaction
// that states a fee never panics inside the ante chain, whatever else is wrong with it (one that
// states none panics in the SDK's first decorator, recovered as described at statesAFee).
func FuzzCheckTx(f *testing.F) {
	c := newFuzzChain(f)
	f.Add(c.signed)
	f.Add(c.editTx(2, 9))
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xff}, 64))
	f.Fuzz(func(t *testing.T, raw []byte) {
		before := c.panics.Load()
		res, err := c.app.CheckTx(&abci.RequestCheckTx{Tx: raw, Type: abci.CheckTxType_New})
		if err != nil {
			t.Fatalf("CheckTx failed: %v", err)
		}
		if c.panics.Load() != before && statesAFee(raw) {
			t.Fatal("CheckTx panicked on a transaction that states a fee")
		}
		if res.Code != 0 {
			again, err := c.app.CheckTx(&abci.RequestCheckTx{Tx: raw, Type: abci.CheckTxType_New})
			if err != nil || again.Code == 0 {
				t.Fatalf("a refused transaction was accepted the second time (%v)", err)
			}
		}
	})
}
