package build

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A binary that links mattn/go-sqlite3 but is built with CGO_ENABLED=0 gets
// the driver's stub: it compiled until CHG-202 and failed every Open at
// runtime, and since CHG-202 it does not compile. Deriving the requirement
// from the dependency graph keeps the build list honest as imports change.
func TestOramaBinaries_EverySQLiteBinaryIsBuiltWithCGO(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list over every binary")
	}
	_, file, _, _ := runtime.Caller(0)
	projectDir := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")

	for _, bin := range oramaBinaries("") {
		cmd := exec.Command("go", "list", "-deps", bin.Package)
		cmd.Dir = projectDir
		cmd.Env = append(cmd.Environ(), "GOOS=linux", "CGO_ENABLED=1")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list %s: %v", bin.Package, err)
		}
		linksSQLite := strings.Contains(string(out), "github.com/mattn/go-sqlite3\n")
		if linksSQLite && !bin.CGO {
			t.Errorf("%s links github.com/mattn/go-sqlite3 but is built without cgo", bin.Name)
		}
		if !linksSQLite && bin.CGO {
			t.Errorf("%s is built with cgo but does not need it; keep it CGO_ENABLED=0", bin.Name)
		}
	}
}

func TestGoBuildArgs_CGOBinaryIsStaticWithPureGoNet(t *testing.T) {
	args := goBuildArgs(oramaBinary{Name: "gateway", Package: "./cmd/gateway/", CGO: true}, "-s -w", "/out/gateway")
	joined := strings.Join(args, " ")

	for _, want := range []string{"-tags " + cgoBuildTags, "-linkmode external", "-extldflags -static", "-o /out/gateway", "./cmd/gateway/"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
	if !strings.Contains(cgoBuildTags, "netgo") || !strings.Contains(cgoBuildTags, "osusergo") {
		t.Errorf("a static cgo binary must use the pure-Go resolver and user lookup: %q", cgoBuildTags)
	}
}

func TestGoBuildArgs_PureGoBinaryHasNoCGOFlags(t *testing.T) {
	joined := strings.Join(goBuildArgs(oramaBinary{Name: "orama", Package: "./cmd/orama/"}, "-s -w", "/out/orama"), " ")
	for _, unwanted := range []string{"-tags", "-linkmode", "-extldflags"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("pure-Go build must not carry %q: %q", unwanted, joined)
		}
	}
}

func TestCGOEnv_TargetsMuslThroughZig(t *testing.T) {
	env, err := cgoEnv(nil, "arm64", "/opt/zig/bin/zig")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	for _, want := range []string{"GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=1", "CC=/opt/zig/bin/zig cc -target aarch64-linux-musl"} {
		if !strings.Contains(joined, want) {
			t.Errorf("env missing %q:\n%s", want, joined)
		}
	}
}

func TestZigTargetFor_UnknownArchIsRefused(t *testing.T) {
	if _, err := zigTargetFor("riscv64"); err == nil {
		t.Fatal("riscv64 must be refused, not mapped to a wrong target")
	}
	if got, _ := zigTargetFor("amd64"); got != "x86_64-linux-musl" {
		t.Errorf("amd64 -> %q", got)
	}
}
