package app

import (
	"bytes"
	"fmt"
	"math"

	"cosmossdk.io/log/v2"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	txsigning "github.com/cosmos/cosmos-sdk/x/tx/signing"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

// inclusionTxRules adapts real chain transactions to x/inclusion.
type inclusionTxRules struct {
	logger    log.Logger
	decode    sdk.TxDecoder
	ante      sdk.AnteHandler
	accounts  authkeeper.AccountKeeper
	signModes *txsigning.HandlerMap
}

// meta reads the fields the inclusion rules need. Only an ordered
// transaction with exactly one signer can be listed: the rules track one
// sender and one sequence per transaction. Fee is the norama fee in whole
// units, saturating at MaxUint64.
func (r inclusionTxRules) meta(raw []byte) (meta inclusion.Meta, err error) {
	// The SDK's transaction types are written for transactions that went through the ante chain, and
	// this reads bytes a peer chose. statesAFee covers the panics known today; this keeps a further
	// one (the same input panics the same way on every node, so the verdict is the same everywhere)
	// from reaching CometBFT as an error from VerifyVoteExtension, which halts the node. The ante
	// chain's admitter and verifier below guard their SDK calls the same way.
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error("reading a listed transaction panicked", "panic", rec)
			meta, err = inclusion.Meta{}, fmt.Errorf("inclusion: the transaction cannot be read")
		}
	}()
	tx, err := r.decode(raw)
	if err != nil {
		return inclusion.Meta{}, fmt.Errorf("inclusion: transaction does not decode: %w", err)
	}
	if !statesAFee(raw) {
		return inclusion.Meta{}, fmt.Errorf("inclusion: the transaction states no fee, or a fee coin without an amount")
	}
	if u, ok := tx.(sdk.TxWithUnordered); ok && u.GetUnordered() {
		return inclusion.Meta{}, fmt.Errorf("inclusion: unordered transactions cannot be listed")
	}
	sigTx, ok := tx.(authsigning.SigVerifiableTx)
	if !ok {
		return inclusion.Meta{}, fmt.Errorf("inclusion: transaction carries no signatures")
	}
	signers, err := sigTx.GetSigners()
	if err != nil || len(signers) != 1 {
		return inclusion.Meta{}, fmt.Errorf("inclusion: a listed transaction needs exactly one signer")
	}
	sigs, err := sigTx.GetSignaturesV2()
	if err != nil || len(sigs) != 1 {
		return inclusion.Meta{}, fmt.Errorf("inclusion: a listed transaction needs exactly one signature")
	}
	feeTx, ok := tx.(sdk.FeeTx)
	if !ok {
		return inclusion.Meta{}, fmt.Errorf("inclusion: transaction carries no fee")
	}
	fee := feeTx.GetFee().AmountOf(params.BaseDenom)
	feeAmount := uint64(math.MaxUint64)
	if fee.IsUint64() {
		feeAmount = fee.Uint64()
	}
	return inclusion.Meta{Sender: signers[0], Sequence: sigs[0].Sequence, Fee: feeAmount}, nil
}

// statesAFee reports whether raw's auth info carries a fee and every coin of it an amount. The SDK
// decoder accepts an auth info without a fee, and the decoded transaction's GetFee and GetGas then
// dereference the missing field; it accepts a fee coin without an amount too, whose Int is nil and
// panics when read. Inside the ante chain those panics are recovered per transaction; here, in the
// checks every validator runs on another validator's vote extension, a panic would reach CometBFT as
// an error from VerifyVoteExtension, which halts the node.
func statesAFee(raw []byte) bool {
	var txRaw txtypes.TxRaw
	if err := txRaw.Unmarshal(raw); err != nil {
		return false
	}
	var auth txtypes.AuthInfo
	if err := auth.Unmarshal(txRaw.AuthInfoBytes); err != nil || auth.Fee == nil {
		return false
	}
	for _, coin := range auth.Fee.Amount {
		if coin.Amount.IsNil() {
			return false
		}
	}
	return true
}

// admitter returns an inclusion.View.Admit that runs the full ante chain on
// scratch, one transaction at a time. A transaction that passes has its state
// changes kept in scratch, so the next one is judged after it; one that fails
// leaves no trace. scratch must be a branch that is never written back.
func (r inclusionTxRules) admitter(scratch sdk.Context) func([]byte, inclusion.Meta) bool {
	return func(raw []byte, _ inclusion.Meta) (ok bool) {
		defer func() {
			if rec := recover(); rec != nil {
				r.logger.Error("ante handler panicked on a listed transaction", "panic", rec)
				ok = false
			}
		}()
		tx, err := r.decode(raw)
		if err != nil {
			return false
		}
		txCtx, write := scratch.CacheContext()
		txCtx = txCtx.
			WithTxBytes(raw).
			WithEventManager(sdk.NewEventManager()).
			WithGasMeter(storetypes.NewInfiniteGasMeter())
		if _, err := r.ante(txCtx, tx, false); err != nil {
			return false
		}
		write()
		return true
	}
}

// verifier returns an inclusion.View.Verify that checks a listed transaction's
// signature against its sender's account, the same check the ante chain's
// signature step makes, without running the rest of the chain. The walk runs
// it before a transaction is charged to its claimed sender, so a transaction
// that only names a sender it cannot sign for spends neither that sender's
// budget nor an ante attempt. It reads state and writes none.
func (r inclusionTxRules) verifier(ctx sdk.Context) func([]byte, inclusion.Meta) bool {
	return func(raw []byte, meta inclusion.Meta) (ok bool) {
		defer func() {
			if rec := recover(); rec != nil {
				r.logger.Error("signature check panicked on a listed transaction", "panic", rec)
				ok = false
			}
		}()
		tx, err := r.decode(raw)
		if err != nil {
			return false
		}
		sigTx, isSig := tx.(authsigning.SigVerifiableTx)
		adaptable, isAdaptable := tx.(authsigning.V2AdaptableTx)
		if !isSig || !isAdaptable {
			return false
		}
		sigs, err := sigTx.GetSignaturesV2()
		if err != nil || len(sigs) != 1 {
			return false
		}
		acc := r.accounts.GetAccount(ctx, sdk.AccAddress(meta.Sender))
		if acc == nil {
			return false
		}
		pubKey := acc.GetPubKey()
		if pubKey == nil {
			pubKey = sigs[0].PubKey
		}
		if pubKey == nil || !bytes.Equal(pubKey.Address(), meta.Sender) {
			return false
		}
		anyPk, err := codectypes.NewAnyWithValue(pubKey)
		if err != nil {
			return false
		}
		signerData := txsigning.SignerData{
			Address:       acc.GetAddress().String(),
			ChainID:       ctx.ChainID(),
			AccountNumber: acc.GetAccountNumber(),
			Sequence:      sigs[0].Sequence,
			PubKey:        &anypb.Any{TypeUrl: anyPk.TypeUrl, Value: anyPk.Value},
		}
		return authsigning.VerifySignature(ctx, pubKey, signerData, sigs[0].Data, r.signModes, adaptable.GetSigningTxData()) == nil
	}
}
