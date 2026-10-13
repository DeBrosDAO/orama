package networkcmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
)

// run executes `orama <args>` against a fresh root holding both groups and a
// HOME of its own, so the config files are the test's.
func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := &cobra.Command{Use: "orama", SilenceUsage: true, SilenceErrors: true}
	printer.Register(root)
	root.AddCommand(newGroup("network", "", ""), deprecatedEnv())
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func isolatedHome(t *testing.T) { t.Helper(); t.Setenv("HOME", t.TempDir()) }

func TestNetworkAdd_acceptsTheOldEnvAddShape(t *testing.T) {
	isolatedHome(t)
	if _, _, err := run(t, "network", "add", "lab", "https://lab.example.org", "my lab"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "network", "use", "lab"); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, "network", "current")
	if err != nil || !strings.Contains(out, "Current network: lab") || !strings.Contains(out, "https://lab.example.org") {
		t.Fatalf("current = %v\n%s", err, out)
	}
}

func TestNetworkAdd_argumentAndFlagShapes(t *testing.T) {
	isolatedHome(t)
	for name, args := range map[string][]string{
		"no arguments":              {"network", "add"},
		"four arguments":            {"network", "add", "a", "https://x.example.org", "d", "extra"},
		"--yes with a gateway":      {"network", "add", "a", "https://x.example.org", "--yes"},
		"--ca-file with a manifest": {"network", "add", "https://x.example.org/manifest.json", "--ca-file", "x.pem"},
		"--network with a manifest": {"network", "add", "https://x.example.org/manifest.json", "--network", "stagenet"},
		"unknown flag":              {"network", "add", "a", "https://x.example.org", "--nope"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := run(t, args...)
			if err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestNetworkAdd_aManifestOnPlainHTTPIsRefused(t *testing.T) {
	isolatedHome(t)
	_, _, err := run(t, "network", "add", "http://example.org/nets/x/manifest.json", "--yes")
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("error = %v, want an https refusal", err)
	}
}

func TestNetworkUse_hasNoSwitchOrEnableAlias(t *testing.T) {
	use, _, err := Cmd.Find([]string{"use"})
	if err != nil || use.Name() != "use" {
		t.Fatalf("use: %v", err)
	}
	if len(use.Aliases) != 0 {
		t.Errorf("use has aliases %v; switch and enable are gone", use.Aliases)
	}
	for _, alias := range []string{"switch", "enable"} {
		if cmd, _, _ := Cmd.Find([]string{alias}); cmd != nil && cmd != Cmd {
			t.Errorf("%q resolves to %q", alias, cmd.Name())
		}
	}
}

func TestEnv_isHiddenDeprecatedAndPrintsOneNotice(t *testing.T) {
	isolatedHome(t)
	if !EnvCmd.Hidden || Cmd.Hidden {
		t.Fatal("env must be hidden and network visible")
	}
	if _, _, err := run(t, "env", "add", "lab", "https://lab.example.org"); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run(t, "env", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "lab") {
		t.Errorf("env list does not list the network added through env:\n%s", out)
	}
	if strings.Count(errOut, "\n") != 1 || !strings.Contains(errOut, "deprecated") || !strings.Contains(errOut, "orama network") {
		t.Errorf("the notice is not one line naming the replacement:\n%q", errOut)
	}
	// What env wrote is what network reads: one store.
	out, errOut, err = run(t, "network", "list")
	if err != nil || !strings.Contains(out, "lab") || errOut != "" {
		t.Errorf("network list = %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
}

func TestEnv_hasEverySubcommandOfNetwork(t *testing.T) {
	names := func(c *cobra.Command) string {
		var n []string
		for _, sub := range c.Commands() {
			n = append(n, sub.Name())
		}
		return strings.Join(n, ",")
	}
	if names(EnvCmd) != names(Cmd) {
		t.Errorf("env has %s, network has %s", names(EnvCmd), names(Cmd))
	}
}

func TestNetworkAdd_oneArgumentThatIsNotAURLIsAUsageError(t *testing.T) {
	isolatedHome(t)
	_, _, err := run(t, "network", "add", "lab")
	if err == nil || !strings.Contains(err.Error(), "neither a manifest URL") {
		t.Fatalf("error = %v, want the shape explained", err)
	}
}
