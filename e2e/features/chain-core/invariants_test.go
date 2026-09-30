//go:build e2e_fleet

package chaincore

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// protoRoot is the chain's proto tree, from this package's directory.
var protoRoot = filepath.Join("..", "..", "..", "chain", "proto", "orama")

// invariantsRPC is the declaration of a module's Invariants query.
var invariantsRPC = regexp.MustCompile(`(?m)^\s*rpc Invariants\(`)

// modulesDeclaringInvariants are the orama modules whose query.proto declares
// an Invariants rpc, from the proto tree the node was built from.
func modulesDeclaringInvariants(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(protoRoot, "*", "v1", "query.proto"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query.proto under %s: %v", protoRoot, err)
	}
	var modules []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if invariantsRPC.Match(raw) {
			modules = append(modules, filepath.Base(filepath.Dir(filepath.Dir(f))))
		}
	}
	sort.Strings(modules)
	return modules
}

// TestInvariants_everyModuleAnswersOnEveryValidator: every module that
// declares an Invariants query (docs/SECURITY_PLAYBOOKS.md: emission, fees,
// storage, nodes, relay, houses, token, market, power, shielded) is the one
// list every chain package's steps end with, answers on every validator, and
// holds. A module that gains an Invariants query and is not added to the
// list turns this red.
func TestInvariants_everyModuleAnswersOnEveryValidator(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	declared := modulesDeclaringInvariants(t)
	checked := append([]string(nil), chain.InvariantModules...)
	sort.Strings(checked)
	if len(declared) != len(checked) {
		t.Fatalf("the protos declare Invariants on %v, the helper checks %v", declared, checked)
	}
	for i := range declared {
		if declared[i] != checked[i] {
			t.Fatalf("the protos declare Invariants on %v, the helper checks %v", declared, checked)
		}
	}
	for _, n := range c.Nodes() {
		answers := c.NodeInvariants(t, n)
		for _, m := range declared {
			if _, ok := answers[m]; !ok {
				t.Errorf("%s: no invariants answer for %s", n.Name, m)
			}
		}
	}
	c.RequireInvariants(t, "the run's start")
}
