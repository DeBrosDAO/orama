package install

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// pid1OpensAsRoot matches the unit directives whose path systemd opens as PID
// 1, before it switches to User=, following symlinks: EnvironmentFile= and
// LoadCredential= are read; StandardInput=/StandardOutput=/StandardError= with
// file:, append: or truncate: are opened — and created when missing — with a
// plain open(2) (src/core/exec-invoke.c, setup_output → acquire_path). The
// orama user owns everything under /opt/orama/.orama, so such a path there
// lets it point the file at a root-owned one: read it back through the unit's
// environment, or have root write the service's output into it.
var pid1OpensAsRoot = regexp.MustCompile(`(?m)^(EnvironmentFile|LoadCredential|StandardInput|StandardOutput|StandardError)=(.*)$`)

// goGeneratedUnits are the unit files install and upgrade write from Go
// strings rather than from core/systemd (which pkg/systemd's guard covers).
func goGeneratedUnits() map[string]string {
	ssg := NewSystemdServiceGenerator(OramaBase, OramaDir)
	return map[string]string{
		nodeServiceName:            ssg.GenerateNodeService(),
		privhelper.SocketUnitName:  privhelper.SocketUnit,
		privhelper.ServiceUnitName: privhelper.ServiceUnit,
	}
}

// pid1ReadsOramaPath reports whether a directive value names a file PID 1
// opens under /opt/orama.
func pid1ReadsOramaPath(directive, value string) bool {
	if strings.HasPrefix(directive, "Standard") {
		i := strings.Index(value, ":")
		if i < 0 {
			return false // journal, null, inherit, socket, ...
		}
		switch value[:i] {
		case "file", "append", "truncate":
		default:
			return false
		}
		value = value[i+1:]
	}
	return strings.Contains(value, OramaBase)
}

func TestGoGeneratedUnits_PID1NeverOpensAnOramaOwnedPath(t *testing.T) {
	for name, unit := range goGeneratedUnits() {
		for _, m := range pid1OpensAsRoot.FindAllStringSubmatch(unit, -1) {
			if pid1ReadsOramaPath(m[1], m[2]) {
				t.Errorf("%s: %s=%s is opened by PID 1 in the orama user's tree", name, m[1], m[2])
			}
		}
	}
}

// The matcher itself: a guard that matches nothing passes everything.
func TestPID1ReadsOramaPath(t *testing.T) {
	for _, c := range []struct {
		directive, value string
		want             bool
	}{
		{"StandardOutput", "append:/opt/orama/.orama/logs/node.log", true},
		{"StandardError", "file:/opt/orama/.orama/logs/x.log", true},
		{"StandardOutput", "truncate:/opt/orama/.orama/logs/x.log", true},
		{"EnvironmentFile", "-/opt/orama/.orama/data/sni-router.env", true},
		{"StandardOutput", "journal", false},
		{"StandardOutput", "append:/var/log/orama/node.log", false},
		{"EnvironmentFile", "/var/lib/orama-unit-env/index/rqlite.env", false},
	} {
		if got := pid1ReadsOramaPath(c.directive, c.value); got != c.want {
			t.Errorf("pid1ReadsOramaPath(%s, %s) = %v, want %v", c.directive, c.value, got, c.want)
		}
	}
}

// orama-node.service is the only host unit install writes through
// WriteServiceUnit; every daemon it supervises runs from a core/systemd
// template. A call naming anything else brings back a host unit orama-node
// then has to stop and disable on every boot.
func TestWriteServiceUnit_writesOnlyTheNodeUnit(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	files, err := filepath.Glob(filepath.Join(filepath.Dir(file), "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	calls := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "WriteServiceUnit" || len(call.Args) == 0 {
				return true
			}
			calls++
			if id, ok := call.Args[0].(*ast.Ident); !ok || id.Name != "nodeServiceName" {
				t.Errorf("%s: WriteServiceUnit(%s): install writes only %s", fset.Position(call.Pos()), exprString(call.Args[0]), nodeServiceName)
			}
			return true
		})
	}
	if calls == 0 {
		t.Fatal("found no WriteServiceUnit call; the scan is not looking at pkg/install")
	}
}

func exprString(e ast.Expr) string {
	if lit, ok := e.(*ast.BasicLit); ok {
		return lit.Value
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return "<expr>"
}
