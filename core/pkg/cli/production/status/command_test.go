package status

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// node status probed a fixed list — orama-ipfs, orama-ipfs-cluster,
// orama-olric, orama-vault, orama-node — four of which the installer
// deliberately disables, because IndexSupervisor runs orama-namespace-*@index
// instead. A correctly installed node printed four "Inactive" rows and did not
// list what was actually running.

func TestSplitNamespaceUnit(t *testing.T) {
	for _, tc := range []struct {
		unit      string
		role      string
		namespace string
		ok        bool
	}{
		{"orama-namespace-rqlite@anchat", "rqlite", "anchat", true},
		{"orama-namespace-gateway@index", "gateway", "index", true},
		{"orama-namespace-olric@my-ns", "olric", "my-ns", true},
		{"orama-node", "", "", false},
		{"orama-anyone-relay", "", "", false},
		{"orama-namespace-rqlite", "", "", false},  // template, no instance
		{"orama-namespace-@anchat", "", "", false}, // no role
		{"orama-namespace-rqlite@", "", "", false}, // no namespace
	} {
		role, namespace, ok := splitNamespaceUnit(tc.unit)
		if ok != tc.ok {
			t.Errorf("splitNamespaceUnit(%q) ok = %v, want %v", tc.unit, ok, tc.ok)
			continue
		}
		if role != tc.role || namespace != tc.namespace {
			t.Errorf("splitNamespaceUnit(%q) = %q, %q; want %q, %q",
				tc.unit, role, namespace, tc.role, tc.namespace)
		}
	}
}

// A template unit with no instance restarts nothing, so it is not a service to
// report on and must not be described as one.
func TestDescribe_namesTheTenantForANamespaceUnit(t *testing.T) {
	got := describe("orama-namespace-gateway@anchat")
	if got == "" {
		t.Fatal("a namespace unit must be described")
	}
	for _, want := range []string{"gateway", "anchat"} {
		if !strings.Contains(got, want) {
			t.Errorf("describe = %q, want it to mention %q", got, want)
		}
	}
}

func TestDescribe_namesTheSupervisor(t *testing.T) {
	if got := describe("orama-node"); got == "" {
		t.Error("orama-node is the supervisor and must be described")
	}
}

// The units the installer disables on purpose are not in the live list, so
// nothing describes them — and if one turned up, an empty description is
// better than claiming it is part of the node.
func TestDescribe_saysNothingAboutALeftoverUnit(t *testing.T) {
	for _, unit := range []string{"orama-ipfs", "orama-ipfs-cluster", "orama-olric", "orama-vault"} {
		if got := describe(unit); got != "" {
			t.Errorf("describe(%q) = %q; these are leftovers the installer disables", unit, got)
		}
	}
}

// The list of services must come from utils.GetProductionServices, which is
// what every other command uses to decide what this node runs: the global
// units that are not systemd.LeftoverHostUnits, plus the namespace instances
// discovered from the data directory. A list written out here would go stale
// the moment the unit names change, which is exactly what happened.
//
// This walks the source rather than running Handle, which needs systemd.
func TestHandle_readsTheLiveServiceList(t *testing.T) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "command.go", nil, 0)
	if err != nil {
		t.Fatalf("parse command.go: %v", err)
	}

	var calls []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		decl, ok := n.(*ast.FuncDecl)
		if !ok || decl.Name.Name != "Handle" {
			return true
		}
		ast.Inspect(decl.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok {
					calls = append(calls, pkg.Name+"."+sel.Sel.Name)
				}
			}
			return true
		})
		return false
	})

	for _, c := range calls {
		if c == "utils.GetProductionServices" {
			return
		}
	}
	t.Errorf("Handle does not call utils.GetProductionServices, so its service list "+
		"can go stale against the units that actually run. Calls found: %v", calls)
}
