package testutil

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoProductionImport fails when a non-test Go file of the module imports this package: the
// fakes here, including a verifier that accepts everything, must never be linked into oramad.
func TestNoProductionImport(t *testing.T) {
	const self = "github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "target" || d.Name() == "build" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Dir(path) == filepath.Dir(mustAbs(t, "doc.go")) {
			return nil // this package itself
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return nil // a file that does not parse cannot be linked either
		}
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == self {
				t.Errorf("%s imports the test-only fakes", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
