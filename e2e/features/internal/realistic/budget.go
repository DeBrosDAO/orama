//go:build e2e_fleet

package realistic

import (
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

// StageRemaining is what is left of the package's stage budget. The runner
// hands go test a -timeout of the stage timeout plus stages.StopGrace and
// interrupts the package when the stage timeout is spent (the grace is for
// the cleanups then), so the budget ends StopGrace before the test binary's
// deadline. ok is false without a deadline (a local run with -timeout 0).
func StageRemaining(t *testing.T) (left time.Duration, ok bool) {
	t.Helper()
	deadline, ok := t.Deadline()
	if !ok {
		return 0, false
	}
	return time.Until(deadline) - stages.StopGrace, true
}

// FitsStageBudget reports whether worst (a fault, the restore and the
// convergence after it) still fits in the stage budget.
func FitsStageBudget(t *testing.T, worst time.Duration) bool {
	t.Helper()
	left, ok := StageRemaining(t)
	return !ok || left >= worst
}

// RequireFaultBudget fails t before it injects a fault whose worst case does
// not fit in what is left of the stage budget: past the budget the runner
// interrupts the package, possibly mid-fault.
func RequireFaultBudget(t *testing.T, what string, worst time.Duration) {
	t.Helper()
	if left, _ := StageRemaining(t); !FitsStageBudget(t, worst) {
		t.Fatalf("not starting %s: %v of the stage budget left, its worst case is %v; raise the stage timeout in e2e/stages/stages.yaml",
			what, left.Round(time.Second), worst)
	}
}
