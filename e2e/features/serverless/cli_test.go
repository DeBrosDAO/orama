//go:build e2e_fleet

package serverless

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

var triggerID = regexp.MustCompile(`\(id: ([^)\s]+)\)`)

// runCLI runs the fixture's CLI and returns the result without judging it.
func runCLI(t *testing.T, fx *fixture, args ...string) oramacli.Result {
	t.Helper()
	res, err := fx.n.CLI.For(t).Run(t.Context(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestFunctionCLI_groupListsSubcommands: `orama function` names every
// subcommand (docs/CLI_REFERENCE.md#orama-function).
func TestFunctionCLI_groupListsSubcommands(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	out := runCLI(t, fx, "function", "--help").Stdout
	for _, sub := range []string{"build", "delete", "deploy", "disable", "enable", "get", "init", "invoke", "list", "logs", "secrets", "triggers", "versions"} {
		if !strings.Contains(out, sub) {
			t.Errorf("`orama function --help` does not list %s", sub)
		}
	}
}

// TestFunctionCLI_initBuildScaffold: the documented quick start — init, then
// build — produces a WASM binary from the scaffold (docs/SERVERLESS.md#quick-start).
func TestFunctionCLI_initBuildScaffold(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	fx.n.CLI.MustOK(t, "function", "init", "e2e-scaffold")
	dir := filepath.Join(fx.n.CLI.Home, "e2e-scaffold")
	for _, file := range []string{"function.go", "function.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			t.Fatalf("init did not write %s: %v", file, err)
		}
	}
	if res := runCLI(t, fx, "function", "init", "e2e-scaffold"); res.Exit == 0 {
		t.Errorf("init over an existing directory succeeded")
	}
	if res := runCLI(t, fx, "function", "init", "9-bad name"); res.Exit == 0 {
		t.Errorf("init accepted an invalid name")
	}
	res := runCLI(t, fx, "function", "build", dir)
	if res.Exit != 0 {
		t.Fatalf("building the scaffold failed (exit %d): %s", res.Exit, res.Stderr)
	}
	if st, err := os.Stat(filepath.Join(dir, "function.wasm")); err != nil || st.Size() == 0 {
		t.Fatalf("build left no function.wasm: %v", err)
	}
}

// TestFunctionCLI_lifecycle walks one function through deploy, list, get,
// invoke, logs, a second version, versions, disable, enable and delete.
func TestFunctionCLI_lifecycle(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	const name = "e2e-life"
	dir := deploy(t, fx, fnSpec{name: name, yaml: "env:\n  MARK: v1\n"})
	if out := fx.n.CLI.MustOK(t, "function", "list").Stdout; !strings.Contains(out, name) {
		t.Errorf("list does not show %s:\n%s", name, out)
	}
	if out := fx.n.CLI.MustOK(t, "function", "get", name).Stdout; !strings.Contains(out, name) {
		t.Errorf("get does not describe %s:\n%s", name, out)
	}
	out := fx.n.CLI.MustOK(t, "function", "invoke", name, "--data", `{"op":"echo","value":"cli-hello"}`).Stdout
	if !strings.Contains(out, "cli-hello") || !strings.Contains(out, "Request ID") {
		t.Errorf("invoke output:\n%s", out)
	}
	t.Run("logs", func(t *testing.T) { waitLog(t, fx, name, "e2e-log:cli-hello") })
	redeploy(t, fx, dir, "env:\n  MARK: v2\n")
	if out := fx.n.CLI.MustOK(t, "function", "versions", name).Stdout; !strings.Contains(out, "Total: 2") {
		t.Errorf("versions after two deploys:\n%s", out)
	}
	fx.n.CLI.MustOK(t, "function", "disable", name)
	if r := invoke(t, fx.c, name, fx.admin, map[string]any{"op": "echo"}); r.Status != 404 {
		t.Errorf("invoking a disabled function: want 404, got %d %.200s", r.Status, r.Body)
	}
	fx.n.CLI.MustOK(t, "function", "enable", name)
	call(t, fx, name, map[string]any{"op": "echo", "value": "back"})
	fx.n.CLI.MustOK(t, "function", "delete", name, "--force")
	if res := runCLI(t, fx, "function", "get", name); res.Exit == 0 {
		t.Errorf("get after delete succeeded:\n%s", res.Stdout)
	}
	for _, args := range [][]string{{"function", "enable", name}, {"function", "invoke", name}} {
		if res := runCLI(t, fx, args...); res.Exit == 0 {
			t.Errorf("%v after delete succeeded", args)
		}
	}
}

// redeploy writes a new function.yaml tail and deploys the same dir again.
func redeploy(t *testing.T, fx *fixture, dir, yamlTail string) {
	t.Helper()
	cfg := filepath.Join(dir, "function.yaml")
	head, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(head), "\n", 3)
	if err := os.WriteFile(cfg, []byte(lines[0]+"\n"+lines[1]+"\n"+yamlTail), fixturePerm); err != nil {
		t.Fatal(err)
	}
	fx.n.CLI.MustOK(t, "function", "deploy", dir)
}

// TestFunctionCLI_invalidConfigRefused: memory 1-256, timeout 1-300 and the
// name rule are enforced before anything is uploaded (docs/SERVERLESS.md#functionyaml).
func TestFunctionCLI_invalidConfigRefused(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	for name, spec := range map[string]fnSpec{
		"memory 257":  {name: "e2e-mem", yaml: "memory: 257\n"},
		"memory -1":   {name: "e2e-memneg", yaml: "memory: -1\n"},
		"timeout 301": {name: "e2e-tmo", yaml: "timeout: 301\n"},
		"bad name":    {name: "9bad", yaml: ""},
		"bad yaml":    {name: "e2e-yaml", yaml: "memory: [\n"},
	} {
		dir := writeFixture(t, spec)
		if res := runCLI(t, fx, "function", "deploy", dir); res.Exit == 0 {
			t.Errorf("%s: deploy succeeded", name)
		}
	}
	if out := fx.n.CLI.MustOK(t, "function", "list").Stdout; strings.Contains(out, "e2e-mem") || strings.Contains(out, "e2e-tmo") {
		t.Errorf("a refused function was deployed:\n%s", out)
	}
	if res := runCLI(t, fx, "function", "get", "e2e-no-such-fn"); res.Exit == 0 {
		t.Errorf("get of a missing function succeeded")
	}
}

// TestFunctionCLI_secrets: set (inline and --from-file), list shows names
// only, get_secret reads the value, delete removes it (docs/SERVERLESS.md#managing-secrets).
func TestFunctionCLI_secrets(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	const fn = "e2e-secrets"
	deploy(t, fx, fnSpec{name: fn})
	value := "s3cr3t-" + fx.n.Name
	fileValue := "-----BEGIN E2E-----\nline two ü\n-----END E2E-----"
	file := filepath.Join(t.TempDir(), "pem")
	if err := os.WriteFile(file, []byte(fileValue), fixturePerm); err != nil {
		t.Fatal(err)
	}
	fx.n.CLI.MustOK(t, "function", "secrets", "set", "E2E_INLINE", value)
	fx.n.CLI.MustOK(t, "function", "secrets", "set", "E2E_FILE", "--from-file", file)
	list := fx.n.CLI.MustOK(t, "function", "secrets", "list").Stdout
	if !strings.Contains(list, "E2E_INLINE") || !strings.Contains(list, "E2E_FILE") || strings.Contains(list, value) || strings.Contains(list, "line two") {
		t.Errorf("secrets list must show names and never values:\n%s", list)
	}
	if got := call(t, fx, fn, map[string]any{"op": "secret", "name": "E2E_INLINE"})["value"]; got != value {
		t.Errorf("get_secret E2E_INLINE returned %q", got)
	}
	if got := call(t, fx, fn, map[string]any{"op": "secret", "name": "E2E_FILE"})["value"]; got != fileValue {
		t.Errorf("get_secret E2E_FILE returned %q", got)
	}
	if got := call(t, fx, fn, map[string]any{"op": "secret", "name": "E2E_MISSING"})["value"]; got != "" {
		t.Errorf("get_secret of a missing name returned %q", got)
	}
	fx.n.CLI.MustOK(t, "function", "secrets", "delete", "E2E_INLINE", "--force")
	if got := call(t, fx, fn, map[string]any{"op": "secret", "name": "E2E_INLINE"})["value"]; got != "" {
		t.Errorf("a deleted secret still reads %q", got)
	}
	if res := runCLI(t, fx, "function", "secrets", "set", "E2E_NOVALUE"); res.Exit == 0 {
		t.Errorf("secrets set without a value or --from-file succeeded")
	}
}

// TestFunctionCLI_triggers: add (topic and schedule), list, delete, and the
// refusals: both or neither flag, a bad schedule, an unknown function.
func TestFunctionCLI_triggers(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	const fn = "e2e-trig"
	deploy(t, fx, fnSpec{name: fn})
	topicOut := fx.n.CLI.MustOK(t, "function", "triggers", "add", fn, "--topic", "e2e:cli").Stdout
	cronOut := fx.n.CLI.MustOK(t, "function", "triggers", "add", fn, "--schedule", "0 3 * * *").Stdout
	ids := []string{firstID(t, topicOut), firstID(t, cronOut)}
	list := fx.n.CLI.MustOK(t, "function", "triggers", "list", fn).Stdout
	if !strings.Contains(list, "e2e:cli") || !strings.Contains(list, "0 3 * * *") {
		t.Errorf("triggers list:\n%s", list)
	}
	for name, args := range map[string][]string{
		"both flags":       {"function", "triggers", "add", fn, "--topic", "x", "--schedule", "0 3 * * *"},
		"no flag":          {"function", "triggers", "add", fn},
		"bad schedule":     {"function", "triggers", "add", fn, "--schedule", "61 * * * *"},
		"unknown function": {"function", "triggers", "add", "e2e-no-such-fn", "--topic", "x"},
	} {
		if res := runCLI(t, fx, args...); res.Exit == 0 {
			t.Errorf("triggers add with %s succeeded", name)
		}
	}
	for _, id := range ids {
		fx.n.CLI.MustOK(t, "function", "triggers", "delete", fn, id)
	}
	if out := fx.n.CLI.MustOK(t, "function", "triggers", "list", fn).Stdout; !strings.Contains(out, "No triggers") {
		t.Errorf("triggers remain after delete:\n%s", out)
	}
	if res := runCLI(t, fx, "function", "triggers", "delete", fn, ids[0]); res.Exit == 0 {
		t.Errorf("deleting a deleted trigger succeeded")
	}
}

func firstID(t *testing.T, out string) string {
	t.Helper()
	m := triggerID.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no trigger id in %q", out)
	}
	return m[1]
}
