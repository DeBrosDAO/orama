package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

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

// Verify charges the bundle's gas and runs every verifier on it. All must accept. A verifier fault
// in a block does not return: it halts the node (checkInBlock).
//
// In the mempool (CheckTx) a bundle that already failed is refused from memory, and each node
// verifies at most MaxCheckVerificationsPerBlock proofs between blocks, so a stream of bundles
// that fail only their proof costs a node one verification per distinct bundle and a bounded
// number per block. In a block neither applies: every node verifies.
func (k Keeper) Verify(ctx sdk.Context, raw, binding []byte, adm *Admitted) error {
	ctx.GasMeter().ConsumeGas(adm.Gas, "shielded proof verification")
	if !ctx.IsCheckTx() {
		return k.checkInBlock(ctx, raw, binding)
	}
	key := admissionKey(raw, binding)
	if err, failed := k.admission.recalled(key); failed {
		return fmt.Errorf("shielded bundle refused (already rejected): %w", err)
	}
	hash := hex.EncodeToString(key[:])
	if k.admission.faultRecalled(key) {
		return unavailable(ctx, ErrRecentVerifierFault, hash, true)
	}
	if !ready(k.mempoolVerifiers()) {
		ctx.Logger().Debug("the mempool's shielded verifier is not ready", "module", "x/shielded", "bundle_sha256", hash)
		return fmt.Errorf("%w: %s", verify.ErrVerifierStarting, GenericRefusal)
	}
	if ctx.ExecMode() == sdk.ExecModeSimulate {
		select {
		case k.admission.simulations <- struct{}{}:
			defer func() { <-k.admission.simulations }()
		default:
			ctx.Logger().Debug("too many simulations are verifying shielded bundles", "module", "x/shielded")
			return ErrMempoolBusy
		}
	}
	if !k.admission.spend() {
		ctx.Logger().Debug("the per-block verification budget of the mempool is spent", "module", "x/shielded")
		return ErrMempoolBusy
	}
	err := verifyWith(k.mempoolVerifiers(), raw, binding)
	if err == nil {
		k.admission.rememberValid(key)
	}
	if err != nil {
		k.admission.remember(key, err)
		if localFault(err) {
			k.admission.rememberFault(key)
		}
	}
	return unavailable(ctx, err, hash, false)
}

// ErrRecentVerifierFault is what the mempool answers for a bundle whose last check, a moment ago,
// ended in a verifier fault: the verifier is not asked again until the memory of it expires. It is
// not a verdict on the bundle, it is kept only by the mempool and never consulted in a block.
var ErrRecentVerifierFault = fmt.Errorf("%w: this bundle's check recently failed on this node", verify.ErrVerifierFault)

// readier is a verifier that can say whether it is ready to be asked without being asked.
type readier interface{ Ready() bool }

// ready reports whether every verifier of the set that can say so is ready. A node whose verifier
// process is still being started refuses at once, before it spends any verification budget on a
// request nothing would answer. Asking also starts a process that is not ready (in the background).
func ready(set []verify.Verifier) bool {
	for _, v := range set {
		if r, ok := v.(readier); ok && !r.Ready() {
			return false
		}
	}
	return true
}

// proposalVerifiers are the verifiers block proposals use.
func (k Keeper) proposalVerifiers() []verify.Verifier {
	if len(k.deps.ProposalVerifiers) > 0 {
		return k.deps.ProposalVerifiers
	}
	return k.mempoolVerifiers()
}

// withDeadline gives a verifier the time one request may take, when it can be given one.
type withDeadline struct {
	verify.Verifier
	d time.Duration
}

func (w withDeadline) Verify(bundle, binding []byte) error {
	if dv, ok := w.Verifier.(verify.DeadlineVerifier); ok {
		return dv.VerifyWithin(bundle, binding, w.d)
	}
	return w.Verifier.Verify(bundle, binding)
}

