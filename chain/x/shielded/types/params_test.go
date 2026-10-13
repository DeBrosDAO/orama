package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

func TestParamsValidate_defaultsAreValid(t *testing.T) {
	require.NoError(t, types.DefaultParams().Validate())
}

func TestParamsValidate_namesTheFirstBadAmountEveryTime(t *testing.T) {
	p := types.DefaultParams()
	p.NullifierFee = math.ZeroInt()
	p.UnshieldFloor = math.ZeroInt()
	p.MaxFeeTopup = math.ZeroInt()
	p.QueuePerAddressCap = math.ZeroInt()
	// The check used to range over a map, so which field the error named changed from run to run
	// and from node to node. Four keys come out in 24 orders; fifty runs that all agree by chance
	// have a probability of 24^-49.
	for range 50 {
		require.EqualError(t, p.Validate(), "nullifier_fee must be a positive integer")
	}
}

func TestParamsValidate_nilAndNonPositiveAmountsAreRefused(t *testing.T) {
	cases := map[string]func(*types.Params){
		"nil nullifier fee":     func(p *types.Params) { p.NullifierFee = math.Int{} },
		"negative unshield":     func(p *types.Params) { p.UnshieldFloor = math.NewInt(-1) },
		"zero fee top-up":       func(p *types.Params) { p.MaxFeeTopup = math.ZeroInt() },
		"nil queue per address": func(p *types.Params) { p.QueuePerAddressCap = math.Int{} },
	}
	for name, mutate := range cases {
		p := types.DefaultParams()
		mutate(&p)
		require.Error(t, p.Validate(), name)
	}
}

func TestParamsValidate_zeroCountIsRefused(t *testing.T) {
	p := types.DefaultParams()
	p.MaxActionsPerBundle = 0
	require.ErrorContains(t, p.Validate(), "max_actions_per_bundle")
}

// max_actions_per_bundle is bounded by the protocol ceiling every size limit is derived from: the
// default is inside it, the ceiling itself is valid, and one more action is refused.
func TestParamsValidate_actionCeiling(t *testing.T) {
	require.LessOrEqual(t, types.DefaultMaxActions, uint32(bundle.MaxActions))
	p := types.DefaultParams()
	p.MaxActionsPerBundle = bundle.MaxActions
	p.ActionGas = 1
	require.NoError(t, p.Validate())
	p.MaxActionsPerBundle = bundle.MaxActions + 1
	require.ErrorContains(t, p.Validate(), "protocol ceiling")
}

// The bundle a default-limit transaction carries is far below the bundle ceiling, and a message
// field accepts one of exactly the ceiling's size and no byte more.
func TestBundleSizes_defaultLimitAndCeiling(t *testing.T) {
	require.Equal(t, 53_325, bundle.Len(int(types.DefaultMaxActions)))
	require.Equal(t, 103_823, bundle.MaxBytes)
	msg := &types.MsgShield{Signer: "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqmg3rhc", Bundle: make([]byte, bundle.MaxBytes+1)}
	require.ErrorIs(t, msg.ValidateBasic(), types.ErrBundleSize)
}

// The anchor window is named as 24 hours: it must last 24 hours at the chain's block interval.
func TestDefaultAnchorWindowBlocks_isTwentyFourHoursOfTheChainsBlocks(t *testing.T) {
	require.Equal(t, uint64(24*60*60), types.DefaultAnchorWindowBlocks*params.BlockIntervalSeconds)
}
