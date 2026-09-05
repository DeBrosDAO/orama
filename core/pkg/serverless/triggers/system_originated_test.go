package triggers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// Every invocation this package makes is the gateway firing something an
// authenticated operator registered: a cron row that came due, a pubsub trigger
// that matched. None of them has a per-fire caller, so each must say so — a
// cron fire that does not is refused at the caller check and stops silently,
// which is bugboard #264, where a trigger fired every minute with
// "unauthorized" for 19 hours before anyone noticed.
//
// This walks the source rather than driving each dispatcher, because the risk
// is a new dispatcher added later, and a new dispatcher is precisely what a
// test of the existing ones does not cover.
func TestEveryDispatchInThisPackageIsMarkedGatewayStarted(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	requests := 0
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				sel, ok := lit.Type.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "InvokeRequest" {
					return true
				}
				requests++

				marked := false
				for _, elt := range lit.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok || key.Name != "SystemOriginated" {
						continue
					}
					if ident, ok := kv.Value.(*ast.Ident); ok && ident.Name == "true" {
						marked = true
					}
				}
				if !marked {
					pos := fset.Position(lit.Pos())
					t.Errorf("%s:%d builds an InvokeRequest without SystemOriginated: true. "+
						"A dispatcher in this package fires on the gateway's own authority; without "+
						"the flag the invocation is refused at the caller check and stops silently.",
						path, pos.Line)
				}
				return true
			})
		}
	}

	if requests < 3 {
		t.Fatalf("found %d InvokeRequest literals; this test is not reading the dispatchers", requests)
	}
}