// CheckProposalProof runs the proposal verifiers over one bundle of a block proposal, within the
// time left (within). It is what ProcessProposal and PrepareProposal ask: a verdict, a fault, a
// verifier that is starting or busy, or the time running out all mean the proposal is not voted for,
// and none ever halts the node (a halt is for FinalizeBlock, where the block is already decided). It
// changes no state and spends none of the mempool's budget.
func (k Keeper) CheckProposalProof(raw, binding []byte, within time.Duration) error {
	key := admissionKey(raw, binding)
	if k.admission.validRecalled(key) {
		return nil
	}
	set := k.proposalVerifiers()
	if !ready(set) {
		return verify.ErrVerifierStarting
	}
	if within <= 0 {
		return ErrProposalBudget
	}
	bounded := make([]verify.Verifier, len(set))
	for i, v := range set {
		bounded[i] = withDeadline{Verifier: v, d: within}
	}
	err := verifyWith(bounded, raw, binding)
	if err == nil {
		k.admission.rememberValid(key)
	}
	return err
}

// mempoolVerifiers are the verifiers CheckTx and a simulation use.
func (k Keeper) mempoolVerifiers() []verify.Verifier {
	if len(k.deps.MempoolVerifiers) > 0 {
		return k.deps.MempoolVerifiers
	}
	return k.deps.Verifiers
}

// unavailable is what the mempool tells a submitter whose transaction this node could not check
// because of the node itself: a generic refusal. The detail (paths, hashes, the child's state) goes
// to this node's log only, so a remote client learns nothing about the machine.
//
// A fault is logged at Error the first time a bundle causes it and at Debug when the same bundle
// comes back (repeat), so a bundle sent in a loop cannot flood the log.
func unavailable(ctx sdk.Context, err error, bundleHash string, repeat bool) error {
	if !localFault(err) {
		return err
	}
	if repeat {
		ctx.Logger().Debug("the shielded verifier could not check a bundle again", "module", "x/shielded", "bundle_sha256", bundleHash, "err", err)
	} else {
		ctx.Logger().Error("the shielded verifier could not check a bundle", "module", "x/shielded", "bundle_sha256", bundleHash, "err", err)
	}
	class := verify.ErrVerifierFault
	if !errors.Is(err, verify.ErrVerifierFault) {
		class = verify.ErrVerifierNotLinked
	}
	return fmt.Errorf("%w: %s", class, GenericRefusal)
}

// localFault reports an error that is a fact about this node and not about the bundle: a verifier
// fault, or verifiers that are not linked or not distinct.
func localFault(err error) bool {
	return errors.Is(err, verify.ErrVerifierFault) || errors.Is(err, verify.ErrVerifierNotLinked)
}

// checkInBlock is check for a bundle in a block.
//
// What the verifiers say about a bundle depends on the bundle's bytes alone, so every node reaches
// the same verdict. A verifier FAULT (verify.ErrVerifierFault: its process died, hung past its
// watchdog, answered out of protocol or would not start) says nothing about the bundle; it is a
// fact about this machine. Turning it into a transaction result would give this node a result its
// peers do not have, a different results hash and a different app hash, and the node would halt
// later on a state of its own making. So in FinalizeBlock a fault records no result at all: the node
// stops with a VerifierHalt, nothing of the block is committed, and on restart it executes the same
// block again against a verifier that works and reaches its peers' verdict. Everywhere else (the
// mempool, a simulation) a fault only refuses the transaction on this node, which is local.
func (k Keeper) checkInBlock(ctx sdk.Context, raw, binding []byte) error {
	err := k.check(raw, binding)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	if ctx.ExecMode() != sdk.ExecModeFinalize {
		return unavailable(ctx, err, hash, false)
	}
	// Not linked and not distinct count as faults only on a chain that must verify: there they
	// mean this node lost a verifier, while on a localnet or a devnet every node refuses alike.
	if errors.Is(err, verify.ErrVerifierFault) || (k.deps.RequireVerifiers && localFault(err)) {
		panic(VerifierHalt{Height: ctx.BlockHeight(), BundleHash: hash, Err: err})
	}
	// Verifiers that are not linked on a localnet or a devnet: the same refusal on every node, with
	// the node's paths and pins kept out of the transaction's log.
	return unavailable(ctx, err, hash, false)
}

func (k Keeper) check(raw, binding []byte) error { return verifyWith(k.deps.Verifiers, raw, binding) }

func verifyWith(verifiers []verify.Verifier, raw, binding []byte) error {
	if err := verify.Check(raw, binding, verifiers...); err != nil {
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
