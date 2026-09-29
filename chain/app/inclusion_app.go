package app

import (
	"time"

	abci "github.com/cometbft/cometbft/abci/types"

	"github.com/cosmos/cosmos-sdk/baseapp"
)

// injectedCommitLog is the log line on the result of the injected commit
// transaction, which FinalizeBlock does not execute.
const injectedCommitLog = "inclusion-list extended commit"

// setInclusionHandlers registers the C13 handlers. They compose with the SDK
// default proposal handler, which keeps doing the mempool selection.
func (app *OramaApp) setInclusionHandlers() {
	pool := newInclusionPool(inclusionPoolMaxBytes, inclusionPoolMaxAge)
	h := &inclusionHandlers{
		logger: app.Logger(),
		rules: inclusionTxRules{
			logger: app.Logger(),
			decode: app.txConfig.TxDecoder(),
			ante:   app.anteHandler,

			accounts:  app.AccountKeeper,
			signModes: app.txConfig.SignModeHandler(),
		},
		accounts:  app.AccountKeeper,
		staking:   app.StakingKeeper,
		valStore:  app.StakingKeeper,
		proposals: baseapp.NewDefaultProposalHandler(app.Mempool(), app.BaseApp),
		pool:      pool,
		now:       time.Now,
	}
	app.inclusion = h
	app.SetExtendVoteHandler(h.ExtendVote)
	app.SetVerifyVoteExtensionHandler(h.VerifyVoteExtension)
	app.SetPrepareProposal(h.PrepareProposal)
	app.SetProcessProposal(h.ProcessProposal)
}

// CheckTx runs BaseApp.CheckTx and records a transaction it accepts, or drops
// one that no longer passes a recheck, in the seen-set ExtendVote lists from.
func (app *OramaApp) CheckTx(req *abci.RequestCheckTx) (*abci.ResponseCheckTx, error) {
	res, err := app.BaseApp.CheckTx(req)
	if err != nil || res == nil {
		return res, err
	}
	switch {
	case res.Code == 0:
		app.inclusion.pool.Add(req.Tx, app.inclusion.now())
	case req.Type == abci.CheckTxType_Recheck:
		app.inclusion.pool.Remove(req.Tx)
	}
	return res, nil
}

// FinalizeBlock strips the injected extended commit before BaseApp sees the
// block, so it never reaches the ante handler or a message handler, and puts
// back a successful empty result in its place: CometBFT needs one result per
// transaction in the block. It also forgets every included transaction.
//
// Optimistic execution calls BaseApp's finalize path directly and would skip
// this, so it must stay off for a chain that uses inclusion lists.
func (app *OramaApp) FinalizeBlock(req *abci.RequestFinalizeBlock) (*abci.ResponseFinalizeBlock, error) {
	app.inclusion.pool.Remove(req.Txs...)
	if len(req.Txs) == 0 || !isInjectedCommit(req.Txs[0]) {
		return app.BaseApp.FinalizeBlock(req)
	}
	stripped := *req
	stripped.Txs = req.Txs[1:]
	res, err := app.BaseApp.FinalizeBlock(&stripped)
	if err != nil || res == nil {
		return res, err
	}
	res.TxResults = append([]*abci.ExecTxResult{{Code: abci.CodeTypeOK, Log: injectedCommitLog}}, res.TxResults...)
	return res, nil
}
