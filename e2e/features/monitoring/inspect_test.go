//go:build e2e_fleet

package monitoring

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// inspectBudget bounds one inspection of the fleet over SSH.
const inspectBudget = 10 * time.Minute

// The inspector's subsystems, statuses and severities (docs/INSPECTOR.md;
// core/pkg/inspector/checker.go: Low=0 .. Critical=3).
var (
	inspectSubsystems = []string{"rqlite", "olric", "ipfs", "dns", "wireguard", "system", "network", "namespace", "tor", "webrtc", "global"}
	inspectStatuses   = []string{"pass", "fail", "warn", "skip"}
)

const maxSeverity = 3

type inspectCheck struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Subsystem string `json:"subsystem"`
	Severity  int    `json:"severity"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	Node      string `json:"node"`
}

type inspectReport struct {
	Summary struct {
		Passed, Failed, Warned, Skipped, Total int
		Seconds                                float64 `json:"duration_seconds"`
	} `json:"summary"`
	Checks []inspectCheck `json:"checks"`
}

// inspect runs `orama maint inspect --format json` and decodes the report. The
// JSON is read from its first '{': the command prints an "Inspecting N
// nodes..." line to stdout ahead of it (TestInspect_jsonStdoutIsOnlyJSON).
func inspect(t *testing.T, extra ...string) (inspectReport, oramacli.Result) {
	t.Helper()
	args := append([]string{"inspect", "--env", harness.Fleet(t).State.Env, "--format", "json"}, extra...)
	res := infra.RunFor(t, harness.CLI(t), inspectBudget, args...)
	var r inspectReport
	i := strings.Index(res.Stdout, "{")
	if i < 0 {
		t.Fatalf("orama maint inspect printed no JSON (exit %d):\n%s%s", res.Exit, res.Stdout, res.Stderr)
	}
	if err := json.Unmarshal([]byte(res.Stdout[i:]), &r); err != nil {
		t.Fatalf("orama maint inspect JSON: %v\n%s", err, res.Stdout)
	}
	return r, res
}

// TestInspect_allSubsystemsConsistentReport: a full inspection over SSH
// covers the fleet, every check is well formed, the summary adds up, and
// the exit code is 1 exactly when a check failed (docs/INSPECTOR.md "Exit
// Codes", "JSON").
func TestInspect_allSubsystemsConsistentReport(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	r, res := inspect(t)
	s := r.Summary
	if s.Total != len(r.Checks) || s.Passed+s.Failed+s.Warned+s.Skipped != s.Total || s.Total == 0 {
		t.Errorf("summary %+v does not add up to %d checks", s, len(r.Checks))
	}
	if (res.Exit == infra.ExitFailure) != (s.Failed > 0) || (res.Exit != infra.ExitOK && res.Exit != infra.ExitFailure) {
		t.Errorf("exit %d with %d failed checks, want 1 exactly when one failed", res.Exit, s.Failed)
	}
	seenNodes := map[string]bool{}
	for _, c := range r.Checks {
		if c.ID == "" || c.Name == "" || !slices.Contains(inspectSubsystems, c.Subsystem) ||
			!slices.Contains(inspectStatuses, c.Status) || c.Severity < 0 || c.Severity > maxSeverity {
			t.Errorf("malformed check %+v", c)
		}
		seenNodes[c.Node] = true
	}
	for _, n := range f.State.Nodes {
		found := false
		for node := range seenNodes {
			found = found || strings.HasSuffix(node, "@"+n.PublicIP) || strings.HasSuffix(node, "@"+n.WGIP)
		}
		if !found {
			t.Errorf("no check names %s", n.Name)
		}
	}
	for _, c := range r.Checks {
		if c.Status == "fail" && c.Severity == maxSeverity {
			t.Errorf("critical failure on the healthy cluster: %+v", c)
		}
	}
}

// TestInspect_subsystemFilter: --subsystem narrows the checks to what was
// asked, "wg" is an alias of wireguard, and each asked subsystem yields a
// check.
func TestInspect_subsystemFilter(t *testing.T) {
	t.Parallel()
	r, _ := inspect(t, "--subsystem", "rqlite,dns,wg")
	seen := map[string]bool{}
	for _, c := range r.Checks {
		if !slices.Contains([]string{"rqlite", "dns", "wireguard"}, c.Subsystem) {
			t.Errorf("--subsystem rqlite,dns,wg returned a %s check", c.Subsystem)
		}
		seen[c.Subsystem] = true
	}
	for _, want := range []string{"rqlite", "dns", "wireguard"} {
		if !seen[want] {
			t.Errorf("no %s check", want)
		}
	}
}

// TestInspect_refusals: --env is required (usage); an unknown subsystem is
// an error, not an empty and therefore green inspection. The second is not
// validated today (core/cmd/orama/internal/inspect_command.go splits the
// flag and runs whatever matches): this test fails until it is.
func TestInspect_refusals(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	infra.ExpectExit(t, infra.RunFor(t, cli, inspectBudget, "maint", "inspect", "--config", "/nonexistent/nodes.conf"), infra.ExitUsage, "--env is required")
	res := infra.RunFor(t, cli, inspectBudget, "maint", "inspect", "--env", harness.Fleet(t).State.Env, "--subsystem", "nosuch", "--format", "json")
	if res.Exit == infra.ExitOK {
		t.Errorf("an unknown subsystem inspected nothing and exited 0:\n%s", res.Stdout)
	}
}

// TestInspect_jsonStdoutIsOnlyJSON: `--format json` is for piping
// (docs/INSPECTOR.md: `orama maint inspect --format json | jq ...`), so stdout must
// be the JSON document alone. Today a progress line precedes it on stdout
// (core/cmd/orama/internal/inspect_command.go fmt.Printf("Inspecting ...")):
// this test fails until it moves to stderr.
func TestInspect_jsonStdoutIsOnlyJSON(t *testing.T) {
	t.Parallel()
	_, res := inspect(t, "--subsystem", "system")
	var v map[string]any
	if err := oramacli.DecodeJSON(res, &v); err != nil {
		t.Fatalf("stdout of --format json is not one JSON document: %v\n%.300s", err, res.Stdout)
	}
}

// TestInspect_tableFormat: the default table ends with the summary line.
func TestInspect_tableFormat(t *testing.T) {
	t.Parallel()
	res := infra.RunFor(t, harness.CLI(t), inspectBudget, "maint", "inspect", "--env", harness.Fleet(t).State.Env, "--subsystem", "wireguard")
	if !strings.Contains(res.Stdout, "Summary: ") || !strings.Contains(res.Stdout, "## WIREGUARD") {
		t.Errorf("table output lacks the section or the summary:\n%s", res.Stdout)
	}
}
