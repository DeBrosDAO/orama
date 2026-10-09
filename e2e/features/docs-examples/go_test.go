//go:build e2e_fleet

package docsexamples

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

const (
	// buildBudget bounds one `go build` or `tinygo build` of an example.
	buildBudget = 5 * time.Minute
	// functionExamples holds the serverless function examples
	// (docs/SERVERLESS.md, docs/GO_CLIENT_SDK.md "Examples").
	functionExamples = "core/examples/functions"
	corePath         = "core"
	coreModule       = "github.com/DeBrosOfficial/network"
)

// goVersionRe reads the go directive of core/go.mod.
var goVersionRe = regexp.MustCompile(`(?m)^go (\S+)$`)

// goModule writes a module in a fresh directory that builds against the
// checkout's core the way a user's `go get` would, and returns the directory.
func goModule(t testing.TB) string {
	t.Helper()
	root := cliconf.RepoRoot(t)
	m := goVersionRe.FindStringSubmatch(cliconf.ReadRepoFile(t, corePath+"/go.mod"))
	if m == nil {
		t.Fatal("core/go.mod has no go directive")
	}
	dir := t.TempDir()
	mod := fmt.Sprintf("module example.com/docsnippet\n\ngo %s\n\nrequire %s v0.0.0-00010101000000-000000000000\n\nreplace %s => %s\n",
		m[1], coreModule, coreModule, filepath.Join(root, corePath))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(cliconf.ReadRepoFile(t, corePath+"/go.sum")), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// toolEnv is the environment of a build: the toolchain's own variables and
// nothing else of the runner's (no run secret reaches a doc example).
func toolEnv() []string {
	env := []string{"GOFLAGS=-mod=mod", "CGO_ENABLED=0"}
	for _, k := range []string{"PATH", "HOME", "TMPDIR", "GOPATH", "GOMODCACHE", "GOCACHE", "GOPROXY", "GOTOOLCHAIN"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// build runs a toolchain command in dir and returns its output and error.
func build(t testing.TB, dir, tool string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), buildBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Dir, cmd.Env = dir, toolEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func requireGo(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		harness.SkipNotApplicable(t, "the Go toolchain is not on the runner's PATH")
	}
}

// TestDocsExamples_goProgramsBuild: every Go block in the documents that is
// a whole program (starts with `package main`) builds against the checkout's
// core, as a user pasting it into their module would build it. Fragments are
// not programs and are not built.
func TestDocsExamples_goProgramsBuild(t *testing.T) {
	t.Parallel()
	requireGo(t)
	built := 0
	for _, s := range allSnippets(t, "go") {
		if !strings.HasPrefix(strings.TrimSpace(s.Body), "package main") {
			continue
		}
		built++
		dir := goModule(t)
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(s.Body), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := build(t, dir, "go", "build", "-o", os.DevNull, "."); err != nil {
			t.Errorf("%s: the Go program does not build (%v):\n%s", s.Where(), err, out)
		}
	}
	if built == 0 {
		t.Fatal("no whole Go program in the example documents: the extraction broke")
	}
}

// functionDirs are the example function directories (each with a main.go).
func functionDirs(t testing.TB) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(cliconf.RepoRoot(t), functionExamples, "*", "main.go"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no function examples under %s: %v", functionExamples, err)
	}
	for i := range dirs {
		dirs[i] = filepath.Dir(dirs[i])
	}
	return dirs
}

// TestDocsExamples_functionExamplesBuild: the serverless examples build with
// Go, and to WASM with TinyGo as their header and build.sh say, when TinyGo
// is on the runner.
func TestDocsExamples_functionExamplesBuild(t *testing.T) {
	t.Parallel()
	requireGo(t)
	for _, src := range functionDirs(t) {
		t.Run(filepath.Base(src), func(t *testing.T) {
			t.Parallel()
			dir := goModule(t)
			code, err := os.ReadFile(filepath.Join(src, "main.go"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.go"), code, 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := build(t, dir, "go", "build", "-o", os.DevNull, "."); err != nil {
				t.Fatalf("%s does not build with Go (%v):\n%s", src, err, out)
			}
			tinygo, err := exec.LookPath("tinygo")
			if err != nil {
				harness.SkipNotApplicable(t, "tinygo is not installed on the runner, so the WASM build core/examples/functions/build.sh does is not checked")
			}
			if out, err := build(t, dir, tinygo, "build", "-o", filepath.Join(dir, "fn.wasm"), "-target", "wasi", "."); err != nil {
				t.Errorf("%s does not build to WASM with TinyGo (%v):\n%s", src, err, out)
			}
		})
	}
}
