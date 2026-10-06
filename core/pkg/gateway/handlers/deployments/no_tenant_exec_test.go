package deployments

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// allowedGatewayCommands are the only programs these handlers may run in the
// gateway's own process: tar unpacks an archive and runs nothing from it.
var allowedGatewayCommands = map[string]bool{"tar": true}

// Nothing the tenant wrote may run as the gateway. `npm install` did — as the
// orama user, with the gateway's environment, running every dependency's
// postinstall — until it moved to orama-deploy-build@ (process/build.go).
// This fails on any exec.Command in the handlers whose program is not a
// literal on the allow-list, so a build step cannot come back.
func TestHandlers_runNoTenantCodeInTheGateway(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		checked++
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "exec" {
				return true
			}
			argIndex := 0
			switch sel.Sel.Name {
			case "Command":
			case "CommandContext":
				argIndex = 1
			default:
				return true
			}
			if len(call.Args) <= argIndex {
				return true
			}
			lit, ok := call.Args[argIndex].(*ast.BasicLit)
			if !ok {
				t.Errorf("%s: exec.%s with a program that is not a literal; the gateway cannot show it runs no tenant code",
					fset.Position(call.Pos()), sel.Sel.Name)
				return true
			}
			program, _ := strconv.Unquote(lit.Value)
			if !allowedGatewayCommands[program] {
				t.Errorf("%s: the gateway runs %q itself; tenant builds run in their own unit (orama-deploy-build@)",
					fset.Position(call.Pos()), program)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no handler source was checked")
	}
}
