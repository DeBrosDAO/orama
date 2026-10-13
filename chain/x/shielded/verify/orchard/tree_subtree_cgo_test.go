//go:build cgo && orchardffi

package orchard

import (
	"errors"
	"testing"
)

// A frontier that has not just filled a 2^16-leaf subtree has no subtree root, and an empty one
// has none either; the filled case is checked against the Rust crate's own recombination test
// (orchardffi/tests/tree.rs), which needs 2^16 real appends.
func TestTree_subtreeRootRefusesAFrontierThatClosesNoSubtree(t *testing.T) {
	_, cmx := vectorCommitments(t, "ironwood-2-action")
	tree := NewTree()
	frontier, _, err := tree.Append(nil, cmx)
	if err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string][]byte{"two leaves": frontier, "empty": nil, "garbage": {1, 2, 3}} {
		if _, err := tree.SubtreeRoot(in); !errors.Is(err, ErrTreeRejected) {
			t.Fatalf("%s: got %v, want ErrTreeRejected", name, err)
		}
	}
}
