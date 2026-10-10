package checks

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestTermConsistency_sameTermPasses(t *testing.T) {
	if got := termConsistency(map[uint64][]string{21: {"a", "b", "c"}}); got.Status != inspector.StatusPass {
		t.Fatalf("one term everywhere: %+v", got)
	}
}

func TestTermConsistency_oneApartIsAnElectionNotDivergence(t *testing.T) {
	if got := termConsistency(map[uint64][]string{21: {"a", "b"}, 22: {"c"}}); got.Status != inspector.StatusWarn {
		t.Fatalf("terms 21 and 22 read one after another: want a warning, got %+v", got)
	}
}

func TestTermConsistency_twoApartFails(t *testing.T) {
	if got := termConsistency(map[uint64][]string{5: {"a", "b"}, 7: {"c"}}); got.Status != inspector.StatusFail {
		t.Fatalf("terms 5 and 7: want a failure, got %+v", got)
	}
}

func TestTermConsistency_unknownTermIsLeftOut(t *testing.T) {
	if got := termConsistency(map[uint64][]string{0: {"old"}, 21: {"a", "b"}}); got.Status != inspector.StatusPass {
		t.Fatalf("a node without a term must not read as divergence: %+v", got)
	}
	if got := termConsistency(map[uint64][]string{0: {"a", "b"}}); got.Status != inspector.StatusSkip {
		t.Fatalf("no term anywhere: want skip, got %+v", got)
	}
}
