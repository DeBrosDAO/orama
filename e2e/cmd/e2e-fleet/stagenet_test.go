package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// fakeOrama writes an orama stand-in that logs its arguments and HOME to
// argsFile and exits with code, printing stderr.
func fakeOrama(t *testing.T, dir string, code int, stderr string) (bin, argsFile string) {
	t.Helper()
	bin = filepath.Join(dir, "orama")
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$HOME $*\" > " + argsFile + "\necho '" + stderr + "' >&2\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

func stagenetTestState(t *testing.T, bin string) *fleet.State {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "home"), 0o700); err != nil {
		t.Fatal(err)
	}
	return &fleet.State{
		Target: config.TargetStagenet, Env: config.StagenetEnv, GatewayURL: config.StagenetGatewayURL,
		OramaBin: bin, Home: filepath.Join(dir, "home"), RWSock: filepath.Join(dir, "agent.sock"),
		OperatorAddress: testAddress,
	}
}

func TestSignInStagenetOperator_runsAuthLoginInTheStagenetHome(t *testing.T) {
	bin, argsFile := fakeOrama(t, t.TempDir(), 0, "")
	st := stagenetTestState(t, bin)
	if err := signInStagenetOperator(context.Background(), st); err != nil {
		t.Fatalf("sign-in failed: %v", err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := st.Home + " auth login --namespace " + config.StagenetOperatorNamespace; strings.TrimSpace(string(got)) != want {
		t.Fatalf("orama ran as %q, want %q", strings.TrimSpace(string(got)), want)
	}
}

func TestSignInStagenetOperator_failedLoginIsAnError(t *testing.T) {
	bin, _ := fakeOrama(t, t.TempDir(), 3, "agent locked")
	err := signInStagenetOperator(context.Background(), stagenetTestState(t, bin))
	if err == nil || !strings.Contains(err.Error(), "exited 3") || !strings.Contains(err.Error(), "agent locked") {
		t.Fatalf("want an error naming the exit and stderr, got %v", err)
	}
}

func TestSignInStagenetOperator_missingBinaryIsRefused(t *testing.T) {
	st := stagenetTestState(t, filepath.Join(t.TempDir(), "absent"))
	if err := signInStagenetOperator(context.Background(), st); err == nil {
		t.Fatal("a missing CLI binary was not refused")
	}
}
