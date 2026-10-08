package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func TestParamsValidate_exitMultiplierBounds(t *testing.T) {
	for name, tc := range map[string]struct {
		value math.LegacyDec
		ok    bool
	}{
		"below one": {math.LegacyMustNewDecFromStr("0.99"), false},
		"one":       {math.LegacyOneDec(), true},
		"at max":    {math.LegacyNewDec(types.MaxExitMultiplier), true},
		"above max": {math.LegacyNewDec(types.MaxExitMultiplier + 1), false},
		"nil":       {math.LegacyDec{}, false},
	} {
		p := types.DefaultParams()
		p.ExitMultiplier = tc.value
		if tc.ok {
			require.NoError(t, p.Validate(), name)
		} else {
			require.Error(t, p.Validate(), name)
		}
	}
}

func TestObservationValidate_weightBounds(t *testing.T) {
	obs := types.RelayObservation{
		RsaFingerprint: make([]byte, types.RSAFingerprintLen),
		Ed25519Id:      make([]byte, types.Ed25519PubLen),
		UptimeFraction: math.LegacyOneDec(),
	}
	for name, tc := range map[string]struct {
		weight math.Int
		ok     bool
	}{
		"zero":      {math.ZeroInt(), true},
		"at max":    {types.MaxConsensusWeight, true},
		"above max": {types.MaxConsensusWeight.AddRaw(1), false},
		"negative":  {math.NewInt(-1), false},
		"nil":       {math.Int{}, false},
	} {
		obs.ConsensusWeight = tc.weight
		if tc.ok {
			require.NoError(t, obs.Validate(), name)
		} else {
			require.Error(t, obs.Validate(), name)
		}
	}
}

func TestGenesisValidate_reportEpochBounds(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.Chunks = []types.ReportChunk{{Epoch: types.MaxReportEpoch + 1}}
	require.ErrorContains(t, gs.Validate(), "must be in [1,")

	gs = types.DefaultGenesisState()
	gs.Reports = []types.CompleteReport{{Epoch: types.MaxReportEpoch + 1}}
	require.ErrorContains(t, gs.Validate(), "must be in [1,")
}
