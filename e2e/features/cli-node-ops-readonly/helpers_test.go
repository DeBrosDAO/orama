//go:build e2e_fleet

package clinodeopsreadonly

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Exit codes (core/cmd/orama/internal/clierr).
const (
	exitOK       = infra.ExitOK
	exitUsage    = infra.ExitUsage
	exitAuth     = infra.ExitAuth
	exitNotFound = infra.ExitNotFound
)

// Budgets.
const (
	// cliBudget bounds one read-only command that SSHes into every node.
	cliBudget = 5 * time.Minute
	// telemetryBudget: node reports are gathered every 10s and a view may be
	// served from a gateway whose snapshot is up to MaxReportAgeSec old
	// (website/src/docs/operator/monitoring.mdx), so a change shows within about a minute.
	telemetryBudget = 3 * time.Minute
	pollEvery       = 5 * time.Second
)

// documentAddr is an address from TEST-NET-1 (RFC 5737): never a node.
const documentAddr = "192.0.2.1"

func run(t testing.TB, cli *oramacli.Runner, args ...string) oramacli.Result {
	t.Helper()
	return infra.RunFor(t, cli, cliBudget, args...)
}

func output(res oramacli.Result) string { return res.Stdout + res.Stderr }

// decode decodes a --json result or fails the test.
func decode(t testing.TB, res oramacli.Result, v any) {
	t.Helper()
	if err := oramacli.DecodeJSON(res, v); err != nil {
		t.Fatalf("orama %v --json: %v\n%s", res.Args, err, res.Stdout)
	}
}

// raw decodes a --json result into generic JSON.
func raw(t testing.TB, res oramacli.Result) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(res.Stdout), &v); err != nil {
		t.Fatalf("orama %v --json printed no JSON: %v\n%s", res.Args, err, res.Stdout)
	}
	return v
}

// jsonOf decodes a successful --json result, for eventually loops: a
// non-zero exit or text output is the observation, not a test failure.
func jsonOf(res oramacli.Result, v any) error {
	if res.Exit != exitOK {
		return fmt.Errorf("orama %v exited %d: %s", res.Args, res.Exit, res.Stderr)
	}
	return oramacli.DecodeJSON(res, v)
}
