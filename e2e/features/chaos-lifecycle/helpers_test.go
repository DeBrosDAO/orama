//go:build e2e_fleet

package chaoslifecycle

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const (
	pollEvery      = 5 * time.Second
	linePoll       = 200 * time.Millisecond
	convergeBudget = infra.ConvergeBudget
	// lineBudget bounds a long-running command reaching the step a fault is
	// injected at.
	lineBudget = 15 * time.Minute
)

// waitLine reads p's output until a line contains want and returns it. The
// lines before it are kept by the Proc for its Result.
func waitLine(t testing.TB, p *oramacli.Proc, want string) string {
	t.Helper()
	lines := p.StdoutLines()
	var found string
	err := eventually.Poll(t.Context(), linePoll, lineBudget, "the command to print "+want, func() (bool, error) {
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					return false, eventually.Stop(fmt.Errorf("the command ended before printing %q", want))
				}
				if strings.Contains(l, want) {
					found = l
					return true, nil
				}
			default:
				return false, nil
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// finish waits for p and fails when it does not end within budget.
func finish(t testing.TB, p *oramacli.Proc, budget time.Duration) oramacli.Result {
	t.Helper()
	done := make(chan oramacli.Result, 1)
	go func() {
		res, _ := p.Wait()
		done <- res
	}()
	var res oramacli.Result
	err := eventually.Poll(t.Context(), linePoll, budget, "the interrupted command to end", func() (bool, error) {
		select {
		case res = <-done:
			return true, nil
		default:
			return false, nil
		}
	})
	if err != nil {
		_ = p.Kill()
		t.Fatalf("the command hung after the fault: %v", err)
	}
	return res
}

// healed waits for the core cluster to converge.
func healed(t testing.TB, what string) {
	t.Helper()
	infra.WaitConverged(t, len(harness.Fleet(t).State.Nodes), convergeBudget, what)
}

// start runs args as a long-running CLI command; a cleanup reaps it.
func start(t testing.TB, cli *oramacli.Runner, args ...string) *oramacli.Proc {
	t.Helper()
	p, err := cli.Start(t.Context(), args...)
	if err != nil {
		t.Fatalf("starting orama %s: %v", strings.Join(args, " "), err)
	}
	t.Cleanup(func() {
		_ = p.Kill()
		_, _ = p.Wait()
	})
	return p
}
