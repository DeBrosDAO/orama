package types_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// genesisWidth is an empty genesis whose ranges are width blocks long.
func genesisWidth(width int64) *types.GenesisState {
	gs := types.DefaultGenesisState()
	gs.Params.RangeBlocks = width
	return gs
}

func TestDefaultGenesis_valid(t *testing.T) {
	require.NoError(t, types.DefaultGenesisState().Validate())
}

func TestGenesis_contiguousPrefixIgnoresGap(t *testing.T) {
	gs := genesisWidth(10)
	gs.Ranges = []types.RangeRecord{
		archivedRecord(1, 10),
		archivedRecord(21, 30),
	}
	gs.LastArchivedHeight = 30
	require.Error(t, gs.Validate())

	gs.LastArchivedHeight = 10
	require.NoError(t, gs.Validate())
	require.Equal(t, int64(10), types.ContiguousArchivedHeight(gs.Ranges))
}

func TestGenesis_archivedWithoutQuorum(t *testing.T) {
	rec := archivedRecord(1, 10)
	rec.DealIds = rec.DealIds[:2]
	gs := genesisWidth(10)
	gs.Ranges = []types.RangeRecord{rec}
	gs.LastArchivedHeight = 10
	require.Error(t, gs.Validate())
}

// A range that is not one of the fixed ranges is refused, which also makes overlapping ranges
// impossible.
func TestGenesis_aMisalignedOrOverlappingRangeIsRefused(t *testing.T) {
	gs := genesisWidth(10)
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
	gs := genesisWidth(10)
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
		PieceRoot:   bytes.Repeat([]byte{7}, types.HashLen), RealLeafCount: 3, PaddedLeafCount: 4, PieceBytes: 3000,
		DealIds:   []string{fmt.Sprint(start*10 + 1), fmt.Sprint(start*10 + 2), fmt.Sprint(start*10 + 3)},
		Archivers: archivers,
		Operators: operators,
		Archived:  true,
		Decided:   true,
	}
}

// undecidedRecord is a range with two candidate tuples, one attested by two operators, one by one.
func undecidedRecord(start, end int64) types.RangeRecord {
	addr := func(n byte) string { return sdk.AccAddress(bytes.Repeat([]byte{n}, 20)).String() }
	candidate := func(root byte, archivers ...byte) types.Candidate {
		c := types.Candidate{
			BundleCid:  "bafyvalidarchivecid",
			BundleHash: bytes.Repeat([]byte{1}, types.HashLen),
			MerkleRoot: bytes.Repeat([]byte{root}, types.HashLen),
			PieceRoot:  bytes.Repeat([]byte{7}, types.HashLen), RealLeafCount: 3, PaddedLeafCount: 4, PieceBytes: 3000,
		}
		for _, n := range archivers {
			c.Archivers = append(c.Archivers, addr(n))
			c.Operators = append(c.Operators, addr(n+100))
			c.NodeIds = append(c.NodeIds, fmt.Sprintf("node-%d", n))
		}
		return c
	}
	return types.RangeRecord{
		StartHeight: start, EndHeight: end,
		Candidates: []types.Candidate{candidate(2, 1, 2), candidate(3, 3)},
	}
}

func TestGenesis_anUndecidedRangeKeepsItsCandidates(t *testing.T) {
	gs := genesisWidth(10)
	gs.Ranges = []types.RangeRecord{undecidedRecord(1, 10)}
	require.NoError(t, gs.Validate())
	require.Equal(t, int64(0), types.ContiguousArchivedHeight(gs.Ranges), "an undecided range archives nothing")
}

func TestGenesis_undecidedRangesAreValidatedStrictly(t *testing.T) {
	validate := func(rec types.RangeRecord, mutate func(*types.GenesisState)) error {
		gs := genesisWidth(10)
		gs.Ranges = []types.RangeRecord{rec}
		if mutate != nil {
			mutate(gs)
		}
		return gs.Validate()
	}
	cases := map[string]struct {
		mutate func(*types.RangeRecord)
		want   string
	}{
		"no candidates":         {func(r *types.RangeRecord) { r.Candidates = nil }, "candidates"},
		"a winning tuple":       {func(r *types.RangeRecord) { r.BundleCid = "bafyvalidarchivecid" }, "not decided"},
		"deals":                 {func(r *types.RangeRecord) { r.DealIds = []string{"1"} }, "not decided"},
		"the archived mark":     {func(r *types.RangeRecord) { r.Archived = true }, "not decided"},
		"a candidate at quorum": {func(r *types.RangeRecord) { r.Candidates[0] = decidedCandidate(t) }, "archivers"},
		"a repeated tuple": {func(r *types.RangeRecord) {
			r.Candidates[1].MerkleRoot = r.Candidates[0].MerkleRoot
		}, "repeats candidate tuple"},
		"an operator twice": {func(r *types.RangeRecord) {
			r.Candidates[1].Operators = []string{r.Candidates[0].Operators[0]}
		}, "attested two candidate tuples"},
		"an unpaired operator": {func(r *types.RangeRecord) { r.Candidates[0].Operators = r.Candidates[0].Operators[:1] }, "operators"},
		"a bad piece":          {func(r *types.RangeRecord) { r.Candidates[0].PieceRoot = nil }, "piece_root"},
	}
	for name, tc := range cases {
		rec := undecidedRecord(1, 10)
		tc.mutate(&rec)
		require.ErrorContainsf(t, validate(rec, nil), tc.want, "%s", name)
	}

	rec := undecidedRecord(1, 10)
	err := validate(rec, func(gs *types.GenesisState) { gs.Params.MaxCandidatesPerRange = 1 })
	require.ErrorContains(t, err, "max_candidates_per_range")
	err = validate(rec, func(gs *types.GenesisState) { gs.Params.MaxPieceBytes = 2999 })
	require.ErrorIs(t, err, types.ErrPieceTooLarge)
}

