package app

import (
	"fmt"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/baseapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"

	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

// inclusionHandlers connects x/inclusion to BaseApp: the four ABCI handlers
// for C13 inclusion lists. Before vote extensions are enabled, and at the
// enable height itself, they do nothing and PrepareProposal/ProcessProposal
// are exactly the SDK default handlers.
type inclusionHandlers struct {
	logger    log.Logger
	rules     inclusionTxRules
	accounts  authkeeper.AccountKeeper
	valStore  baseapp.ValidatorStore
	proposals *baseapp.DefaultProposalHandler
	pool      *inclusionPool
	now       func() time.Time
}

// voteExtensionActive reports whether validators extend votes at height:
// from the enable height on, like ExtendVote and VerifyVoteExtension.
func voteExtensionActive(ctx sdk.Context, height int64) bool {
	cp := ctx.ConsensusParams()
	return cp.Abci != nil && cp.Abci.VoteExtensionsEnableHeight != 0 && height >= cp.Abci.VoteExtensionsEnableHeight
}

// commitInjected reports whether the block at height carries the previous
// height's extended commit: from the height after the enable height, like
// baseapp.ValidateVoteExtensions.
func commitInjected(ctx sdk.Context, height int64) bool {
	cp := ctx.ConsensusParams()
	return cp.Abci != nil && cp.Abci.VoteExtensionsEnableHeight != 0 && height > cp.Abci.VoteExtensionsEnableHeight
}

// params returns the limits for a block whose injected commit is injectedLen
// bytes. Only the consensus block max bytes, which every node shares, moves
// the block budget.
func (h *inclusionHandlers) params(ctx sdk.Context, injectedLen int) inclusion.Params {
	p := inclusion.DefaultParams()
	if b := ctx.ConsensusParams().Block; b != nil && b.MaxBytes > 0 {
		p.MaxBlockBytes = int(b.MaxBytes)
	}
	p.MaxBlockBytes -= blockOverheadReserve + injectedLen
	if p.MaxBlockBytes < 1 {
		p.MaxBlockBytes = 1
	}
	return p
}

// listView is the stateless view: enough to validate and select lists.
// BaseFee 1 is the stateless floor (a listed transaction pays something); the
// real base fee is enforced by the ante chain in Admit.
func (h *inclusionHandlers) listView(p inclusion.Params) inclusion.View {
	return inclusion.View{
		BaseFee:       1,
		Params:        p,
		Decode:        h.rules.meta,
		Authenticated: true,
	}
}

// walkView is the view for the sequential walk over an included commit:
// account sequences from state, and the ante chain on a scratch branch of ctx.
func (h *inclusionHandlers) walkView(ctx sdk.Context, p inclusion.Params, height int64, ec abci.ExtendedCommitInfo, c inclusion.Commit, total int64) inclusion.View {
	v := h.listView(p)
	v.Height = height - 1
	v.Round = ec.Round
	v.TotalPower = total
	v.State = h.sequences(ctx, c)
	scratch, _ := ctx.CacheContext()
	v.Admit = h.rules.admitter(scratch)
	return v
}

func (h *inclusionHandlers) sequences(ctx sdk.Context, c inclusion.Commit) inclusion.State {
	next := make(map[string]uint64)
	for _, e := range c.Extensions {
		for _, raw := range e.Txs {
			meta, err := h.rules.meta(raw)
			if err != nil {
				continue
			}
			key := inclusion.SenderKey(meta.Sender)
			if _, ok := next[key]; ok {
				continue
			}
			if acc := h.accounts.GetAccount(ctx, sdk.AccAddress(meta.Sender)); acc != nil {
				next[key] = acc.GetSequence()
			}
		}
	}
	return inclusion.State{NextSequence: next}
}

// ExtendVote lists this node's long-waiting mempool transactions that pass
// the ante chain against the last committed state. It never fails: a node
// that cannot list sends an empty extension, which counts as an empty list.
func (h *inclusionHandlers) ExtendVote(ctx sdk.Context, req *abci.RequestExtendVote) (*abci.ResponseExtendVote, error) {
	empty := &abci.ResponseExtendVote{VoteExtension: []byte{}}
	if !voteExtensionActive(ctx, req.Height) {
		return empty, nil
	}
	inBlock := make(map[string]struct{}, len(req.Txs))
	for _, tx := range req.Txs {
		inBlock[string(tx)] = struct{}{}
	}
	var candidates [][]byte
	for _, raw := range h.pool.Eligible(h.now(), inclusionIncludeAfter) {
		if _, ok := inBlock[string(raw)]; !ok {
			candidates = append(candidates, raw)
		}
	}
	v := h.listView(inclusion.DefaultParams())
	scratch, _ := ctx.CacheContext()
	v.Admit = h.rules.admitter(scratch)
	list := inclusion.SelectList(v, candidates)
	if len(list) == 0 {
		return empty, nil
	}
	raw, err := inclusion.EncodeList(list)
	if err != nil {
		h.logger.Error("failed to encode the inclusion list", "height", req.Height, "err", err)
		return empty, nil
	}
	return &abci.ResponseExtendVote{VoteExtension: raw}, nil
}

// VerifyVoteExtension accepts an extension that decodes and passes the
// stateless list rules, and rejects anything else. It reads no state, so
// every validator gives the same answer.
func (h *inclusionHandlers) VerifyVoteExtension(ctx sdk.Context, req *abci.RequestVerifyVoteExtension) (*abci.ResponseVerifyVoteExtension, error) {
	reject := &abci.ResponseVerifyVoteExtension{Status: abci.ResponseVerifyVoteExtension_REJECT}
	accept := &abci.ResponseVerifyVoteExtension{Status: abci.ResponseVerifyVoteExtension_ACCEPT}
	if !voteExtensionActive(ctx, req.Height) {
		if len(req.VoteExtension) > 0 {
			return reject, nil
		}
		return accept, nil
	}
	txs, err := inclusion.DecodeList(req.VoteExtension)
	if err != nil {
		return reject, nil
	}
	if err := inclusion.ValidateList(h.listView(inclusion.DefaultParams()), txs); err != nil {
		return reject, nil
	}
	return accept, nil
}

// PrepareProposal puts the previous height's extended commit first, then the
// listed transactions it requires, then whatever the SDK default handler
// selects from the remaining space. If the commit cannot be turned into a
// valid block (under 2/3 valid extension power, say) it returns an error, and
// BaseApp falls back to the raw request transactions, which ProcessProposal
// then rejects.
func (h *inclusionHandlers) PrepareProposal(ctx sdk.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
	def := h.proposals.PrepareProposalHandler()
	if !commitInjected(ctx, req.Height) {
		return def(ctx, req)
	}
	if err := baseapp.ValidateVoteExtensions(ctx, h.valStore, req.Height, "", req.LocalLastCommit); err != nil {
		return nil, fmt.Errorf("local extended commit does not validate: %w", err)
	}
	injected, err := encodeInjectedCommit(req.LocalLastCommit)
	if err != nil {
		return nil, err
	}
	p := h.params(ctx, len(injected))
	commit, total := commitOf(h.listView(p), req.Height, req.LocalLastCommit)
	prefix, err := inclusion.Assemble(h.walkView(ctx, p, req.Height, req.LocalLastCommit, commit, total), commit, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to assemble the listed transactions: %w", err)
	}

	block := append([][]byte{injected}, prefix...)
	listed := make(map[string]struct{}, len(prefix))
	for _, tx := range prefix {
		listed[string(tx)] = struct{}{}
	}
	rest := *req
	rest.MaxTxBytes = req.MaxTxBytes - protoSize(block...)
	rest.Txs = nil
	for _, tx := range req.Txs {
		if _, dup := listed[string(tx)]; !dup && !isInjectedCommit(tx) {
			rest.Txs = append(rest.Txs, tx)
		}
	}
	if rest.MaxTxBytes <= 0 || len(rest.Txs) == 0 {
		return &abci.ResponsePrepareProposal{Txs: block}, nil
	}
	resp, err := def(ctx, &rest)
	if err != nil {
		return nil, err
	}
	return &abci.ResponsePrepareProposal{Txs: append(block, resp.Txs...)}, nil
}

// ProcessProposal rejects a block that lacks a valid injected commit, whose
// commit fails signature or power checks, or that omits or misorders a valid
// listed transaction. Everything it reads is in the block or in state, so
// every validator reaches the same verdict. The remaining transactions go to
// the SDK default handler.
func (h *inclusionHandlers) ProcessProposal(ctx sdk.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
	def := h.proposals.ProcessProposalHandler()
	reject := &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}
	if !commitInjected(ctx, req.Height) {
		for _, tx := range req.Txs {
			if isInjectedCommit(tx) {
				return reject, nil
			}
		}
		return def(ctx, req)
	}
	if len(req.Txs) == 0 {
		return reject, nil
	}
	ec, err := decodeInjectedCommit(req.Txs[0])
	if err != nil {
		return reject, nil
	}
	if err := baseapp.ValidateVoteExtensions(ctx, h.valStore, req.Height, "", ec); err != nil {
		return reject, nil
	}
	block := req.Txs[1:]
	for _, tx := range block {
		if isInjectedCommit(tx) {
			return reject, nil
		}
	}
	p := h.params(ctx, len(req.Txs[0]))
	commit, total := commitOf(h.listView(p), req.Height, ec)
	if err := inclusion.Process(h.walkView(ctx, p, req.Height, ec, commit, total), commit, block); err != nil {
		return reject, nil
	}
	rest := *req
	rest.Txs = block
	return def(ctx, &rest)
}
