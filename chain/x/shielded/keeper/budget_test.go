package keeper_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// deadlineVerifier records the time limit a request was given.
type deadlineVerifier struct {
	*testutil.Verifier
	got []time.Duration
}

func (d *deadlineVerifier) VerifyWithin(b, binding []byte, within time.Duration) error {
	d.got = append(d.got, within)
	return d.Verifier.Verify(b, binding)
}

// Each request of a proposal check is given what is left of the budget, so the check cannot run past
// it by a whole request timeout; with nothing left it asks nothing.
func TestProposalChecker_everyRequestGetsWhatIsLeftOfTheBudget(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	dv := &deadlineVerifier{Verifier: &testutil.Verifier{Name: "proposal-b"}}
	k := keeper.WithProposalVerifiersForTest(e.Keeper, []verify.Verifier{dv, e.V1})
	c, err := k.NewProposalChecker(withBlockGas(e.Ctx, 1_000_000), 2*time.Second)
	require.NoError(t, err)
	now := time.Unix(1_700_000_000, 0)
	keeper.SetCheckerClockForTest(c, func() time.Time { return now }, now.Add(2*time.Second))
	anchor := emptyAnchor(t)

	require.NoError(t, c.Admit(check(testutil.Bundle{Seed: 20, ValueBalance: 20, Anchor: anchor}.Encode(1))))
	now = now.Add(1500 * time.Millisecond)
	require.NoError(t, c.Admit(check(testutil.Bundle{Seed: 21, ValueBalance: 20, Anchor: anchor}.Encode(1))))
	require.Len(t, dv.got, 2)
	require.Equal(t, 2*time.Second, dv.got[0])
	require.Equal(t, 500*time.Millisecond, dv.got[1], "the second request got only what was left")

	now = now.Add(time.Second)
	require.ErrorIs(t, c.Admit(check(testutil.Bundle{Seed: 22, ValueBalance: 20, Anchor: anchor}.Encode(1))), keeper.ErrProposalBudget)
	require.Len(t, dv.got, 2, "with nothing left nothing was asked")
	require.True(t, keeper.AbortsChecks(keeper.ErrProposalBudget))
	require.True(t, keeper.AbortsChecks(verify.ErrVerifierStarting))
	require.False(t, keeper.AbortsChecks(verify.ErrProofRejected), "a verdict on one transaction does not end the checking")
}

// blocking is a verifier that waits to be released; unlike the shared fake it is safe to call from
// several goroutines.
type blocking struct {
	entered *atomic.Int32
	release chan struct{}
}

func (blocking) ID() string { return "mempool-b" }

func (b blocking) Verify([]byte, []byte) error {
	b.entered.Add(1)
	<-b.release
	return nil
}

// accepts is a stateless verifier that accepts everything, safe for concurrent use.
type accepts struct{}

func (accepts) ID() string                  { return "library" }
func (accepts) Verify([]byte, []byte) error { return nil }

// Simulations run outside CometBFT's ABCI lock; at most MaxConcurrentSimulations verify at once and
// the rest are refused with the one generic text.
func TestVerify_concurrentSimulationsAreCapped(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	release := make(chan struct{})
	var entered atomic.Int32
	hung := blocking{entered: &entered, release: release}
	k := e.KeeperWithMempool([]verify.Verifier{e.V1, e.V2}, []verify.Verifier{hung, accepts{}})
	// Each simulation has its own gas meter, as it does in a node.
	newSim := func() sdk.Context {
		return e.Ctx.WithIsCheckTx(true).WithExecMode(sdk.ExecModeSimulate).WithGasMeter(storetypes.NewInfiniteGasMeter())
	}
	done := make(chan error, keeper.MaxConcurrentSimulations)
	for i := 0; i < keeper.MaxConcurrentSimulations; i++ {
		raw := transferBundle(byte(30+i), 20, emptyAnchor(t))
		sim := newSim()
		adm, err := k.Admit(sim, raw, nil, keeper.KindTransfer, false)
		require.NoError(t, err)
		go func() { done <- k.Verify(sim, raw, nil, adm) }()
	}
	require.Eventually(t, func() bool { return int(entered.Load()) == keeper.MaxConcurrentSimulations }, 5*time.Second, time.Millisecond)

	raw := transferBundle(40, 20, emptyAnchor(t))
	sim := newSim()
	adm, err := k.Admit(sim, raw, nil, keeper.KindTransfer, false)
	require.NoError(t, err)
	err = k.Verify(sim, raw, nil, adm)
	require.ErrorIs(t, err, keeper.ErrMempoolBusy)
	require.Equal(t, keeper.GenericRefusal, err.Error(), "a remote caller gets the one generic text")
	close(release)
	for i := 0; i < keeper.MaxConcurrentSimulations; i++ {
		require.NoError(t, <-done)
	}
}

// Busy, starting and a recent fault all read the same to a remote caller.
func TestVerify_remoteCallersGetOneTextForEveryLocalRefusal(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	raw := transferBundle(50, 20, emptyAnchor(t))
	ctx := e.CheckCtx()
	adm, err := e.Keeper.Admit(ctx, raw, nil, keeper.KindTransfer, false)
	require.NoError(t, err)

	e.V2.Reject = func([]byte) error { return verify.ErrVerifierFault }
	first := e.Keeper.Verify(ctx, raw, nil, adm) // the fault
	again := e.Keeper.Verify(ctx, raw, nil, adm) // remembered
	require.ErrorIs(t, first, verify.ErrVerifierFault)
	require.ErrorIs(t, again, verify.ErrVerifierFault)
	require.Contains(t, first.Error(), keeper.GenericRefusal)
	require.Contains(t, again.Error(), keeper.GenericRefusal)

	starting := e.KeeperWithMempool([]verify.Verifier{e.V1, e.V2}, []verify.Verifier{notReady{&testutil.Verifier{Name: "m"}}, e.V1})
	err = starting.Verify(ctx, transferBundle(51, 20, emptyAnchor(t)), nil, adm)
	require.ErrorIs(t, err, verify.ErrVerifierStarting)
	require.Contains(t, err.Error(), keeper.GenericRefusal)
	require.Equal(t, keeper.GenericRefusal, keeper.ErrMempoolBusy.Error())
}

// With less than the floor left, the bundle is not put to the verifier at all: a local nil vote.
func TestProposalChecker_underTheFloorNothingIsAsked(t *testing.T) {
	e := testutil.NewEnv(t, nil)
	dv := &deadlineVerifier{Verifier: &testutil.Verifier{Name: "proposal-b"}}
	k := keeper.WithProposalVerifiersForTest(e.Keeper, []verify.Verifier{dv, e.V1})
	c, err := k.NewProposalChecker(withBlockGas(e.Ctx, 1_000_000), time.Second)
	require.NoError(t, err)
	now := time.Unix(1_700_000_000, 0)
	keeper.SetCheckerClockForTest(c, func() time.Time { return now }, now.Add(keeper.MinProposalRequest-time.Millisecond))

	err = c.Admit(check(testutil.Bundle{Seed: 60, ValueBalance: 20, Anchor: emptyAnchor(t)}.Encode(1)))
	require.ErrorIs(t, err, keeper.ErrProposalBudget)
	require.NotErrorIs(t, err, keeper.ErrProposalShape)
	require.True(t, keeper.AbortsChecks(err))
	require.Empty(t, dv.got, "the verifier was not asked")
}
