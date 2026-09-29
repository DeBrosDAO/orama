//go:build e2e_fleet

package clienvauthmisc

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// inspectBudget bounds one `orama inspect` of the fleet (an SSH round to
// every node, --timeout 30s by default, plus the checks).
const inspectBudget = 5 * time.Minute

// inspectSubsystems are the read-only subsystems every core node has.
const inspectSubsystems = "wg,system"

// inspectedSubsystems are the names the checks of inspectSubsystems report
// under ("wg" is an alias of "wireguard", core/pkg/inspector/checker.go).
var inspectedSubsystems = map[string]bool{"wireguard": true, "system": true}

// inspectReport is `orama inspect --format json` (core/pkg/inspector/report.go PrintJSON).
type inspectReport struct {
	Summary struct {
		Passed  int `json:"passed"`
		Failed  int `json:"failed"`
		Warned  int `json:"warned"`
		Skipped int `json:"skipped"`
		Total   int `json:"total"`
	} `json:"summary"`
	Checks []struct {
		ID        string `json:"id"`
		Subsystem string `json:"subsystem"`
		Status    string `json:"status"`
		Node      string `json:"node"`
	} `json:"checks"`
}

// jsonTail decodes the JSON document that starts at the first "{" of out.
func jsonTail(t testing.TB, out string) inspectReport {
	t.Helper()
	i := strings.Index(out, "{")
	if i < 0 {
		t.Fatalf("no JSON object in:\n%s", out)
	}
	var r inspectReport
	if err := json.Unmarshal([]byte(out[i:]), &r); err != nil {
		t.Fatalf("inspect JSON does not decode: %v\n%s", err, out)
	}
	return r
}

// TestInspect_checksEveryNodeOverSSH: inspect SSHes into every node of the
// environment and a healthy fresh fleet passes the checks (docs/CLI_REFERENCE.md#orama-inspect;
// a failed check is a failed command, inspect_command.go).
func TestInspect_checksEveryNodeOverSSH(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	res := infra.RunFor(t, harness.CLI(t), inspectBudget,
		"inspect", "--env", f.State.Env, "--subsystem", inspectSubsystems, "--format", "json")
	infra.ExpectExit(t, res, exitOK)
	r := jsonTail(t, res.Stdout)
	if r.Summary.Total == 0 || r.Summary.Total != len(r.Checks) || r.Summary.Failed != 0 {
		t.Fatalf("inspect summary %+v with %d checks", r.Summary, len(r.Checks))
	}
	for _, c := range r.Checks {
		if !inspectedSubsystems[c.Subsystem] {
			t.Errorf("check %s is subsystem %q, outside --subsystem %s", c.ID, c.Subsystem, inspectSubsystems)
		}
	}
	for _, n := range f.State.Nodes {
		found := false
		for _, c := range r.Checks {
			found = found || strings.Contains(c.Node, n.PublicIP)
		}
		if !found {
			t.Errorf("inspect reported nothing for %s (%s)", n.Name, n.PublicIP)
		}
	}
}

// TestInspect_jsonFormatIsPureJSON: --format json is for programs, so stdout
// must be one JSON document; a banner before it breaks every consumer.
func TestInspect_jsonFormatIsPureJSON(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	res := infra.RunFor(t, harness.CLI(t), inspectBudget,
		"inspect", "--env", f.State.Env, "--subsystem", "system", "--format", "json")
	infra.ExpectExit(t, res, exitOK)
	var v map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &v); err != nil {
		t.Errorf("inspect --format json stdout is not one JSON document (%v): starts %q",
			err, res.Stdout[:min(len(res.Stdout), 80)])
	}
}

// TestInspect_writesResultsDirectory: --output saves the results as markdown
// in the directory (docs/CLI_REFERENCE.md#orama-inspect).
func TestInspect_writesResultsDirectory(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	dir := t.TempDir()
	res := infra.RunFor(t, harness.CLI(t), inspectBudget,
		"inspect", "--env", f.State.Env, "--subsystem", "system", "--output", dir)
	infra.ExpectExit(t, res, exitOK, "Results saved to")
	// <dir>/<env>/<timestamp>/summary.md (core/pkg/inspector/results_writer.go).
	md, err := filepath.Glob(filepath.Join(dir, f.State.Env, "*", "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(md) != 1 {
		t.Errorf("inspect --output %s wrote %v, want one <env>/<timestamp>/summary.md", dir, md)
	}
}

// TestInspect_unwritableOutputFails: results that could not be saved are a
// failed command, not a warning and exit 0.
func TestInspect_unwritableOutputFails(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	res := infra.RunFor(t, harness.CLI(t), inspectBudget,
		"inspect", "--env", f.State.Env, "--subsystem", "system", "--output", "/dev/null/e2e-results")
	if res.Exit == exitOK {
		t.Errorf("inspect succeeded although --output could not be written:\n%s", output(res))
	}
}

// TestInspect_badArgumentsAreUsage: a missing --env, an unknown subsystem or
// output format are mistakes on the command line (exit 2), found before any
// SSH (inspect_command.go RunInspect "--env is required").
func TestInspect_badArgumentsAreUsage(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	for label, args := range map[string][]string{
		"no env":            {"inspect"},
		"unknown subsystem": {"inspect", "--env", f.State.Env, "--subsystem", "e2e-nope"},
		"unknown format":    {"inspect", "--env", f.State.Env, "--format", "yaml"},
		"negative timeout":  {"inspect", "--env", f.State.Env, "--timeout", "-1s"},
	} {
		res := infra.RunFor(t, cli, inspectBudget, args...)
		if res.Exit != exitUsage {
			t.Errorf("inspect with %s: exit %d, want %d\n%s", label, res.Exit, exitUsage, output(res))
		}
	}
}

// TestInspect_unknownEnvironmentFindsNoNodes: an environment that is not
// configured has no nodes to SSH into.
func TestInspect_unknownEnvironmentFindsNoNodes(t *testing.T) {
	t.Parallel()
	res := infra.RunFor(t, isolated(t), inspectBudget, "inspect", "--env", e2eEnvPrefix+"absent")
	if res.Exit == exitOK {
		t.Fatalf("inspect of an unconfigured environment succeeded:\n%s", output(res))
	}
}
