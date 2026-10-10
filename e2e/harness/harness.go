// Package harness is what every feature package's TestMain calls. It loads the
// run's fleet state, sets up evidence recording, and hands tests the fleet,
// the CLI and the gateway client.
//
//	func TestMain(m *testing.M) { harness.Main(m) }
package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/e2e/harness/pace"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// NotApplicablePrefix starts the message of every SkipNotApplicable skip. The
// report counts every skip as not covered, this one included.
const NotApplicablePrefix = "not applicable: "

// NotInFleetMode is the skip message outside a fleet run in non-strict mode.
const NotInFleetMode = "not in fleet mode: " + config.EnvState + " is not set (see e2e/README.md)"

// Exit codes of Main when it refuses to run the tests.
const (
	exitRefused = 1
	exitBadEnv  = 2
)

var current struct {
	fleet *fleet.Fleet
	// workDir is the run's work dir, beside the state file.
	workDir string
}

// Main runs a feature package. In strict mode (E2E_STRICT=1, which the runner
// always sets) a package started outside a fleet run fails at once, so a
// misconfigured run cannot report green by skipping everything. Outside
// strict mode every test skips with NotInFleetMode.
func Main(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	mode, err := config.FromEnv(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return exitBadEnv
	}
	if mode.StatePath == "" {
		if mode.Strict {
			fmt.Fprintf(os.Stderr, "e2e: %s is not set and %s=1: feature tests only run against a fleet (make e2e-fleet); refusing to skip them all\n",
				config.EnvState, config.EnvStrict)
			return exitRefused
		}
		return m.Run()
	}
	realHome, err := secrets.RealHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return exitRefused
	}
	f, err := load(mode.StatePath, realHome, os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		return exitRefused
	}
	current.fleet = f
	current.workDir = filepath.Dir(mode.StatePath)
	stop := watchInterrupt()
	defer stop()
	return m.Run()
}

// load reads the state, refuses one that fails the run guards, and opens
// this package's evidence file, named after the feature directory (go test
// runs a package in its own directory), in the directory the runner gave this
// package run (E2E_EVIDENCE_DIR) or the run's shared evidence dir. The
// redactor knows the run's secrets and every credential minted so far, and
// adds the ones this package mints to the run's token registry.
func load(statePath, realHome string, lookup func(string) (string, bool)) (*fleet.Fleet, error) {
	st, err := fleet.Load(statePath)
	if err != nil {
		return nil, err
	}
	if err := fleet.CheckState(st, realHome); err != nil {
		return nil, fmt.Errorf("refusing the fleet state %s: %w", statePath, err)
	}
	// Invalid pacing budgets fail the package here, once, instead of in
	// every gateway client and CLI invocation of it.
	if _, err := pace.FromEnv(lookup); err != nil {
		return nil, fmt.Errorf("refusing the credential pacing: %w", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to find the feature directory: %w", err)
	}
	evDir := filepath.Join(st.ArtifactDir, evidence.DirName)
	if dir, ok := lookup(config.EnvEvidenceDir); ok && dir != "" {
		if !filepath.IsAbs(dir) {
			return nil, fmt.Errorf("%s=%q must be an absolute path", config.EnvEvidenceDir, dir)
		}
		evDir = dir
	}
	red, err := secrets.ForRun(lookup, statePath)
	if err != nil {
		return nil, err
	}
	red.PersistTo(secrets.RegistryPath(statePath))
	rec, err := evidence.New(evDir, filepath.Base(wd), red)
	if err != nil {
		return nil, err
	}
	return fleet.New(st, rec), nil
}

// Fleet returns the run's fleet; outside a fleet run (non-strict) it skips.
// Once the run is interrupted it fails instead: a test that has not started
// yet must not start disturbing the fleet.
func Fleet(t testing.TB) *fleet.Fleet {
	t.Helper()
	if current.fleet == nil {
		t.Skip(NotInFleetMode)
	}
	if interrupted.Load() {
		t.Fatal(InterruptedMessage)
	}
	return current.fleet
}

// CLI returns the run's orama CLI, attributed to t. Its RunOpts.Dir may be
// inside its HOME or inside the run's work dir (WorkTemp).
func CLI(t testing.TB) *oramacli.Runner {
	t.Helper()
	f := Fleet(t)
	r := oramacli.ForState(f.State, f.Recorder()).For(t)
	r.WorkDir = current.workDir
	return r
}

// WorkTemp is a fresh directory under the run's work dir, removed when the
// test ends: a working directory the CLI may run in (oramacli.RunOpts.Dir),
// e.g. a copy of the source tree for `orama maint build`.
func WorkTemp(t testing.TB) string {
	t.Helper()
	Fleet(t)
	dir, err := os.MkdirTemp(current.workDir, "e2e-tmp-")
	if err != nil {
		t.Fatalf("failed to create a directory in the run's work dir %s: %v", current.workDir, err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("cleanup: failed to remove %s: %v", dir, err)
		}
	})
	return dir
}

// GW returns the public gateway client, attributed to t.
func GW(t testing.TB) *gw.Client {
	t.Helper()
	return gw.ForFleet(t, Fleet(t))
}

// SkipNotApplicable skips a test that cannot apply to this run, with a reason.
// It is the only skip feature tests may use; the report lists it as not
// covered, so the reason must say what would make the test apply.
func SkipNotApplicable(t testing.TB, reason string) {
	t.Helper()
	t.Skip(NotApplicablePrefix + reason)
}

// RequireChain skips (not covered) when the run has no co-hosted chain.
func RequireChain(t testing.TB) {
	t.Helper()
	if Fleet(t).State.ChainID == "" {
		SkipNotApplicable(t, "the run has no chain (state.chain_id is empty); provision with chain validators")
	}
}

// RequireArchive skips (not covered) on the stagenet target when archive, a
// path from the run's state (ArchivePath, PreviousArchivePath), is empty: the
// stagenet state is written without building, so it carries no release
// archive. On a fleet run an empty path is a real fault and is left to fail.
func RequireArchive(t testing.TB, archive string) {
	t.Helper()
	if archive == "" && Fleet(t).State.IsStagenet() {
		SkipNotApplicable(t, "the stagenet state carries no release archive (the run did not build one): the existing cluster is only tested")
	}
}
