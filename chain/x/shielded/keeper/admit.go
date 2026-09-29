package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// Kind is which message a bundle came in.
type Kind uint8

const (
	// KindTransfer is a signer-less shielded transfer: the value balance is the fee.
	KindTransfer Kind = iota
	// KindShield is transparent to shielded from the signer's bank balance.
	KindShield
	// KindShieldEarnings is transparent to shielded from the signer's earnings.
	KindShieldEarnings
	// KindUnshield is shielded to a target owned by the signer.
	KindUnshield
)

// Admitted is a bundle that passed every check.
type Admitted struct {
	Bundle *bundle.Bundle
	// Amount is what crosses between the pool and the transparent side: the fee of a transfer, the
	// amount shielded, or the amount unshielded. It is the absolute value balance.
	Amount math.Int
	// NullifierFees is the burned per-nullifier fee for this bundle.
	NullifierFees math.Int
	// Gas is the gas the bundle must declare and is charged: action gas x actions.
	Gas uint64
	// BaseFee is the burned base part of a transfer's fee (action gas x base fee), zero otherwise.
	BaseFee math.Int
}

// Admit runs the checks a bundle must pass, cheap ones first: size, value balance and fee,
// nullifiers, anchor, and only then, when verifyProof is set, the proofs and signatures. It
// changes no state. The mempool runs it in the ante handler; the message server runs it again
// with verifyProof set, because the message server is what decides.
func (k Keeper) Admit(ctx sdk.Context, raw, binding []byte, kind Kind, verifyProof bool) (*Admitted, error) {
	p, err := k.params(ctx)
	if err != nil {
		return nil, err
	}
	b, err := bundle.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", types.ErrBundleSize, err)
	}
	if b.Actions > int(p.MaxActionsPerBundle) {
		return nil, fmt.Errorf("%w: %d actions, the limit is %d", types.ErrBundleSize, b.Actions, p.MaxActionsPerBundle)
	}
	adm, err := k.checkValue(ctx, p, b, kind)
	if err != nil {
		return nil, err
	}
	if err := k.checkNullifiers(ctx, b); err != nil {
		return nil, err
	}
	if err := k.checkAnchor(ctx, p, b.Anchor); err != nil {
		return nil, err
	}
	if verifyProof {
		if err := k.Verify(ctx, raw, binding, adm); err != nil {
			return nil, err
		}
	}
	return adm, nil
}

// Verify charges the bundle's gas and runs every verifier on it. All must accept.
//
// In the mempool (CheckTx) a bundle that already failed is refused from memory, and each node
// verifies at most MaxCheckVerificationsPerBlock proofs between blocks, so a stream of bundles
// that fail only their proof costs a node one verification per distinct bundle and a bounded
// number per block. In a block neither applies: every node verifies.
func (k Keeper) Verify(ctx sdk.Context, raw, binding []byte, adm *Admitted) error {
	ctx.GasMeter().ConsumeGas(adm.Gas, "shielded proof verification")
	if !ctx.IsCheckTx() {
		return k.check(raw, binding)
	}
	key := admissionKey(raw, binding)
	if err, failed := k.admission.recalled(key); failed {
		return fmt.Errorf("shielded bundle refused (already rejected): %w", err)
	}
	if !k.admission.spend() {
		return ErrMempoolBusy
	}
	err := k.check(raw, binding)
	if err != nil {
		k.admission.remember(key, err)
	}
	return err
}

func (k Keeper) check(raw, binding []byte) error {
	if err := verify.Check(raw, binding, k.deps.Verifiers...); err != nil {
		return fmt.Errorf("shielded bundle refused: %w", err)
	}
	return nil
}

// checkValue applies the sign and fee rules of the message the bundle came in.
func (k Keeper) checkValue(ctx sdk.Context, p types.Params, b *bundle.Bundle, kind Kind) (*Admitted, error) {
	vb := math.NewInt(b.ValueBalance)
	nullifierFees := p.NullifierFee.MulRaw(int64(b.Actions))
	adm := &Admitted{Bundle: b, NullifierFees: nullifierFees, BaseFee: math.ZeroInt(), Gas: p.TxGas(b.Actions)}
	switch kind {
	case KindTransfer:
		if vb.IsNegative() {
			return nil, fmt.Errorf("%w: a transfer's value balance is its fee and cannot be negative", types.ErrValueBalance)
		}
		baseFee, err := k.deps.Fees.BaseFeePerGas(ctx)
		if err != nil {
			return nil, fmt.Errorf("load the base fee: %w", err)
		}
		adm.BaseFee = baseFee.Mul(math.NewIntFromUint64(adm.Gas))
		if need := adm.BaseFee.Add(nullifierFees); vb.LT(need) {
			return nil, fmt.Errorf("%w: value balance %s, need %s (base %s + nullifiers %s)",
				types.ErrFeeTooLow, vb, need, adm.BaseFee, nullifierFees)
		}
		adm.Amount = vb
	case KindShield, KindShieldEarnings:
		if !vb.IsNegative() {
			return nil, fmt.Errorf("%w: shielding needs a negative value balance", types.ErrValueBalance)
		}
		adm.Amount = vb.Neg()
	case KindUnshield:
		if !vb.IsPositive() {
			return nil, fmt.Errorf("%w: unshielding needs a positive value balance", types.ErrValueBalance)
		}
		adm.Amount = vb
	default:
		return nil, fmt.Errorf("unknown bundle kind %d", kind)
	}
	if kind == KindUnshield && adm.Amount.LTE(nullifierFees) {
		return nil, fmt.Errorf("%w: amount %s, nullifier fees %s", types.ErrAmountTooSmall, adm.Amount, nullifierFees)
	}
	return adm, nil
}

func (k Keeper) checkNullifiers(ctx sdk.Context, b *bundle.Bundle) error {
	seen := make(map[[bundle.NodeLen]byte]bool, len(b.Nullifiers))
	for _, nf := range b.Nullifiers {
		if seen[nf] {
			return fmt.Errorf("%w: %x", types.ErrDuplicateNullifier, nf)
		}
		seen[nf] = true
		if err := k.checkUnspent(ctx, nf); err != nil {
			return err
		}
	}
	return nil
}

// checkAnchor accepts the empty tree's root, which only fake spends can use, and any root the
// tree had within the anchor window.
func (k Keeper) checkAnchor(ctx context.Context, p types.Params, anchor [bundle.NodeLen]byte) error {
	empty, err := k.EmptyRoot()
	if err != nil {
		return fmt.Errorf("compute the empty tree root: %w", err)
	}
	if bytes.Equal(anchor[:], empty[:]) {
		return nil
	}
	height, err := k.Anchors.Get(ctx, anchor[:])
	if errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("%w: %x", types.ErrAnchorUnknown, anchor)
	}
	if err != nil {
		return fmt.Errorf("read anchor: %w", err)
	}
	now := sdk.UnwrapSDKContext(ctx).BlockHeight()
	if now-height > int64(p.AnchorWindowBlocks) {
		return fmt.Errorf("%w: %x was the root %d blocks ago, the window is %d",
			types.ErrAnchorUnknown, anchor, now-height, p.AnchorWindowBlocks)
	}
	return nil
}
