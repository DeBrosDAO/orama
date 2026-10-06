package types_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/relay/types"
)

func TestInputsRootMatchesRecomputationAndChangesWhenMutated(t *testing.T) {
	entry := types.RelayObservation{
		RsaFingerprint:  bytes.Repeat([]byte{0x01}, types.RSAFingerprintLen),
		Ed25519Id:       bytes.Repeat([]byte{0x02}, types.Ed25519PubLen),
		ConsensusWeight: math.NewInt(10),
		Flags:           types.FlagExit,
		UptimeFraction:  math.LegacyMustNewDecFromStr("0.9"),
	}
	first, err := types.InputsRoot([]types.RelayObservation{entry})
	require.NoError(t, err)
	second, err := types.InputsRoot([]types.RelayObservation{entry})
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Len(t, first, types.InputsRootLen)

	mutated := entry
	mutated.ConsensusWeight = math.NewInt(11)
	bad, err := types.InputsRoot([]types.RelayObservation{mutated})
	require.NoError(t, err)
	require.NotEqual(t, first, bad)
}

func TestProtoRoundTrip(t *testing.T) {
	params := types.DefaultParams()
	roundTrip(t, &params)

	obs := types.RelayObservation{
		RsaFingerprint:  bytes.Repeat([]byte{0x11}, types.RSAFingerprintLen),
		Ed25519Id:       bytes.Repeat([]byte{0x22}, types.Ed25519PubLen),
		ConsensusWeight: math.NewInt(42),
		Flags:           types.FlagExit,
		UptimeFraction:  math.LegacyMustNewDecFromStr("0.5"),
	}
	roundTrip(t, &obs)

	msg := types.MsgReportEpoch{
		Reporter:   "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq",
		Epoch:      3,
		ChunkIndex: 1,
		ChunkCount: 2,
		Entries:    []types.RelayObservation{obs},
		InputsRoot: bytes.Repeat([]byte{0xab}, types.InputsRootLen),
	}
	roundTrip(t, &msg)

	gs := types.DefaultGenesisState()
	gs.Reporters = []string{"orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"}
	roundTrip(t, gs)

	result := types.EpochResult{
		Epoch:     2,
		QuorumMet: true,
		Ceiling:   math.NewInt(100),
		Minted:    math.NewInt(40),
	}
	roundTrip(t, &result)

	register := types.MsgRegisterRelay{
		Operator:         "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq",
		NodeId:           "node-a",
		RsaFingerprint:   bytes.Repeat([]byte{0x11}, types.RSAFingerprintLen),
		Exit:             true,
		Ed25519Signature: bytes.Repeat([]byte{0xcd}, types.Ed25519SigLen),
	}
	roundTrip(t, &register)

	update := types.MsgUpdateReporters{
		Signer:    "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq",
		Reporters: []string{"orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"},
	}
	roundTrip(t, &update)
}

type marshaller interface {
	Marshal() ([]byte, error)
	Unmarshal([]byte) error
	Reset()
}

func roundTrip(t *testing.T, msg marshaller) {
	t.Helper()
	bz, err := msg.Marshal()
	require.NoError(t, err)
	require.NotEmpty(t, bz)
	msg.Reset()
	require.NoError(t, msg.Unmarshal(bz))
	again, err := msg.Marshal()
	require.NoError(t, err)
	require.Equal(t, bz, again)
}
