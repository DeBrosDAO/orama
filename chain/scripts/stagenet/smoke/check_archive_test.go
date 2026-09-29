package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

var threeOps = []string{"op1", "op2", "op3"}

func TestDistinctASNs(t *testing.T) {
	require.Equal(t, 1, distinctASNs([]uint32{16276, 16276, 16276}))
	require.Equal(t, 3, distinctASNs([]uint32{1, 2, 3}))
	require.Equal(t, 0, distinctASNs([]uint32{0, 0}), "an undeclared ASN is not a distinct one")
	require.Equal(t, 0, distinctASNs(nil))
}

func TestArchiveVerdict_tooEarlyIsASkipNotAFailure(t *testing.T) {
	r := archiveVerdict(archiveInput{Tip: 400, Width: 1000, Replicas: 3})
	require.Equal(t, Skip, r.Status)
	require.Contains(t, r.Detail, "400")
}

func TestArchiveVerdict_finalRangeNobodyAttestedFails(t *testing.T) {
	r := archiveVerdict(archiveInput{Tip: 1500, Width: 1000, Replicas: 3})
	require.Equal(t, Fail, r.Status)
	require.Contains(t, r.Detail, "no archiver attested")
}

func TestArchiveVerdict_needsThreeDistinctOperators(t *testing.T) {
	r := archiveVerdict(archiveInput{Tip: 1500, Width: 1000, RangeFound: true, Operators: []string{"op1", "op1", "op2"}, Replicas: 3})
	require.Equal(t, Fail, r.Status)
	require.Contains(t, r.Detail, "2 operators")
}

func TestArchiveVerdict_archivedPasses(t *testing.T) {
	r := archiveVerdict(archiveInput{Tip: 1500, Width: 1000, RangeFound: true, Operators: threeOps, Archived: true, Replicas: 3})
	require.Equal(t, Pass, r.Status)
}

func TestArchiveVerdict_archivedPassesEvenOnASharedASN(t *testing.T) {
	r := archiveVerdict(archiveInput{Tip: 1500, Width: 1000, RangeFound: true, Operators: threeOps, Archived: true,
		ProviderASNs: []uint32{16276, 16276, 16276}, Replicas: 3})
	require.Equal(t, Pass, r.Status)
}

func TestArchiveVerdict_attestedButNoDealsFails(t *testing.T) {
	r := archiveVerdict(archiveInput{Tip: 1500, Width: 1000, RangeFound: true, Operators: threeOps, Replicas: 3})
	require.Equal(t, Fail, r.Status)
	require.Contains(t, r.Detail, "no ARCHIVE deal")
}

func TestArchiveVerdict_unassignedDealsOnASharedASNAreAnEnvironmentSkip(t *testing.T) {
	r := archiveVerdict(archiveInput{Tip: 1500, Width: 1000, RangeFound: true, Operators: threeOps,
		Deals:        []archiveDeal{{ID: 7, Assigned: 0, Replicas: 3}, {ID: 8, Assigned: 0, Replicas: 3}},
		ProviderASNs: []uint32{16276, 16276, 16276}, Replicas: 3})
	require.Equal(t, Skip, r.Status)
	require.Contains(t, r.Detail, "environment")
	require.Contains(t, r.Detail, "1 distinct ASN")
	require.Contains(t, r.Detail, "16276")
}

func TestArchiveVerdict_unassignedDealsWithDistinctASNsIsAFailure(t *testing.T) {
	r := archiveVerdict(archiveInput{Tip: 1500, Width: 1000, RangeFound: true, Operators: threeOps,
		Deals:        []archiveDeal{{ID: 7, Assigned: 1, Replicas: 3}},
		ProviderASNs: []uint32{1, 2, 3}, Replicas: 3})
	require.Equal(t, Fail, r.Status, "the ASN condition is not the cause here, so it must not be excused")
	require.Contains(t, r.Detail, "not archived")
}

func TestArchiveVerdict_assignedDealsButNotArchivedIsAFailureEvenOnASharedASN(t *testing.T) {
	r := archiveVerdict(archiveInput{Tip: 1500, Width: 1000, RangeFound: true, Operators: threeOps,
		Deals:        []archiveDeal{{ID: 7, Assigned: 3, Replicas: 3}},
		ProviderASNs: []uint32{16276, 16276, 16276}, Replicas: 3})
	require.Equal(t, Fail, r.Status)
}
