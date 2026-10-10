//go:build cgo && orchardffi

package orchard

import (
	"bytes"
	"errors"
	"testing"
)

func vectorCommitments(t *testing.T, name string) (anchor [NodeLen]byte, cmx [][NodeLen]byte) {
	t.Helper()
	bundle, _, _ := loadVector(t, name)
	n := int(bundle[0])
	for i := 0; i < n; i++ {
		var c [NodeLen]byte
		copy(c[:], bundle[1+i*actionLen+96:])
		cmx = append(cmx, c)
	}
	copy(anchor[:], bundle[1+n*actionLen+9:])
	return anchor, cmx
}

// The anchor of a shielding bundle is the empty tree's root, so the FFI's empty root is the
// value the circuit was proven against.
func TestTree_emptyRootIsTheVectorAnchor(t *testing.T) {
	for _, name := range vectorNames {
		anchor, _ := vectorCommitments(t, name)
		root, err := NewTree().EmptyRoot()
		if err != nil {
			t.Fatal(err)
		}
		if root != anchor {
			t.Fatalf("%s: empty root %x, vector anchor %x", name, root, anchor)
		}
	}
}

func TestTree_appendIsDeterministicAndIncremental(t *testing.T) {
	_, cmx := vectorCommitments(t, "ironwood-2-action")
	tree := NewTree()
	all, rootAll, err := tree.Append(nil, cmx)
	if err != nil {
		t.Fatal(err)
	}
	one, _, err := tree.Append(nil, cmx[:1])
	if err != nil {
		t.Fatal(err)
	}
	two, rootTwo, err := tree.Append(one, cmx[1:])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(all, two) || rootAll != rootTwo {
		t.Fatal("appending in two steps differs from appending at once")
	}
	if empty, _ := tree.EmptyRoot(); rootAll == empty {
		t.Fatal("a tree with notes has the empty root")
	}
}

func TestTree_rejectsBadInput(t *testing.T) {
	tree := NewTree()
	var notCanonical [NodeLen]byte
	for i := range notCanonical {
		notCanonical[i] = 0xff
	}
	if _, _, err := tree.Append(nil, [][NodeLen]byte{notCanonical}); !errors.Is(err, ErrTreeRejected) {
		t.Fatalf("non-canonical commitment: %v", err)
	}
	if _, _, err := tree.Append([]byte{1, 2, 3}, nil); !errors.Is(err, ErrTreeRejected) {
		t.Fatalf("garbage frontier: %v", err)
	}
}
