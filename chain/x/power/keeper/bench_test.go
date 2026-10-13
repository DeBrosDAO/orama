package keeper_test

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// benchValidatorStake is each benchmark validator's self-bond, equal so the cap does not bind
	// on any one of them.
	benchValidatorStake = int64(1_000_000)
	// benchHandoverEpoch is the epoch at which the bootstrap committee has handed over (the
	// BootstrapDeadlineEpochs below), so the benchmark measures the steady state of a live set.
	benchHandoverEpoch = uint64(11)
	benchFirstEpoch    = uint64(5)
)

// benchPubKey is a distinct 32-byte consensus key per validator index (testPubKey repeats one byte,
// so it has only 256).
func benchPubKey(i int) []byte {
	pk := make([]byte, 32)
	binary.BigEndian.PutUint32(pk, uint32(i)+1)
	return pk
}

// benchFixture is a keeper whose bonded set holds n validators of equal stake, past the bootstrap
// handover and the ramp, with every validator already published once.
func benchFixture(b *testing.B, n int) *testFixture {
	b.Helper()
	f := newTestFixture(b)
	setupSingleCommitteeGenesis(b, f)
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(b, err)
	p.BootstrapExitStake = math.NewInt(1_000_000_000_000_000)
	p.RampEpochs = 1
	require.NoError(b, f.Keeper.Params.Set(f.Ctx, p))
	for i := 0; i < n; i++ {
		valoper := sdk.ValAddress(fmt.Sprintf("bench_validator_%05d", i)).String()
		f.Staking.addValidator(b, valoper, benchPubKey(i), benchValidatorStake, "0.0")
	}
	// The per-epoch rate limits move lambda one step at a time, so walk the epochs up to the handover.
	for epoch := benchFirstEpoch; epoch <= benchHandoverEpoch; epoch++ {
		f.Emission.epoch = epoch
		_, err := f.Keeper.RunEndBlock(f.Ctx, f.Emission)
		require.NoError(b, err)
	}
	first := sdk.ValAddress(fmt.Sprintf("bench_validator_%05d", 0)).String()
	power, err := f.Keeper.LastPower.Get(f.Ctx, first)
	require.NoError(b, err, "the benchmark's validators must hold published power")
	require.Positive(b, power)
	return f
}

// BenchmarkRunEndBlock is x/power's end-block power computation (shares, cap, rate limits and the
// validator updates) over a bonded set of 30, 100 and 300 validators.
func BenchmarkRunEndBlock(b *testing.B) {
	for _, n := range []int{30, 100, 300} {
		b.Run(fmt.Sprintf("validators=%d", n), func(b *testing.B) {
			f := benchFixture(b, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := f.Keeper.RunEndBlock(f.Ctx, f.Emission); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
