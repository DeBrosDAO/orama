package upgrade

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The rolling upgrade plan says "leader — last, after leadership transfer".
// lifecycle.HandlePreUpgrade is what performs that transfer: it checks quorum,
// hands leadership to another voter and confirms one has taken it, refusing
// rather than warning when it cannot. Nothing called it, so the plan's promise
// was not kept and restarting the leader forced an election that failed every
// in-flight write.
//
// This walks the source rather than running a restart, because running one
// needs a cluster.

// callsIn returns the names of functions called inside fn.
func callsIn(t *testing.T, file, fn string) []string {
	t.Helper()

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	var calls []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		decl, ok := n.(*ast.FuncDecl)
		if !ok || decl.Name.Name != fn {
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
	return calls
}

func TestRestartServices_transfersLeadershipFirst(t *testing.T) {
	calls := callsIn(t, "orchestrator.go", "restartServices")

	found := false
	for _, c := range calls {
		if c == "lifecycle.HandlePreUpgrade" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("restartServices does not call lifecycle.HandlePreUpgrade, so the leader is "+
			"restarted without stepping down. Calls found: %v", calls)
	}
}

// The transfer has to happen before anything is restarted, not after.
func TestRestartServices_transfersBeforeRestarting(t *testing.T) {
	calls := callsIn(t, "orchestrator.go", "restartServices")

	preUpgrade, firstRestart := -1, -1
	for i, c := range calls {
		if c == "lifecycle.HandlePreUpgrade" && preUpgrade < 0 {
			preUpgrade = i
		}
		if strings.HasPrefix(c, "exec.Command") && firstRestart < 0 {
			firstRestart = i
		}
	}
	if preUpgrade < 0 {
		t.Fatal("restartServices does not transfer leadership at all")
	}
	if firstRestart >= 0 && preUpgrade > firstRestart {
		t.Error("leadership is transferred after services have already been touched")
	}
}

// The maintenance flag pre-upgrade writes keeps the node out of rotation for
// anything that reads it, so it has to be cleared once the node is serving.
func TestRestartServices_clearsTheMaintenanceFlag(t *testing.T) {
	calls := callsIn(t, "orchestrator.go", "restartServices")

	for _, c := range calls {
		if c == "lifecycle.ClearMaintenanceFlag" {
			return
		}
	}
	t.Errorf("restartServices never clears the maintenance flag it caused to be written: %v", calls)
}
