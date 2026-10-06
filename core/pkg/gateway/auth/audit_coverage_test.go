package auth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// An action nobody records is a promise: it is in AuditActions, so `orama audit
// --action` accepts it, the CLI help lists it and the docs describe it — and it
// never appears, because no handler passes it to Record. The reverse is worse:
// a handler recording an action that is not in the list writes rows the filter
// refuses to select, so the event is in the table and unreachable.
//
// These tests hold both ends together by reading the source: the declared list,
// the constants, and every call site in the tree.

// auditActionNames returns the constant names listed in AuditActions, in order.
func auditActionNames(t *testing.T) []string {
	t.Helper()
	file := parseAuditSource(t)

	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "AuditActions" {
			return true
		}
		lit, ok := spec.Values[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, elt := range lit.Elts {
			ident, ok := elt.(*ast.Ident)
			if !ok {
				t.Fatalf("AuditActions holds something that is not a constant name: %T", elt)
			}
			names = append(names, ident.Name)
		}
		return false
	})
	if len(names) == 0 {
		t.Fatal("AuditActions could not be read from the source")
	}
	return names
}

// auditActionConsts returns every action constant declared in audit.go, by
// name. An action is a dotted string: "key.issue", never "success".
func auditActionConsts(t *testing.T) map[string]string {
	t.Helper()
	file := parseAuditSource(t)

	out := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.CONST {
			return true
		}
		for _, s := range decl.Specs {
			spec, ok := s.(*ast.ValueSpec)
			if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
				continue
			}
			name := spec.Names[0].Name
			if !strings.HasPrefix(name, "Audit") {
				continue
			}
			lit, ok := spec.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil || !strings.Contains(value, ".") {
				continue
			}
			out[name] = value
		}
		return true
	})
	return out
}

func parseAuditSource(t *testing.T) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "audit.go", nil, 0)
	if err != nil {
		t.Fatalf("parse audit.go: %v", err)
	}
	return file
}

func TestAuditActions_listsEveryDeclaredAction(t *testing.T) {
	listed := map[string]bool{}
	for _, name := range auditActionNames(t) {
		if listed[name] {
			t.Errorf("%s is in AuditActions twice", name)
		}
		listed[name] = true
	}

	for name, value := range auditActionConsts(t) {
		if !listed[name] {
			t.Errorf("%s (%q) is declared but missing from AuditActions, so `orama audit --action %s` "+
				"refuses it and the rows it writes cannot be selected", name, value, value)
		}
	}

	// The list is what the runtime hands the filter and the CLI; a name in the
	// source with no value behind it would be a compile error, but a list that
	// has drifted in length would not.
	if len(AuditActions) != len(listed) {
		t.Errorf("AuditActions has %d entries but %d distinct names", len(AuditActions), len(listed))
	}

	seen := map[string]bool{}
	for _, action := range AuditActions {
		if seen[action] {
			t.Errorf("two constants share the action string %q, so the two events cannot be told apart", action)
		}
		seen[action] = true
		if !IsAuditAction(action) {
			t.Errorf("IsAuditAction(%q) is false for an action in its own list", action)
		}
	}
}

func TestAuditActions_everyActionIsRecordedSomewhere(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}

	used := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" || d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// audit.go declares them; declaring is not recording.
		if filepath.Base(path) == "audit.go" && strings.Contains(path, filepath.Join("gateway", "auth")) {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, name := range auditActionNames(t) {
			if strings.Contains(string(source), name) {
				used[name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range auditActionNames(t) {
		if !used[name] {
			t.Errorf("%s is advertised in AuditActions and in `orama audit --help`, but nothing "+
				"outside audit.go ever records it", name)
		}
	}
}
