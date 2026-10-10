//go:build e2e_fleet

package clienvauthmisc

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

// dummyArchive is an archive path that names nothing: State.ArchivePath is
// empty on a fleet the run did not build (stagenet), and these commands are
// refused at validation or at the environment lookup, before any file is read.
const dummyArchive = "/nonexistent/e2e-orama.tar.gz"

// TestRollout_refusesBadFlagsBeforeBuilding: `orama maint rollout` and `orama node
// rollout` are the same command (docs/CLI_REFERENCE.md#orama-maint-rollout) and
// check their flags before building or pushing anything; a flag mistake is
// the usage code (exit 2) with the reason (production/rollout Flags.validate).
// Nothing here can roll out: every case is refused at validation.
func TestRollout_refusesBadFlagsBeforeBuilding(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t).NoWallet(t)
	cases := []struct {
		args []string
		want string
	}{
		{nil, "--env is required"},
		{[]string{"--env", f.State.Env, "--no-build"}, "--no-build needs --archive"},
		{[]string{"--env", f.State.Env, "--archive", dummyArchive}, "--archive is only for --no-build"},
		{[]string{"--env", f.State.Env, "--delay", "not-a-number"}, "delay"},
	}
	for _, cmd := range [][]string{{"maint", "rollout"}} {
		for _, c := range cases {
			res := run(t, cli, append(append([]string{}, cmd...), c.args...)...)
			if res.Exit != exitUsage || !strings.Contains(output(res), c.want) {
				t.Errorf("orama %v %v: exit %d, want %d and %q\n%s", cmd, c.args, res.Exit, exitUsage, c.want, output(res))
			}
			if strings.Contains(res.Stdout, "Step 1/3") {
				t.Errorf("orama %v %v started the pipeline before refusing it", cmd, c.args)
			}
		}
	}
}

// TestRollout_unknownEnvironmentReachesNoNode: a rollout of an environment
// that is not configured cannot push anywhere (the archive is given, so no
// build runs).
func TestRollout_unknownEnvironmentReachesNoNode(t *testing.T) {
	t.Parallel()
	harness.Fleet(t)
	res := run(t, harness.CLI(t).NoWallet(t), "maint", "rollout", "--env", e2eEnvPrefix+"absent", "--no-build", "--archive", dummyArchive)
	if res.Exit == exitOK {
		t.Fatalf("rollout to an unconfigured environment succeeded:\n%s", output(res))
	}
	if strings.Contains(res.Stdout, "Step 3/3") {
		t.Errorf("rollout to an unconfigured environment reached the upgrade step:\n%s", res.Stdout)
	}
}