func TestGenesis_aDecidedRangeKeepsNoCandidates(t *testing.T) {
	rec := archivedRecord(1, 10)
	rec.Candidates = undecidedRecord(1, 10).Candidates
	gs := genesisWidth(10)
	gs.Ranges, gs.LastArchivedHeight = []types.RangeRecord{rec}, 10
	require.ErrorContains(t, gs.Validate(), "keeps 2 candidates")

	rec = archivedRecord(1, 10)
	rec.Archivers, rec.Operators = rec.Archivers[:2], rec.Operators[:2]
	rec.Archived, rec.DealIds = false, nil
	gs.Ranges, gs.LastArchivedHeight = []types.RangeRecord{rec}, 0
	require.ErrorContains(t, gs.Validate(), "archivers", "a decided range needs the operator quorum")
}

func TestParams_maxCandidatesIsBounded(t *testing.T) {
	p := types.DefaultParams()
	require.NoError(t, p.Validate())
	p.MaxCandidatesPerRange = 0
	require.Error(t, p.Validate())
	p.MaxCandidatesPerRange = types.MaxCandidatesLimit + 1
	require.Error(t, p.Validate())
	p.MaxCandidatesPerRange = types.MaxCandidatesLimit
	require.NoError(t, p.Validate())
}

func decidedCandidate(t *testing.T) types.Candidate {
	t.Helper()
	rec := archivedRecord(1, 10)
	return types.Candidate{
		BundleCid: rec.BundleCid, BundleHash: rec.BundleHash, MerkleRoot: rec.MerkleRoot,
		PieceRoot: rec.PieceRoot, RealLeafCount: rec.RealLeafCount, PaddedLeafCount: rec.PaddedLeafCount, PieceBytes: rec.PieceBytes,
		Archivers: rec.Archivers, Operators: rec.Operators,
	}
}

func TestGenesis_operatorsPairWithArchiversAndAreDistinct(t *testing.T) {
	gs := genesisWidth(100)
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

func TestGenesis_aRangeThatIsNotCanonicalIsRefused(t *testing.T) {
	for name, r := range map[string][2]int64{
		"wider than the width":    {1, 20},
		"shifted by one":          {2, 11},
		"narrower than the width": {1, 5},
	} {
		gs := genesisWidth(10)
		gs.Ranges = []types.RangeRecord{archivedRecord(r[0], r[1])}
		gs.LastArchivedHeight = types.ContiguousArchivedHeight(gs.Ranges)
		require.ErrorIs(t, gs.Validate(), types.ErrNotCanonicalRange, name)
	}
}

func TestParams_rangeBlocksMustBeWithinBounds(t *testing.T) {
	require.NoError(t, types.DefaultParams().Validate())
	require.Equal(t, types.DefaultRangeBlocks, types.DefaultParams().RangeBlocks)
	for _, width := range []int64{0, -1, types.MaxRangeBlocksLimit + 1} {
		p := types.DefaultParams()
		p.RangeBlocks = width
		require.Error(t, p.Validate(), "range_blocks %d", width)
	}
	for _, width := range []int64{1, types.MaxRangeBlocksLimit} {
		p := types.DefaultParams()
		p.RangeBlocks = width
		require.NoError(t, p.Validate(), "range_blocks %d", width)
	}
}

func TestGenesis_candidateNodeIdsPairWithArchivers(t *testing.T) {
	gs := genesisWidth(10)
	rec := undecidedRecord(1, 10)
	rec.Candidates[0].NodeIds = rec.Candidates[0].NodeIds[:1]
	gs.Ranges = []types.RangeRecord{rec}
	require.ErrorContains(t, gs.Validate(), "node ids")

	rec = undecidedRecord(1, 10)
	rec.Candidates[0].NodeIds[0] = ""
	gs.Ranges = []types.RangeRecord{rec}
	require.Error(t, gs.Validate())
}
