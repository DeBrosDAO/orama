// Package srcscan parses a package directory's source for the tests that
// guard a code-wide rule by walking the syntax tree.
package srcscan

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

const (
	goSuffix   = ".go"
	testSuffix = "_test.go"
)

// ParseNonTest parses every non-test .go file directly in dir, comments
// included, and returns the files by path (dir joined with the file name).
// Build constraints are ignored: every file is parsed, as parser.ParseDir
// (deprecated) did. A directory that does not exist is an error satisfying
// errors.Is(err, os.ErrNotExist).
func ParseNonTest(fset *token.FileSet, dir string) (map[string]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read directory %s: %w", dir, err)
	}
	files := make(map[string]*ast.File)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, goSuffix) || strings.HasSuffix(name, testSuffix) {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		files[path] = f
	}
	return files, nil
}
