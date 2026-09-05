package wireguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// A secret compared with == leaks its contents through timing, one byte at a
// time, to anyone who can make the comparison happen — and every node on the
// mesh can make this one happen. No behavioural test can tell the two apart:
// `==` and subtle.ConstantTimeCompare return the same answers. So the guard is
// on the source.
//
// This looks for the secret being compared with an equality operator anywhere
// in the package, which is what the fix replaced.
func TestClusterSecret_isNeverComparedWithAnEqualityOperator(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// The one comparison that is legitimate: asking whether a secret was
	// configured at all. It compares against the empty string, not against
	// anything a caller sent.
	isEmptyStringCheck := func(bin *ast.BinaryExpr) bool {
		for _, side := range []ast.Expr{bin.X, bin.Y} {
			if lit, ok := side.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `""` {
				return true
			}
		}
		return false
	}

	// The result of a constant-time compare is itself compared to 1. That is
	// the fix, not the thing being guarded against.
	isConstantTimeResult := func(bin *ast.BinaryExpr) bool {
		for _, side := range []ast.Expr{bin.X, bin.Y} {
			call, ok := side.(*ast.CallExpr)
			if !ok {
				continue
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ConstantTimeCompare" {
				return true
			}
		}
		return false
	}

	mentionsSecret := func(e ast.Expr) bool {
		found := false
		ast.Inspect(e, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.Ident:
				if strings.Contains(strings.ToLower(v.Name), "secret") {
					found = true
				}
			case *ast.SelectorExpr:
				if strings.Contains(strings.ToLower(v.Sel.Name), "secret") {
					found = true
				}
			}
			return true
		})
		return found
	}

	checked := 0
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				bin, ok := n.(*ast.BinaryExpr)
				if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
					return true
				}
				if !mentionsSecret(bin.X) && !mentionsSecret(bin.Y) {
					return true
				}
				checked++
				if isEmptyStringCheck(bin) || isConstantTimeResult(bin) {
					return true
				}
				pos := fset.Position(bin.Pos())
				t.Errorf("%s:%d compares a secret with %s. Use subtle.ConstantTimeCompare: "+
					"every node on the mesh can make this comparison happen, and a byte-at-a-time "+
					"timing difference is enough to recover the secret.",
					path[strings.LastIndexByte(path, '/')+1:], pos.Line, bin.Op)
				return true
			})
		}
	}

	if checked == 0 {
		t.Fatal("no comparison involving a secret was found at all; this test is not looking where it thinks it is")
	}
}
