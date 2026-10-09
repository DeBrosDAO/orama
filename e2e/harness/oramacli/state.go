package oramacli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// ConfigDirName and EnvironmentsFile are where the CLI keeps its environment
// list under HOME (core/pkg/auth, cmd/orama/internal/environment.go).
const (
	ConfigDirName    = ".orama"
	EnvironmentsFile = "environments.json"
)

// ForState returns the runner for the run's CLI under test, signing as the
// run's operator wallet.
func ForState(st *fleet.State, rec *evidence.Recorder) *Runner {
	return &Runner{Bin: st.OramaBin, Home: st.Home, AgentSock: st.RWSock, Target: st.Target, Recorder: rec,
		Wallet: st.OperatorAddress}
}

// ForPreviousRelease returns the runner for the previous release's CLI, for
// upgrade tests. It fails the test when the run built none.
func ForPreviousRelease(t testing.TB, st *fleet.State, rec *evidence.Recorder) *Runner {
	t.Helper()
	if st.PreviousOramaBin == "" {
		t.Fatal("the run has no previous release CLI (state.previous_orama_bin is empty)")
	}
	return &Runner{Bin: st.PreviousOramaBin, Home: st.Home, AgentSock: st.RWSock, Target: st.Target, Recorder: rec,
		Wallet: st.OperatorAddress}
}

// Isolated returns a runner with a fresh HOME that holds a copy of the
// environment list and no credentials. Commands that act on "the current
// namespace" (auth login --namespace, namespace delete, members) switch state
// in HOME, and the shared HOME is used by every test in the run at once; an
// isolated HOME signs in on its own through the same agent.
func (r *Runner) Isolated(t testing.TB) *Runner {
	t.Helper()
	home := t.TempDir()
	if err := copyEnvironments(r.Home, home); err != nil {
		t.Fatal(err)
	}
	c := *r
	c.Home = home
	c.Test = t.Name()
	return &c
}

func copyEnvironments(fromHome, toHome string) error {
	src := filepath.Join(fromHome, ConfigDirName, EnvironmentsFile)
	raw, err := os.ReadFile(src)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("the run's CLI HOME has no %s: was `orama env add` run during provisioning?", src)
	}
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", src, err)
	}
	dir := filepath.Join(toHome, ConfigDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, EnvironmentsFile), raw, 0o600); err != nil {
		return fmt.Errorf("failed to write the isolated environment list: %w", err)
	}
	return nil
}
