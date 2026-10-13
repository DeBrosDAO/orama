package functions

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func scaffold(t *testing.T, name string) string {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := runInit(InitCmd, []string{name}); err != nil {
		t.Fatalf("init %s: %v", name, err)
	}
	dir, err := filepath.Abs(name)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Bug: init wrote no go.mod, so the documented init-then-build failed: TinyGo
// builds a module and refuses a directory without one.
func TestInit_writesAModuleNamedAfterTheFunction(t *testing.T) {
	for _, name := range []string{"my-function", "hello_world", "F1", "a"} {
		t.Run(name, func(t *testing.T) {
			dir := scaffold(t, name)
			raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
			if err != nil {
				t.Fatalf("init wrote no go.mod: %v", err)
			}
			if want := "module " + name + "\n\ngo " + scaffoldGoVersion + "\n"; string(raw) != want {
				t.Errorf("go.mod = %q, want %q", raw, want)
			}
			for _, file := range []string{"function.go", "function.yaml", "fn/fn.go"} {
				if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
					t.Errorf("init wrote no %s: %v", file, err)
				}
			}
			handler, err := os.ReadFile(filepath.Join(dir, "function.go"))
			if err != nil || !strings.Contains(string(handler), `import "`+name+`/fn"`) || !strings.Contains(string(handler), "fn.Run") {
				t.Errorf("function.go does not import the copied SDK as %q: %v\n%s", name+"/fn", err, handler)
			}
		})
	}
}

// The scaffold must build with nothing but the toolchain: it imports the SDK
// copy inside it and nothing to download, in a module the Go tool accepts. This
// is the compile TinyGo does, on the WASI target it is given.
func TestInit_theScaffoldCompilesOffline(t *testing.T) {
	dir := scaffold(t, "compiles")
	cmd := exec.Command("go", "build", "-o", os.DevNull, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOFLAGS=-mod=readonly", "GOPROXY=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the scaffold does not build: %v\n%s", err, out)
	}
}

// The copied SDK is the repository's, byte for byte after its note, so a fix
// to the SDK reaches new projects.
func TestInit_theCopiedSDKIsTheRepositorysSDK(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "sdk", "fn", "fn.go"))
	if err != nil {
		t.Fatal(err)
	}
	dir := scaffold(t, "sdkcopy")
	copied, err := os.ReadFile(filepath.Join(dir, "fn", "fn.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(copied), string(source)) {
		t.Error("fn/fn.go is not sdk/fn/fn.go")
	}
	if !strings.HasPrefix(string(copied), "// Copied by 'orama function init'") {
		t.Errorf("fn/fn.go does not say where it came from:\n%.200s", copied)
	}
}

func TestInit_scaffoldedYAMLIsALoadableConfig(t *testing.T) {
	dir := scaffold(t, "configured")
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("the scaffolded function.yaml does not load: %v", err)
	}
	if cfg.Name != "configured" {
		t.Errorf("name = %q", cfg.Name)
	}
}

func TestInit_refusesABadNameAndAnExistingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, bad := range []string{"9-bad", "has space", "-lead", "a/b", "../x", ""} {
		if err := runInit(InitCmd, []string{bad}); err == nil {
			t.Errorf("init accepted the name %q", bad)
		}
		if entries, _ := os.ReadDir("."); len(entries) != 0 {
			t.Fatalf("a refused name %q left %d entries behind", bad, len(entries))
		}
	}
	if err := runInit(InitCmd, []string{"twice"}); err != nil {
		t.Fatal(err)
	}
	err := runInit(InitCmd, []string{"twice"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("init over an existing directory = %v", err)
	}
}
