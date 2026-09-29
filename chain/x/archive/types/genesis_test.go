package types_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

func TestDefaultGenesis_valid(t *testing.T) {
	require.NoError(t, types.DefaultGenesisState().Validate())
}

func TestGenesis_contiguousPrefixIgnoresGap(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.Ranges = []types.RangeRecord{
		archivedRecord(1, 10),
		archivedRecord(12, 20),
	}
	gs.LastArchivedHeight = 20
	require.Error(t, gs.Validate())

	gs.LastArchivedHeight = 10
	require.NoError(t, gs.Validate())
	require.Equal(t, int64(10), types.ContiguousArchivedHeight(gs.Ranges))
}

func TestGenesis_archivedWithoutQuorum(t *testing.T) {
	rec := archivedRecord(1, 10)
	rec.DealIds = rec.DealIds[:2]
	gs := types.DefaultGenesisState()
	gs.Ranges = []types.RangeRecord{rec}
	gs.LastArchivedHeight = 10
	require.Error(t, gs.Validate())
}

func TestGenesis_overlap(t *testing.T) {
	gs := types.DefaultGenesisState()
	gs.Ranges = []types.RangeRecord{
		archivedRecord(1, 10),
		archivedRecord(10, 20),
	}
	gs.LastArchivedHeight = 20
	require.Error(t, gs.Validate())
}

func TestGenesis_quorumNotMarkedArchived(t *testing.T) {
	rec := archivedRecord(1, 10)
	rec.Archived = false
	gs := types.DefaultGenesisState()
	gs.Ranges = []types.RangeRecord{rec}
	require.Error(t, gs.Validate())
}

func archivedRecord(start, end int64) types.RangeRecord {
	var archivers, operators []string
	for n := byte(1); n <= 3; n++ {
		archivers = append(archivers, sdk.AccAddress(bytes.Repeat([]byte{n}, 20)).String())
		operators = append(operators, sdk.AccAddress(bytes.Repeat([]byte{n + 100}, 20)).String())
	}
	return types.RangeRecord{
		StartHeight: start,
		EndHeight:   end,
		BundleCid:   "bafyvalidarchivecid",
		BundleHash:  bytes.Repeat([]byte{1}, types.HashLen),
		MerkleRoot:  bytes.Repeat([]byte{2}, types.HashLen),
		DealIds:     []string{fmt.Sprint(start*10 + 1), fmt.Sprint(start*10 + 2), fmt.Sprint(start*10 + 3)},
		Archivers:   archivers,
		Operators:   operators,
		Archived:    true,
	}
}

func TestGenesis_operatorsPairWithArchiversAndAreDistinct(t *testing.T) {
	gs := types.DefaultGenesisState()
	rec := archivedRecord(1, 100)
	gs.LastArchivedHeight = 100
	gs.Ranges = []types.RangeRecord{rec}
	require.NoError(t, gs.Validate())

	short := archivedRecord(1, 100)
	short.Operators = short.Operators[:2]
	gs.Ranges = []types.RangeRecord{short}
	require.ErrorContains(t, gs.Validate(), "operators")

	repeat := archivedRecord(1, 100)
	repeat.Operators[1] = repeat.Operators[0]
	gs.Ranges = []types.RangeRecord{repeat}
	require.ErrorContains(t, gs.Validate(), "repeats operator")

	bad := archivedRecord(1, 100)
	bad.Operators[2] = "op-3"
	gs.Ranges = []types.RangeRecord{bad}
	require.ErrorContains(t, gs.Validate(), "canonical account address")

	shared := archivedRecord(101, 200)
	shared.DealIds[0] = rec.DealIds[0]
	gs.Ranges = []types.RangeRecord{rec, shared}
	gs.LastArchivedHeight = 200
	require.ErrorContains(t, gs.Validate(), "backs both")
}
