package orchard

import "errors"

// ErrTreeRejected means the tree could not take the commitments: the frontier is not one this
// package wrote, a commitment is not a canonical field element, or the tree is full.
var ErrTreeRejected = errors.New("note-commitment tree rejected the input")

// MaxFrontierLen is the largest encoded frontier: position (8), leaf (32), 32 ommers (32 each).
const MaxFrontierLen = 8 + 32 + 32*32

// NodeLen is the size of a commitment, a tree node and a root.
const NodeLen = 32

// Tree appends note commitments to the Orchard note-commitment tree and reads its root. Only the
// frontier (the right edge of the tree) is kept, as opaque bytes; an empty tree is nil. The
// Sinsemilla hashing is upstream orchard's, so this is available only in the cgo build.
type Tree struct{}

// NewTree returns the tree functions.
func NewTree() Tree { return Tree{} }
