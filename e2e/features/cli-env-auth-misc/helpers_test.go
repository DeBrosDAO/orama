//go:build e2e_fleet

package clienvauthmisc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Exit codes (core/cmd/orama/internal/clierr).
const (
	exitOK       = infra.ExitOK
	exitFailure  = infra.ExitFailure
	exitUsage    = infra.ExitUsage
	exitAuth     = infra.ExitAuth
	exitNotFound = infra.ExitNotFound
	exitConflict = infra.ExitConflict
)

// Messages the CLI prints (core/cmd/orama/internal).
const (
	// loginHint ends every "no credential" refusal.
	loginHint = "orama auth login"
	// noEnvHelp is environment.go noEnvironmentHelp's fix.
	noEnvHelp = "orama network add"
	// caRefused is network_commands.go NetworkAddCluster's refusal of a CA file.
	caRefused = "CA file was refused"
)

// e2eEnvPrefix starts every environment name this package adds, so no name
// can collide with the run's own (e2e-<run>) or a default.
const e2eEnvPrefix = "e2e-cli-"

func run(t testing.TB, cli *oramacli.Runner, args ...string) oramacli.Result {
	t.Helper()
	return infra.Run(t, cli, args...)
}

// isolated is the operator's CLI in a HOME of its own that holds the run's
// environment list and no credential: every `orama network` change stays in it.
func isolated(t testing.TB) *oramacli.Runner {
	t.Helper()
	return harness.CLI(t).Isolated(t)
}

// emptyHome is the operator's CLI in a HOME with nothing at all: no
// environment configured, no credential, the throwaway agent still reachable.
func emptyHome(t testing.TB) *oramacli.Runner {
	t.Helper()
	r := *harness.CLI(t)
	r.Home = t.TempDir()
	return &r
}

// envFile is one HOME's ~/.orama/environments.json.
type envFile struct {
	Environments []struct {
		Name        string `json:"name"`
		GatewayURL  string `json:"gateway_url"`
		Description string `json:"description"`
		CAFile      string `json:"ca_file"`
	} `json:"environments"`
	ActiveEnvironment string `json:"active_environment"`
}

func readEnvFile(t testing.TB, cli *oramacli.Runner) envFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cli.Home, oramacli.ConfigDirName, oramacli.EnvironmentsFile))
	if err != nil {
		t.Fatalf("failed to read the CLI's environment list: %v", err)
	}
	var f envFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("the CLI's environment list is not JSON: %v", err)
	}
	return f
}

// caFileOf returns the CA file recorded for env, and whether env exists.
func caFileOf(t testing.TB, cli *oramacli.Runner, env string) (string, bool) {
	t.Helper()
	for _, e := range readEnvFile(t, cli).Environments {
		if e.Name == env {
			return e.CAFile, true
		}
	}
	return "", false
}

// copyFile copies src to a new file in dir and returns its path.
func copyFile(t testing.TB, src, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("failed to read %s: %v", src, err)
	}
	dst := filepath.Join(dir, filepath.Base(src))
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		t.Fatalf("failed to write %s: %v", dst, err)
	}
	return dst
}

// output is a result's stdout and stderr together.
func output(res oramacli.Result) string { return res.Stdout + res.Stderr }

// expectNoEcho fails when the CLI printed secret back.
func expectNoEcho(t testing.TB, res oramacli.Result, secret string) {
	t.Helper()
	if strings.Contains(output(res), secret) {
		t.Errorf("orama %v printed the credential it was given back", oramacli.RedactArgs(res.Args))
	}
}
