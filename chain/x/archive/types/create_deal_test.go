package types_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

func validCreateDeal() *types.MsgCreateArchiveDeal {
	return &types.MsgCreateArchiveDeal{
		Archiver:        sdk.AccAddress(bytes.Repeat([]byte{9}, 20)).String(),
		NodeId:          "node-1",
		StartHeight:     1,
		EndHeight:       1000,
		PieceRoot:       bytes.Repeat([]byte{7}, types.HashLen),
		RealLeafCount:   3,
		PaddedLeafCount: 4,
		PieceBytes:      3000,
	}
}

func TestMsgCreateArchiveDeal_validateBasic(t *testing.T) {
	require.NoError(t, validCreateDeal().ValidateBasic())
	signer := sdk.AccAddress(bytes.Repeat([]byte{9}, 20))
	require.Equal(t, []sdk.AccAddress{signer}, validCreateDeal().GetSigners())

	for name, mutate := range map[string]func(*types.MsgCreateArchiveDeal){
		"bad archiver":         func(m *types.MsgCreateArchiveDeal) { m.Archiver = "nope" },
		"no node":              func(m *types.MsgCreateArchiveDeal) { m.NodeId = "" },
		"heights reversed":     func(m *types.MsgCreateArchiveDeal) { m.StartHeight, m.EndHeight = 10, 5 },
		"start below one":      func(m *types.MsgCreateArchiveDeal) { m.StartHeight = 0 },
		"short root":           func(m *types.MsgCreateArchiveDeal) { m.PieceRoot = m.PieceRoot[:31] },
		"no bytes":             func(m *types.MsgCreateArchiveDeal) { m.PieceBytes = 0 },
		"no leaves":            func(m *types.MsgCreateArchiveDeal) { m.RealLeafCount = 0 },
		"padding below leaves": func(m *types.MsgCreateArchiveDeal) { m.PaddedLeafCount = 2 },
	} {
		m := validCreateDeal()
		mutate(m)
		require.Errorf(t, m.ValidateBasic(), "%s must be refused", name)
	}
	var nilMsg *types.MsgCreateArchiveDeal
	require.Error(t, nilMsg.ValidateBasic())
}

func validAttest() *types.MsgAttest {
	return &types.MsgAttest{
		Archiver:        sdk.AccAddress(bytes.Repeat([]byte{9}, 20)).String(),
		NodeId:          "node-1",
		StartHeight:     1,
		EndHeight:       1000,
		BundleCid:       "bafyarchivecid",
		BundleHash:      bytes.Repeat([]byte{1}, types.HashLen),
		MerkleRoot:      bytes.Repeat([]byte{2}, types.HashLen),
		PieceRoot:       bytes.Repeat([]byte{7}, types.HashLen),
		RealLeafCount:   3,
		PaddedLeafCount: 4,
		PieceBytes:      3000,
	}
}

// An attestation carries the bundle file's piece commitment, and its shape is checked: the leaf
// counts must be the ones piece_bytes implies, so a commitment that no file could have is refused
// before it is pinned.
func TestMsgAttest_validateBasicChecksThePieceCommitment(t *testing.T) {
	require.NoError(t, validAttest().ValidateBasic())
	for name, mutate := range map[string]func(*types.MsgAttest){
		"no piece root":        func(m *types.MsgAttest) { m.PieceRoot = nil },
		"short piece root":     func(m *types.MsgAttest) { m.PieceRoot = m.PieceRoot[:31] },
		"no bytes":             func(m *types.MsgAttest) { m.PieceBytes = 0 },
		"no leaves":            func(m *types.MsgAttest) { m.RealLeafCount = 0 },
		"leaves too many":      func(m *types.MsgAttest) { m.RealLeafCount, m.PaddedLeafCount = 40, 64 },
		"leaves too few":       func(m *types.MsgAttest) { m.RealLeafCount, m.PaddedLeafCount = 2, 2 },
		"padding not a power":  func(m *types.MsgAttest) { m.PaddedLeafCount = 6 },
		"padding below leaves": func(m *types.MsgAttest) { m.PaddedLeafCount = 2 },
		"beyond the hard limit": func(m *types.MsgAttest) {
			m.PieceBytes = types.MaxPieceBytesLimit + 1
			m.RealLeafCount = (m.PieceBytes + 1023) / 1024
			m.PaddedLeafCount = 1 << 31
		},
		"byte count that wraps an int": func(m *types.MsgAttest) { m.PieceBytes = 1 << 63 },
	} {
		m := validAttest()
		mutate(m)
		require.Errorf(t, m.ValidateBasic(), "%s must be refused", name)
	}
}

func TestParams_maxPieceBytesIsBounded(t *testing.T) {
	require.NoError(t, types.DefaultParams().Validate())
	p := types.DefaultParams()
	p.MaxPieceBytes = 0
	require.Error(t, p.Validate())
	p.MaxPieceBytes = types.MaxPieceBytesLimit + 1
	require.Error(t, p.Validate())
	p.MaxPieceBytes = types.MaxPieceBytesLimit
	require.NoError(t, p.Validate())
}

func TestGenesis_aRangeMustCarryAValidPieceWithinTheCap(t *testing.T) {
	gs := types.DefaultGenesisState()
	rec := archivedRecord(1, 10)
	gs.Ranges, gs.LastArchivedHeight = []types.RangeRecord{rec}, 10
	require.NoError(t, gs.Validate())

	noPiece := archivedRecord(1, 10)
	noPiece.PieceRoot = nil
	gs.Ranges = []types.RangeRecord{noPiece}
	require.ErrorContains(t, gs.Validate(), "piece_root")

	gs.Ranges = []types.RangeRecord{rec}
	gs.Params.MaxPieceBytes = 2999
	require.ErrorIs(t, gs.Validate(), types.ErrPieceTooLarge)
}
