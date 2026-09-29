// Package lint enforces the feature-package contract (e2e/README.md) without
// servers: every feature has a manifest and a TestMain that calls
// harness.Main, every file carries the e2e_fleet build tag, tests are named
// Test{Function}_{scenario}, and nothing waits on a timer (time.Sleep,
// time.After, time.NewTimer, time.Tick) or skips with a bare t.Skip.
//
// features/internal/ holds helper packages shared by features. They are not
// features (no manifest, no TestMain, no tests), but the build tag, timer and
// skip rules apply to them too.
package lint

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
)

// BuildTag is the tag every feature file must require.
const BuildTag = "e2e_fleet"

const harnessImport = "github.com/DeBrosOfficial/network/e2e/harness"

// testName is Test{Function}_{scenario}.
var testName = regexp.MustCompile(`^Test[A-Z][A-Za-z0-9]*_[A-Za-z0-9_]+$`)

var bannedSkips = map[string]bool{"Skip": true, "Skipf": true, "SkipNow": true}

// bannedTimers are the time functions that wait on a clock instead of a
// readiness signal.
var bannedTimers = map[string]bool{"Sleep": true, "After": true, "NewTimer": true, "Tick": true}

// InternalDir is the directory of shared helper packages under features/.
const InternalDir = "internal"

// Problem is one violation, file:line when it has one.
type Problem struct {
	Pos string
	Msg string
}

func (p Problem) String() string { return p.Pos + ": " + p.Msg }

// Check lints every feature directory under featuresDir.
func Check(featuresDir string) ([]Problem, error) {
	entries, err := os.ReadDir(featuresDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list %s: %w", featuresDir, err)
	}
	var out []Problem
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		check := checkFeature
		if e.Name() == InternalDir {
			check = checkInternal
		}
		ps, err := check(filepath.Join(featuresDir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, ps...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// checkInternal lints the shared helper packages: every Go file under dir,
// at any depth, for the build tag, timers and skips.
func checkInternal(dir string) ([]Problem, error) {
	var out []Problem
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to walk %s: %w", path, err)
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			out = append(out, Problem{Pos: path, Msg: "does not parse: " + perr.Error()})
			return nil
		}
		out = append(out, checkBuildTag(fset, path, f)...)
		out = append(out, bannedCalls(fset, f)...)
		return nil
	})
	return out, err
}

func checkFeature(dir string) ([]Problem, error) {
	var out []Problem
	if _, err := manifest.LoadFile(filepath.Join(dir, manifest.FileName)); err != nil {
		out = append(out, Problem{Pos: dir, Msg: "manifest: " + err.Error()})
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("failed to list Go files in %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	hasMain, hasTests := false, false
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			out = append(out, Problem{Pos: path, Msg: "does not parse: " + err.Error()})
			continue
		}
		out = append(out, checkBuildTag(fset, path, f)...)
		fp, main, tests := checkFile(fset, path, f)
		out = append(out, fp...)
		hasMain, hasTests = hasMain || main, hasTests || tests
	}
	if !hasMain {
		out = append(out, Problem{Pos: dir, Msg: "no TestMain calling harness.Main(m)"})
	}
	if !hasTests {
		out = append(out, Problem{Pos: dir, Msg: "no Test functions"})
	}
	return out, nil
}

// checkBuildTag requires a //go:build line that excludes the file without the tag.
func checkBuildTag(fset *token.FileSet, path string, f *ast.File) []Problem {
	for _, cg := range f.Comments {
		if cg.Pos() > f.Package {
			break
		}
		for _, c := range cg.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return []Problem{{Pos: fset.Position(c.Pos()).String(), Msg: "bad build constraint: " + err.Error()}}
			}
			if expr.Eval(func(tag string) bool { return tag != BuildTag }) {
				return []Problem{{Pos: path, Msg: "build constraint does not require " + BuildTag}}
			}
			return nil
		}
	}
	return []Problem{{Pos: path, Msg: "missing //go:build " + BuildTag}}
}

// checkFile reports banned calls and badly named tests, and whether the file
// has a TestMain calling harness.Main and any Test function.
func checkFile(fset *token.FileSet, path string, f *ast.File) ([]Problem, bool, bool) {
	harnessPkg := importName(f, harnessImport)
	out := bannedCalls(fset, f)
	hasMain, hasTests := false, false
	isTestFile := strings.HasSuffix(path, "_test.go")
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		if fn.Name.Name == "TestMain" {
			hasMain = hasMain || callsHarnessMain(fn, harnessPkg)
			continue
		}
		p, isTest := checkTestFunc(fset, fn, isTestFile)
		out = append(out, p...)
		hasTests = hasTests || isTest
	}
	return out, hasMain, hasTests
}

// bannedCalls reports timer waits and bare skips.
func bannedCalls(fset *token.FileSet, f *ast.File) []Problem {
	timePkg, harnessPkg := importName(f, "time"), importName(f, harnessImport)
	var out []Problem
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, _ := sel.X.(*ast.Ident)
		pos := fset.Position(call.Pos()).String()
		switch {
		case x != nil && timePkg != "" && x.Name == timePkg && bannedTimers[sel.Sel.Name]:
			out = append(out, Problem{Pos: pos, Msg: "time." + sel.Sel.Name + ": poll a readiness signal with harness/eventually"})
		case bannedSkips[sel.Sel.Name] && (x == nil || x.Name != harnessPkg):
			out = append(out, Problem{Pos: pos, Msg: "bare " + sel.Sel.Name + ": use harness.SkipNotApplicable(t, reason)"})
		}
		return true
	})
	return out
}

// checkTestFunc: a Test* function must be func(*testing.T) named
// Test{Function}_{scenario}; a func(*testing.T) with no results that is not
// named Test... would silently never run.
func checkTestFunc(fset *token.FileSet, fn *ast.FuncDecl, isTestFile bool) ([]Problem, bool) {
	pos := fset.Position(fn.Pos()).String()
	takesT := len(fn.Type.Params.List) == 1 && len(fn.Type.Params.List[0].Names) <= 1 && isTestingT(fn.Type.Params.List[0].Type)
	noResults := fn.Type.Results == nil || len(fn.Type.Results.List) == 0
	name := fn.Name.Name
	switch {
	case strings.HasPrefix(name, "Test") && isTestFile:
		if !takesT || !noResults {
			return []Problem{{Pos: pos, Msg: name + " must have the signature func(t *testing.T)"}}, false
		}
		if !testName.MatchString(name) {
			return []Problem{{Pos: pos, Msg: name + " must be named Test{Function}_{scenario}"}}, true
		}
		return nil, true
	case takesT && noResults && isTestFile && (fn.Name.IsExported() || strings.HasPrefix(name, "test")):
		return []Problem{{Pos: pos, Msg: name + " looks like a test but is not named Test...: it never runs"}}, false
	}
	return nil, false
}

func isTestingT(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}

func callsHarnessMain(fn *ast.FuncDecl, harnessPkg string) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Main" {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == harnessPkg {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// importName is the name path is imported under in f, "" when not imported.
func importName(f *ast.File, path string) string {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != path {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return filepath.Base(p)
	}
	return ""
}
