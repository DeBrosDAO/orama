package keeper_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

func checkVerify(e *testutil.Env, raw []byte) error {
	ctx := e.CheckCtx()
	adm, err := e.Keeper.Admit(ctx, raw, nil, keeper.KindTransfer, false)
	if err != nil {
		return err
	}
	return e.Keeper.Verify(ctx, raw, nil, adm)
}

func TestAdmission_aBundleThatFailedItsProofCostsOneVerification(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error { return verify.ErrProofRejected }
	raw := transferBundle(2, 20, emptyAnchor(t))

	require.ErrorIs(t, checkVerify(e, raw), verify.ErrProofRejected)
	require.Equal(t, 1, e.V2.Calls)
	for i := 0; i < 5; i++ {
		require.ErrorIs(t, checkVerify(e, raw), verify.ErrProofRejected, "still refused, from memory")
	}
	require.Equal(t, 1, e.V2.Calls, "the repeats never reached a verifier")
}

func TestAdmission_aDifferentByteIsADifferentBundle(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error { return verify.ErrProofRejected }
	raw := transferBundle(2, 20, emptyAnchor(t))
	require.Error(t, checkVerify(e, raw))
	mutated := append([]byte(nil), raw...)
	mutated[len(mutated)-100] ^= 1 // in the proof
	require.Error(t, checkVerify(e, mutated))
	require.Equal(t, 2, e.V2.Calls, "remembering one failure does not refuse another bundle")
}

func TestAdmission_aVerifierFaultIsNeverRemembered(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error { return verify.ErrVerifierFault }
	raw := transferBundle(2, 20, emptyAnchor(t))
	require.ErrorIs(t, checkVerify(e, raw), verify.ErrVerifierFault)
	e.V2.Reject = nil
	require.NoError(t, checkVerify(e, raw), "a fault says nothing about the bundle")
}

func TestAdmission_aBlockNeverReadsTheMempoolsMemory(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	shield(t, e, 1, 1000)
	e.EndBlock()
	raw := transferBundle(2, 20, rootAfter(1))
	e.V2.Reject = func([]byte) error { return verify.ErrProofRejected }
	require.Error(t, checkVerify(e, raw))
	e.V2.Reject = nil
	require.NoError(t, transfer(e, raw), "in a block every node verifies afresh")
}

func TestAdmission_eachNodeVerifiesABoundedNumberOfProofsPerBlock(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	e.V2.Reject = func([]byte) error { return verify.ErrProofRejected }
	var busy error
	for i := 0; i <= keeper.MaxCheckVerificationsPerBlock+1; i++ {
		nf := [32]byte{0xAA, byte(i >> 8), byte(i)}
		raw := testutil.Bundle{Seed: 1, ValueBalance: 20, Anchor: emptyAnchor(t), Nullifiers: [][32]byte{nf}}.Encode(1)
		if err := checkVerify(e, raw); errors.Is(err, keeper.ErrMempoolBusy) {
			busy = err
			break
		}
	}
	require.ErrorIs(t, busy, keeper.ErrMempoolBusy)
	require.LessOrEqual(t, e.V2.Calls, keeper.MaxCheckVerificationsPerBlock)
	e.EndBlock()
	require.NotErrorIs(t, checkVerify(e, transferBundle(99, 20, emptyAnchor(t))), keeper.ErrMempoolBusy, "the next block has its budget back")
}

func TestSignerless_aBlockHoldsAtMostTheParamsLimit(t *testing.T) {
	e := testutil.NewEnv(t, func(gs *types.GenesisState) { gs.Params.MaxSignerlessPerBlock = 2 })
	shield(t, e, 1, 1000)
	e.EndBlock()
	require.NoError(t, transfer(e, transferBundle(2, 20, rootAfter(1))))
	require.NoError(t, transfer(e, transferBundle(3, 20, rootAfter(1))))
	require.ErrorIs(t, transfer(e, transferBundle(4, 20, rootAfter(1))), keeper.ErrSignerlessBlockFull)
	e.EndBlock()
	require.NoError(t, transfer(e, transferBundle(4, 20, rootAfter(1, 2, 3))), "the next block starts empty")
}

func TestSignerless_aFailedTransferGivesItsSlotBack(t *testing.T) {
	e := testutil.NewEnv(t, func(gs *types.GenesisState) { gs.Params.MaxSignerlessPerBlock = 1 })
	shield(t, e, 1, 1000)
	e.EndBlock()
	require.Error(t, transfer(e, transferBundle(2, 1, rootAfter(1))), "fee too low")
	require.NoError(t, transfer(e, transferBundle(3, 20, rootAfter(1))), "the failed one took no slot")
}
