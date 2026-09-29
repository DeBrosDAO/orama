// Package ante is x/shielded's part of the ante path. A signer-less shielded transfer has its own
// short chain (Route); every signed shielded message goes through the normal chain with a shape
// check before fees are taken and a proof check after the signature.
package ante

import (
	"fmt"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// MaxSignerlessOverhead is the most bytes a signer-less tx may carry around its bundle. The tx pays no
// size gas (its gas is fixed), so padding is refused rather than priced.
const MaxSignerlessOverhead = 512

// IsSignerless reports whether tx is a signer-less shielded transfer: exactly one message, a
// MsgShieldedTransfer. A tx that carries one among other messages is not, and is refused by
// ShapeDecorator.
func IsSignerless(tx sdk.Tx) bool {
	msgs := tx.GetMsgs()
	if len(msgs) != 1 {
		return false
	}
	_, ok := msgs[0].(*types.MsgShieldedTransfer)
	return ok
}

// Route sends a signer-less transfer down its own chain and every other tx down the normal one.
func Route(signerless, normal sdk.AnteHandler) sdk.AnteHandler {
	return func(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
		if IsSignerless(tx) {
			return signerless(ctx, tx, simulate)
		}
		return normal(ctx, tx, simulate)
	}
}

// signedBundle is a signed shielded message's bundle, the binding its signatures must commit to
// and the kind of message it came in.
type signedBundle struct {
	raw     []byte
	binding []byte
	kind    keeper.Kind
}

// bundleOf returns the bundle of a signed shielded message. ok is false for any other message.
func bundleOf(msg sdk.Msg) (b signedBundle, ok bool, err error) {
	switch m := msg.(type) {
	case *types.MsgShield:
		return signedBundle{raw: m.Bundle, kind: keeper.KindShield}, true, nil
	case *types.MsgShieldEarnings:
		return signedBundle{raw: m.Bundle, kind: keeper.KindShieldEarnings}, true, nil
	case *types.MsgUnshield:
		binding, err := m.Binding()
		return signedBundle{raw: m.Bundle, binding: binding, kind: keeper.KindUnshield}, true, err
	}
	return signedBundle{}, false, nil
}

// ShapeDecorator refuses a shielded message that is not alone in its tx. It runs early in the
// normal chain, before any fee is taken, so a malformed tx costs nothing to refuse. A
// MsgShieldedTransfer never reaches the normal chain alone (Route), so any it finds here is one
// among other messages.
type ShapeDecorator struct{}

// AnteHandle implements sdk.AnteDecorator.
func (ShapeDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	msgs := tx.GetMsgs()
	for _, msg := range msgs {
		_, signed, _ := bundleOf(msg)
		_, transfer := msg.(*types.MsgShieldedTransfer)
		if (signed || transfer) && len(msgs) != 1 {
			return ctx, fmt.Errorf("%w: %T must be the only message in its tx", types.ErrTxShape, msg)
		}
		if transfer {
			return ctx, fmt.Errorf("%w: a signer-less transfer takes the signer-less path", types.ErrTxShape)
		}
	}
	return next(ctx, tx, simulate)
}

// ProofDecorator checks a signed shielded message's bundle in the mempool: the cheap checks, then
// the proofs, after the signature has been verified so unsigned garbage never costs proof work. It
// also marks the nullifiers pending so a second bundle with the same nullifier is refused. In a
// block it does nothing: the message server verifies there, once.
type ProofDecorator struct{ keeper keeper.Keeper }

// NewProofDecorator builds the decorator.
func NewProofDecorator(k keeper.Keeper) ProofDecorator { return ProofDecorator{keeper: k} }

// AnteHandle implements sdk.AnteDecorator.
func (d ProofDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	if !ctx.IsCheckTx() && !ctx.IsReCheckTx() {
		return next(ctx, tx, simulate)
	}
	for _, msg := range tx.GetMsgs() {
		b, signed, err := bundleOf(msg)
		if err != nil {
			return ctx, err
		}
		if !signed {
			continue
		}
		adm, err := d.keeper.Admit(ctx, b.raw, b.binding, b.kind, !ctx.IsReCheckTx())
		if err != nil {
			return ctx, err
		}
		if err := d.keeper.MarkPending(ctx, adm.Bundle.Nullifiers); err != nil {
			return ctx, err
		}
	}
	return next(ctx, tx, simulate)
}

// SignerlessDecorator is the whole check of a signer-less transfer. The tx must carry no
// signature, no fee, no memo and no fee payer or granter, and declare exactly the gas of its
// bundle: its fee is the bundle's value balance, and nothing else may pay or identify anyone.
type SignerlessDecorator struct{ keeper keeper.Keeper }

// NewSignerlessDecorator builds the decorator.
func NewSignerlessDecorator(k keeper.Keeper) SignerlessDecorator {
	return SignerlessDecorator{keeper: k}
}

// AnteHandle implements sdk.AnteDecorator.
func (d SignerlessDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	msg, ok := tx.GetMsgs()[0].(*types.MsgShieldedTransfer)
	if !ok {
		return ctx, fmt.Errorf("%w: not a signer-less transfer", types.ErrTxShape)
	}
	if err := msg.ValidateBasic(); err != nil {
		return ctx, err
	}
	if err := d.checkShape(tx); err != nil {
		return ctx, err
	}
	if len(ctx.TxBytes()) > len(msg.Bundle)+MaxSignerlessOverhead {
		return ctx, fmt.Errorf("%w: the tx is %d bytes around a %d-byte bundle, the most overhead is %d",
			types.ErrTxShape, len(ctx.TxBytes())-len(msg.Bundle), len(msg.Bundle), MaxSignerlessOverhead)
	}
	// The tx declares exactly its bundle's gas and is charged that fixed schedule, so the reads
	// the checks make run on an unmetered context.
	work := ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	adm, err := d.keeper.Admit(work, msg.Bundle, nil, keeper.KindTransfer, false)
	if err != nil {
		return ctx, err
	}
	if gas := ctx.GasMeter().Limit(); gas != adm.Gas && !simulate {
		return ctx, fmt.Errorf("%w: gas limit %d, a %d-action bundle must declare %d", types.ErrTxShape, gas, adm.Bundle.Actions, adm.Gas)
	}
	if ctx.IsCheckTx() && !ctx.IsReCheckTx() && !simulate {
		if err := d.keeper.Verify(ctx, msg.Bundle, nil, adm); err != nil {
			return ctx, err
		}
	}
	if ctx.IsCheckTx() {
		if err := d.keeper.MarkPending(work, adm.Bundle.Nullifiers); err != nil {
			return ctx, err
		}
	}
	return next(ctx, tx, simulate)
}

func (d SignerlessDecorator) checkShape(tx sdk.Tx) error {
	sigTx, ok := tx.(authsigning.SigVerifiableTx)
	if !ok {
		return sdkerrors.ErrTxDecode.Wrap("tx must be a signature-verifiable tx")
	}
	sigs, err := sigTx.GetSignaturesV2()
	if err != nil {
		return err
	}
	if len(sigs) != 0 {
		return fmt.Errorf("%w: a signer-less transfer carries no signature", types.ErrTxShape)
	}
	feeTx, ok := tx.(sdk.FeeTx)
	if !ok {
		return sdkerrors.ErrTxDecode.Wrap("tx must be a fee tx")
	}
	if !feeTx.GetFee().IsZero() || len(feeTx.FeeGranter()) != 0 {
		return fmt.Errorf("%w: a signer-less transfer declares no fee and no fee granter; its fee is the bundle's value balance", types.ErrTxShape)
	}
	if memo, ok := tx.(sdk.TxWithMemo); ok && memo.GetMemo() != "" {
		return fmt.Errorf("%w: a signer-less transfer carries no memo", types.ErrTxShape)
	}
	return nil
}
