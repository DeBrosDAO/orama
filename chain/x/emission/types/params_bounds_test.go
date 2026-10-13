package types_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// The bounds of Params.Validate are exact: a boundary that moves by one is a parameter accepted
// outside its coded bound. Mutation testing (make -C chain mutation) found these edges unpinned:
// the tests around them used values far from the bound.
func TestParamsValidate_epochDurationBounds(t *testing.T) {
	maxSeconds := int64(types.MaxEpochDuration / time.Second)
	for name, tc := range map[string]struct {
		seconds int64
		ok      bool
	}{
		"negative":         {-1, false},
		"zero":             {0, false},
		"one second":       {1, true},
		"the maximum":      {maxSeconds, true},
		"one past maximum": {maxSeconds + 1, false},
	} {
		// Bootstrap stake switches the production floors off, so only the absolute bounds are tested.
		p := types.NewParams(time.Duration(tc.seconds)*time.Second, types.MinBlocksFloor, true)
		err := p.Validate()
		if tc.ok {
			require.NoError(t, err, name)
		} else {
			require.Error(t, err, name)
		}
	}
}

func TestParamsValidate_minBlocksPerEpochBounds(t *testing.T) {
	for name, tc := range map[string]struct {
		blocks uint64
		ok     bool
	}{
		"zero":             {0, false},
		"one block":        {1, true},
		"the maximum":      {types.MaxBlocksPerEpoch, true},
		"one past maximum": {types.MaxBlocksPerEpoch + 1, false},
	} {
		p := types.NewParams(types.EpochDurationFloor, tc.blocks, true)
		err := p.Validate()
		if tc.ok {
			require.NoError(t, err, name)
		} else {
			require.Error(t, err, name)
		}
	}
}

func TestParamsValidate_productionFloorsAreExactUnlessBootstrapStake(t *testing.T) {
	require.NoError(t, types.NewParams(types.EpochDurationFloor, types.MinBlocksFloor, false).Validate())
	require.Error(t, types.NewParams(types.EpochDurationFloor-time.Second, types.MinBlocksFloor, false).Validate())
	require.Error(t, types.NewParams(types.EpochDurationFloor, types.MinBlocksFloor-1, false).Validate())
}
