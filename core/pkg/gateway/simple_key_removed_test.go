package gateway

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// /v1/auth/simple-key required that *some* API key was present, then took the
// wallet and the namespace from the request body with no cross-check against
// the authenticated key, and minted a key for that namespace. A runtime key
// scraped from a browser bundle minted an admin key for anyone's namespace.
//
// It is gone. This walks the route table rather than trusting that, because
// the endpoint was two lines to re-add and its only first-party caller was a
// convenience flag.
func TestSimpleKeyRouteIsGone(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse the gateway package: %v", err)
	}

	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if strings.Contains(lit.Value, "/v1/auth/simple-key") {
					t.Errorf("%s: %s is registered again — it mints a key for a namespace "+
						"named in the request body, authenticated by any key at all",
						fset.Position(lit.Pos()), lit.Value)
				}
				return true
			})
		}
	}
}

// An unregistered path is a 404 from the mux, but only if nothing else claims
// it by prefix. /v1/auth/ has several handlers, so this checks the actual
// routing rather than assuming.
func TestSimpleKeyPathIsNotRoutedByPrefix(t *testing.T) {
	for _, path := range []string{"/v1/auth/simple-key", "/v1/auth/simple-key/"} {
		if isPublicPath(path) {
			t.Errorf("%s is still in the public allowlist", path)
		}
	}
}
