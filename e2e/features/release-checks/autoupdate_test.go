//go:build e2e_fleet

package releasechecks

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// autoupdate runs `orama node autoupdate` with the version pair and flags.
func autoupdate(t testing.TB, current, candidate string, flags ...string) (int, string) {
	t.Helper()
	args := append([]string{"node", "autoupdate", "--current", current, "--candidate", candidate}, flags...)
	res := infra.Run(t, harness.CLI(t), args...)
	return res.Exit, strings.TrimSpace(res.Stdout + res.Stderr)
}

// TestAutoupdate_decisions: what the cluster should do with a candidate
// release, one line "<action>: <reason>" (docs/CLI_REFERENCE.md "orama node
// autoupdate"): notify by default, auto installs only when healthy, newer
// and inside the window, and every TUF failure, a downgrade and a release
// marked bad are refused.
func TestAutoupdate_decisions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, cur, cand string
		flags           []string
		want            string
	}{
		{"newer notify by default", "1.2.3", "1.2.4", nil, "notify: newer release 1.2.4 (notify)"},
		{"newer auto healthy no window", "1.2.3", "1.3.0", []string{"--mode", "auto"}, "upgrade: newer release 1.3.0"},
		{"same version", "1.2.3", "1.2.3", []string{"--mode", "auto"}, "none: nothing to install"},
		{"mode off", "1.2.3", "2.0.0", []string{"--mode", "off"}, "none: nothing to install"},
		{"downgrade", "1.2.3", "1.2.2", []string{"--mode", "auto"}, "refuse: release 1.2.2 is older than 1.2.3"},
		{"marked bad", "1.2.3", "1.2.4", []string{"--mode", "auto", "--bad"}, "refuse: release 1.2.4 is marked bad"},
		{"degraded", "1.2.3", "1.2.4", []string{"--mode", "auto", "--degraded"}, "refuse: cluster is degraded or below quorum"},
		{"below quorum", "1.2.3", "1.2.4", []string{"--mode", "auto", "--voters", "3", "--healthy-voters", "1"}, "refuse: cluster is degraded or below quorum"},
		{"exactly half is below quorum", "1.2.3", "1.2.4", []string{"--mode", "auto", "--voters", "4", "--healthy-voters", "2"}, "refuse: cluster is degraded or below quorum"},
		{"tuf rollback", "1.2.3", "1.2.4", []string{"--mode", "auto", "--verify", "rollback"}, "refuse: rolled-back release metadata"},
		{"tuf freeze", "1.2.3", "1.2.4", []string{"--verify", "freeze"}, "refuse: frozen release metadata"},
		{"tuf threshold", "1.2.3", "1.2.4", []string{"--verify", "threshold"}, "refuse: release signature set is below threshold"},
		{"tuf hash", "1.2.3", "1.2.4", []string{"--verify", "hash"}, "refuse: release archive does not match its targets metadata"},
		{"verify beats bad", "1.2.3", "1.2.4", []string{"--bad", "--verify", "hash"}, "refuse: release archive does not match its targets metadata"},
		{"v prefix and length", "v1.2", "1.2.0.1", []string{"--mode", "auto"}, "upgrade: newer release 1.2.0.1"},
		{"validator notify", "1.2.3", "1.2.4", []string{"--role", "validator"}, "notify: newer release 1.2.4 (notify)"},
		// A validator never installs a release by itself: on auto it skips, with the
		// way to upgrade it, and the rollout counts it as done (docs/DEV_DEPLOY.md, "Auto-update").
		{"validator auto", "1.2.3", "1.2.4", []string{"--mode", "auto", "--role", "validator"}, "skip: release 1.2.4 is not installed here: this machine is a validator, upgrade it by hand ('orama global stage-oramad')"},
	}
	for _, c := range cases {
		exit, out := autoupdate(t, c.cur, c.cand, c.flags...)
		if exit != infra.ExitOK || out != c.want {
			t.Errorf("%s: exit %d %q, want %q", c.name, exit, out, c.want)
		}
	}
}

// TestAutoupdate_windowDecides: auto with a maintenance window that excludes
// every hour but one installs only in that hour; a window of the same start
// and end is always open.
func TestAutoupdate_windowDecides(t *testing.T) {
	t.Parallel()
	exit, out := autoupdate(t, "1.0.0", "1.0.1", "--mode", "auto", "--window", "5-5")
	if exit != 0 || out != "upgrade: newer release 1.0.1" {
		t.Errorf("an always-open window: exit %d %q", exit, out)
	}
	upgrades, notifies := 0, 0
	for _, w := range []string{"0-12", "12-0"} {
		_, out := autoupdate(t, "1.0.0", "1.0.1", "--mode", "auto", "--window", w)
		switch out {
		case "upgrade: newer release 1.0.1":
			upgrades++
		case "notify: newer release 1.0.1 is outside the maintenance window":
			notifies++
		default:
			t.Errorf("window %s: %q", w, out)
		}
	}
	if upgrades != 1 || notifies != 1 {
		t.Errorf("two complementary windows gave %d upgrades and %d notifies, want one each", upgrades, notifies)
	}
}

// TestAutoupdate_refusals: every input the decision cannot use is a usage
// error, never a decision: missing versions, a non-numeric or leading-zero
// version, an unknown mode, role or TUF failure, a malformed or out-of-range
// window. (A validator on auto is a decision, a skip: see the table above.)
func TestAutoupdate_refusals(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{"node", "autoupdate", "--candidate", "1.0.1"},
		{"node", "autoupdate", "--current", "1.0.0"},
		{"node", "autoupdate", "--current", "1.0.0", "--candidate", "1.0.x"},
		{"node", "autoupdate", "--current", "1.0.0", "--candidate", "1.01.0"},
		{"node", "autoupdate", "--current", "", "--candidate", "1.0.1"},
		{"node", "autoupdate", "--current", "1.0.0", "--candidate", "1.0.1", "--mode", "yolo"},
		{"node", "autoupdate", "--current", "1.0.0", "--candidate", "1.0.1", "--role", "god"},
		{"node", "autoupdate", "--current", "1.0.0", "--candidate", "1.0.1", "--verify", "maybe"},
		{"node", "autoupdate", "--current", "1.0.0", "--candidate", "1.0.1", "--window", "night"},
		{"node", "autoupdate", "--current", "1.0.0", "--candidate", "1.0.1", "--window", "1-24"},
		{"node", "autoupdate", "--current", "1.0.0", "--candidate", "\u202e1.0.1"},
	}
	for _, args := range cases {
		res := infra.Run(t, harness.CLI(t), args...)
		if res.Exit != infra.ExitUsage {
			t.Errorf("orama %v: exit %d, want %d (usage): %s%s", args, res.Exit, infra.ExitUsage, res.Stdout, res.Stderr)
		}
	}
}
