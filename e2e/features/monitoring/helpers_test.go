//go:build e2e_fleet

package monitoring

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Values `orama status` writes (core/pkg/telemetry/cluster).
const (
	severityCritical = "critical"
	severityWarning  = "warning"
	severityInfo     = "info"
	nodeStatusOK     = "ok"
	// verdictMarks start every table view's verdict line (website/src/docs/operator/monitoring.mdx
	// "The verdict line").
	verdictOK  = "✓"
	verdictBad = "✗"
)

// monitorArgs is `orama status <view> --env <env>` plus extra.
func monitorArgs(t *testing.T, view string, extra ...string) []string {
	t.Helper()
	args := []string{"status"}
	if view != "" {
		args = append(args, view)
	}
	return append(append(args, "--env", harness.Fleet(t).State.Env), extra...)
}

// monitorJSON runs a one-shot view with --json and decodes it into v.
func monitorJSON(t *testing.T, view string, v any, extra ...string) {
	t.Helper()
	res := harness.CLI(t).MustOK(t, monitorArgs(t, view, append([]string{"--json"}, extra...)...)...)
	if err := oramacli.DecodeJSON(res, v); err != nil {
		t.Fatalf("orama status %s --json: %v\n%s", view, err, res.Stdout)
	}
}

// startsWithVerdict reports whether a table view's first line is the verdict.
func startsWithVerdict(out string) bool {
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	return strings.HasPrefix(first, verdictOK) || strings.HasPrefix(first, verdictBad)
}

// operator makes a fresh namespace owner an operator of the cluster for t
// (`orama maint operator add`, removed at t's cleanup), so the telemetry tests can
// call the operator API over HTTP with a session of their own. The owner's
// session carries the namespace's admin grant, the other half of the
// requirement (docs/whitepaper/technical-reference/appendices/i-api-surface.md "/v1/operator/telemetry"). Only
// TestTelemetry_asOperator calls it, once, and shares the result with its
// parallel subtests, so the package changes the operator list once.
func operator(t *testing.T) *ns.Namespace {
	t.Helper()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	addr := n.Owner.Wallet.Address()
	cli := harness.CLI(t)
	infra.ExpectExit(t, infra.Run(t, cli, "maint", "operator", "add", addr), infra.ExitOK)
	t.Cleanup(func() {
		ctx, cancel := fleet.CleanupContext(t)
		defer cancel()
		if res, err := cli.Run(ctx, "maint", "operator", "remove", addr); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: removing operator %s failed: %v %s", addr, err, res.Stderr)
		}
	})
	return n
}

// monitorJSONArgs runs `orama <args>` (which must print JSON) and decodes it.
func monitorJSONArgs(t *testing.T, v any, args ...string) {
	t.Helper()
	res := harness.CLI(t).MustOK(t, args...)
	if err := oramacli.DecodeJSON(res, v); err != nil {
		t.Fatalf("orama %s: %v\n%s", strings.Join(args, " "), err, res.Stdout)
	}
}
