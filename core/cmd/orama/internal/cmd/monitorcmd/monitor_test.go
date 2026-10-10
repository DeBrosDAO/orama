package monitorcmd

import (
	"strings"
	"testing"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
)

// A positional argument can only be a view that does not exist. It was
// ignored and the live view opened (stagenet e2e, 2026-09-30); it is refused,
// and the CLI's usage classification turns the refusal into exit 2.
func TestMonitor_refusesAPositionalArgument(t *testing.T) {
	if Cmd.Args == nil {
		t.Fatal("monitor accepts any positional argument")
	}
	if err := Cmd.Args(Cmd, []string{"e2e-no-such-view"}); err == nil {
		t.Error("a view that does not exist was accepted")
	}
	if err := Cmd.Args(Cmd, nil); err != nil {
		t.Errorf("monitor with no argument was refused: %v", err)
	}
}

func TestResolveEnv_flagThenActiveThenAnError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := flagEnv
	t.Cleanup(func() { flagEnv = old })

	flagEnv = "devnet"
	if got, err := resolveEnv(); err != nil || got != "devnet" {
		t.Fatalf("resolveEnv with --env = %q, %v", got, err)
	}

	flagEnv = ""
	if _, err := resolveEnv(); err == nil || !strings.Contains(err.Error(), "no --env given") {
		t.Fatalf("resolveEnv with nothing configured = %v, want an error naming --env", err)
	}

	if err := cli.AddEnvironment("lab", "https://lab.example.org", ""); err != nil {
		t.Fatal(err)
	}
	if err := cli.SwitchEnvironment("lab"); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveEnv(); err != nil || got != "lab" {
		t.Fatalf("resolveEnv with an active network = %q, %v; --env is optional, as it is for `orama status`", got, err)
	}
}

func TestMonitor_envIsNotRequired(t *testing.T) {
	if f := Cmd.PersistentFlags().Lookup("env"); f == nil || strings.Contains(f.Usage, "required") {
		t.Errorf("--env = %+v; it defaults to the active network", f)
	}
}
