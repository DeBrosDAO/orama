package types

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/bits"
	"sync"
)

// emptyNodes[level] is the hash of a complete empty subtree of that height.
// Level 0 is 32 zero bytes. The slices are immutable.
var (
	emptyNodes [MaxDepth + 1][]byte
	emptyOnce  sync.Once
)

func emptyNode(level uint32) []byte {
	emptyOnce.Do(func() {
		emptyNodes[0] = make([]byte, HashSize)
		for i := uint32(1); i <= MaxDepth; i++ {
			emptyNodes[i] = hashPair(emptyNodes[i-1], emptyNodes[i-1])
		}
	})
	return emptyNodes[level]
}

func hashPair(left, right []byte) []byte {
	var buf [HashSize * 2]byte
	copy(buf[:HashSize], left)
	copy(buf[HashSize:], right)
	sum := sha256.Sum256(buf[:])
	out := make([]byte, HashSize)
	copy(out, sum[:])
	return out
}

func hashToParent(node, sibling []byte, isLeft bool) []byte {
	if isLeft {
		return hashPair(node, sibling)
	}
	return hashPair(sibling, node)
}

func recompute(leaf []byte, proof [][]byte, index uint32) []byte {
	current := leaf
	for level, sibling := range proof {
		isLeft := (index>>uint(level))&1 == 0
		current = hashToParent(current, sibling, isLeft)
	}
	return current
}

func cloneProof(proof [][]byte) [][]byte {
	out := make([][]byte, len(proof))
	for i, node := range proof {
		out[i] = append([]byte(nil), node...)
	}
	return out
}

func isZeroHash(node []byte) bool {
	return len(node) == HashSize && bytes.Equal(node, emptyNode(0))
}

// NewTree builds an empty concurrent Merkle tree. The single buffered root is
// the hash of a full tree of empty leaves, and the rightmost index is 0.
func NewTree(id, collectionID uint64, creator string, depth, buffer, canopy uint32) (*Tree, error) {
	if err := ValidateTreeShape(depth, buffer, canopy); err != nil {
		return nil, err
	}
	t := &Tree{
		Id:           id,
		Creator:      creator,
		CollectionId: collectionID,
		Depth:        depth,
		BufferSize:   buffer,
		CanopyDepth:  canopy,
	}
	t.initialize()
	return t, nil
}

func (t *Tree) initialize() {
	depth := t.Depth
	proof := make([][]byte, depth)
	path := make([][]byte, depth)
	for i := uint32(0); i < depth; i++ {
		node := append([]byte(nil), emptyNode(i)...)
		proof[i] = node
		path[i] = append([]byte(nil), emptyNode(i)...)
	}
	t.ChangeLogs = make([]ChangeLog, t.BufferSize)
	t.ChangeLogs[0] = ChangeLog{
		Root:  append([]byte(nil), emptyNode(depth)...),
		Path:  path,
		Index: 0,
	}
	t.ActiveIndex = 0
	t.FilledBuffer = 1
	t.Sequence = 0
	t.Rightmost = RightmostProof{
		Proof: proof,
		Leaf:  append([]byte(nil), emptyNode(0)...),
		Index: 0,
	}
	t.initCanopy()
}

func (t *Tree) initCanopy() {
	n := CanopyNodes(t.CanopyDepth)
	if n == 0 {
		t.Canopy = nil
		return
	}
	t.Canopy = make([][]byte, n)
	end := uint32(1) << (t.CanopyDepth + 1)
	for heap := uint32(2); heap < end; heap++ {
		levelFromRoot := uint32(bits.Len32(heap) - 1)
		t.Canopy[heap-2] = append([]byte(nil), emptyNode(t.Depth-levelFromRoot)...)
	}
}

// Root returns a copy of the current Merkle root.
func (t *Tree) Root() []byte {
	if t == nil || len(t.ChangeLogs) == 0 || t.ActiveIndex >= uint64(len(t.ChangeLogs)) {
		return nil
	}
	return append([]byte(nil), t.ChangeLogs[t.ActiveIndex].Root...)
}

func (t *Tree) currentRoot() []byte {
	return t.ChangeLogs[t.ActiveIndex].Root
}

// RightmostIndex is the next leaf index an append will use.
func (t *Tree) RightmostIndex() uint32 {
	return t.Rightmost.Index
}

// ContainsRoot reports whether root is one of the changelog buffer's roots.
func (t *Tree) ContainsRoot(root []byte) bool {
	_, ok := t.findRoot(root)
	return ok
}

// BufferedRoots returns the buffered roots, newest first.
func (t *Tree) BufferedRoots() [][]byte {
	buf := uint64(t.BufferSize)
	if buf == 0 {
		return nil
	}
	out := make([][]byte, 0, t.FilledBuffer)
	for i := uint64(0); i < t.FilledBuffer; i++ {
		j := (t.ActiveIndex + buf - i%buf) % buf
		out = append(out, append([]byte(nil), t.ChangeLogs[j].Root...))
	}
	return out
}

func (t *Tree) findRoot(root []byte) (uint64, bool) {
	if len(root) != HashSize || t.BufferSize == 0 {
		return 0, false
	}
	buf := uint64(t.BufferSize)
	for i := uint64(0); i < t.FilledBuffer; i++ {
		j := (t.ActiveIndex + buf - i%buf) % buf
		if bytes.Equal(t.ChangeLogs[j].Root, root) {
			return j, true
		}
	}
	return 0, false
}

// AppendBatch appends leaves anchored at a root still in the changelog buffer.
// The anchor is checked once, then every leaf is appended at the on-chain
// rightmost index. On error the tree value is unspecified and must be discarded.
func (t *Tree) AppendBatch(anchor []byte, leaves [][]byte) ([]uint32, error) {
	if !t.ContainsRoot(anchor) {
		return nil, ErrRootNotInBuffer
	}
	indices := make([]uint32, 0, len(leaves))
	for _, leaf := range leaves {
		idx := t.Rightmost.Index
		if err := t.appendLeaf(leaf); err != nil {
			return nil, err
		}
		indices = append(indices, idx)
	}
	return indices, nil
}

// Append appends one leaf anchored at a buffered root.
func (t *Tree) Append(anchor, leaf []byte) error {
	_, err := t.AppendBatch(anchor, [][]byte{leaf})
	return err
}

func (t *Tree) appendLeaf(leaf []byte) error {
	if len(leaf) != HashSize || isZeroHash(leaf) {
		return fmt.Errorf("cannot append an empty leaf")
	}
	if uint64(t.Rightmost.Index) >= uint64(1)<<t.Depth {
		return fmt.Errorf("tree is full")
	}
	if len(t.Rightmost.Proof) != int(t.Depth) {
		return fmt.Errorf("rightmost proof length %d != depth %d", len(t.Rightmost.Proof), t.Depth)
	}
	if t.Rightmost.Index == 0 {
		return t.appendFirst(leaf)
	}
	return t.appendNext(leaf)
}

func (t *Tree) appendFirst(leaf []byte) error {
	proof := cloneProof(t.Rightmost.Proof)
	oldRoot := recompute(emptyNode(0), proof, 0)
	if !bytes.Equal(oldRoot, emptyNode(t.Depth)) {
		return fmt.Errorf("tree is not an empty tree")
	}
	return t.applyProof(oldRoot, emptyNode(0), append([]byte(nil), leaf...), proof, 0)
}

func (t *Tree) appendNext(leaf []byte) error {
	depth := int(t.Depth)
	index := t.Rightmost.Index
	intersection := bits.TrailingZeros32(index)
	change := make([][]byte, depth)
	intersectionNode := append([]byte(nil), t.Rightmost.Leaf...)
	node := append([]byte(nil), leaf...)
	last := index - 1

	for i := 0; i < depth; i++ {
		change[i] = append([]byte(nil), node...)
		switch {
		case i < intersection:
			sibling := emptyNode(uint32(i))
			isLeft := ((last >> uint(i)) & 1) == 0
			intersectionNode = hashToParent(intersectionNode, t.Rightmost.Proof[i], isLeft)
			node = hashToParent(node, sibling, true)
			t.Rightmost.Proof[i] = append([]byte(nil), sibling...)
		case i == intersection:
			node = hashToParent(node, intersectionNode, false)
			t.Rightmost.Proof[i] = intersectionNode
		default:
			isLeft := ((last >> uint(i)) & 1) == 0
			node = hashToParent(node, t.Rightmost.Proof[i], isLeft)
		}
	}

	t.bump()
	t.ChangeLogs[t.ActiveIndex] = ChangeLog{Root: node, Path: change, Index: index}
	t.writeCanopy(index, change)
	t.Rightmost.Index = index + 1
	t.Rightmost.Leaf = append([]byte(nil), leaf...)
	return nil
}

// SetLeaf replaces the leaf at index. The proof must verify against anchor, and
// anchor must still be in the changelog buffer. A proof of a leaf that has
// changed since that root is stale. On error the tree must be discarded.
func (t *Tree) SetLeaf(anchor, previous, next []byte, index uint32, siblings [][]byte) error {
	if uint64(index) >= uint64(1)<<t.Depth {
		return fmt.Errorf("leaf index %d is out of range for depth %d", index, t.Depth)
	}
	if index >= t.Rightmost.Index {
		return fmt.Errorf("leaf index %d is past the rightmost index %d", index, t.Rightmost.Index)
	}
	if len(next) != HashSize {
		return fmt.Errorf("replacement leaf must be %d bytes", HashSize)
	}
	proof, err := t.fillProof(index, siblings)
	if err != nil {
		return err
	}
	return t.applyProof(anchor, previous, next, proof, index)
}

// Prove checks that leaf currently occupies index. The proof may name any root
// still in the changelog buffer. A proof of an older leaf value is stale.
func (t *Tree) Prove(anchor, leaf []byte, index uint32, siblings [][]byte) error {
	if index >= t.Rightmost.Index {
		return fmt.Errorf("leaf index %d is past the rightmost index %d", index, t.Rightmost.Index)
	}
	proof, err := t.fillProof(index, siblings)
	if err != nil {
		return err
	}
	updated, unchanged, err := t.prepareProof(anchor, leaf, proof, index)
	if err != nil {
		return err
	}
	if !unchanged {
		return ErrStaleProof
	}
	if !bytes.Equal(recompute(updated, proof, index), t.currentRoot()) {
		return ErrInvalidProof
	}
	return nil
}

func (t *Tree) applyProof(anchor, previous, next []byte, proof [][]byte, index uint32) error {
	updated, unchanged, err := t.prepareProof(anchor, previous, proof, index)
	if err != nil {
		return err
	}
	if !unchanged {
		return ErrStaleProof
	}
	if !bytes.Equal(recompute(updated, proof, index), t.currentRoot()) {
		return ErrInvalidProof
	}
	t.bump()
	t.ChangeLogs[t.ActiveIndex].replace(index, next, proof)
	t.writeCanopy(index, t.ChangeLogs[t.ActiveIndex].Path)
	return t.syncRightmost(index, proof)
}

func (t *Tree) prepareProof(anchor, leaf []byte, proof [][]byte, index uint32) ([]byte, bool, error) {
	start, ok := t.findRoot(anchor)
	if !ok {
		return nil, false, ErrRootNotInBuffer
	}
	if !bytes.Equal(recompute(leaf, proof, index), anchor) {
		return nil, false, ErrInvalidProof
	}
	updated := append([]byte(nil), leaf...)
	idx := start
	buf := uint64(t.BufferSize)
	for {
		if idx == t.ActiveIndex {
			break
		}
		idx = (idx + 1) % buf
		if err := t.ChangeLogs[idx].applyToProof(index, proof, &updated, t.Depth); err != nil {
			return nil, false, err
		}
	}
	return updated, bytes.Equal(updated, leaf), nil
}

func (t *Tree) bump() {
	buf := uint64(t.BufferSize)
	t.ActiveIndex = (t.ActiveIndex + 1) % buf
	if t.FilledBuffer < buf {
		t.FilledBuffer++
	}
	t.Sequence++
}

func (c *ChangeLog) replace(index uint32, node []byte, proof [][]byte) {
	c.Index = index
	c.Path = make([][]byte, len(proof))
	current := append([]byte(nil), node...)
	for i, sibling := range proof {
		c.Path[i] = append([]byte(nil), current...)
		isLeft := (index>>uint(i))&1 == 0
		current = hashToParent(current, sibling, isLeft)
	}
	c.Root = current
}

func (c ChangeLog) applyToProof(leafIndex uint32, proof [][]byte, leaf *[]byte, depth uint32) error {
	if leafIndex == c.Index {
		if len(c.Path) == 0 {
			return fmt.Errorf("changelog path is empty")
		}
		*leaf = append([]byte(nil), c.Path[0]...)
		return nil
	}
	xor := leafIndex ^ c.Index
	padding := 32 - depth
	common := bits.LeadingZeros32(xor << padding)
	crit := int(depth) - 1 - common
	if crit < 0 || crit >= len(proof) || crit >= len(c.Path) {
		return fmt.Errorf("changelog critbit %d out of range", crit)
	}
	proof[crit] = append([]byte(nil), c.Path[crit]...)
	return nil
}

func (t *Tree) syncRightmost(index uint32, proof [][]byte) error {
	limit := uint32(1) << t.Depth
	if t.Rightmost.Index >= limit {
		return nil
	}
	if index < t.Rightmost.Index {
		leaf := append([]byte(nil), t.Rightmost.Leaf...)
		if err := t.ChangeLogs[t.ActiveIndex].applyToProof(t.Rightmost.Index-1, t.Rightmost.Proof, &leaf, t.Depth); err != nil {
			return err
		}
		t.Rightmost.Leaf = leaf
		return nil
	}
	if index != t.Rightmost.Index {
		return fmt.Errorf("set leaf index %d is past rightmost %d", index, t.Rightmost.Index)
	}
	t.Rightmost.Proof = cloneProof(proof)
	t.Rightmost.Index = index + 1
	path := t.ChangeLogs[t.ActiveIndex].Path
	if len(path) == 0 {
		return fmt.Errorf("changelog path is empty")
	}
	t.Rightmost.Leaf = append([]byte(nil), path[0]...)
	return nil
}

func (t *Tree) writeCanopy(leafIndex uint32, path [][]byte) {
	if t.CanopyDepth == 0 {
		return
	}
	end := uint32(1) << (t.CanopyDepth + 1)
	for level := 0; level < len(path); level++ {
		heap := ((uint32(1) << t.Depth) + leafIndex) >> uint(level)
		if heap >= 2 && heap < end {
			t.Canopy[heap-2] = append([]byte(nil), path[level]...)
		}
	}
}

// fillProof returns a depth-length sibling list. A proof may omit the uppermost
// canopyDepth siblings; those are read from the canopy, which tracks the current
// root. A proof against an older root must be full length.
func (t *Tree) fillProof(index uint32, siblings [][]byte) ([][]byte, error) {
	depth := int(t.Depth)
	if len(siblings) == depth {
		return cloneProof(siblings), nil
	}
	if t.CanopyDepth == 0 || len(siblings) != depth-int(t.CanopyDepth) {
		return nil, fmt.Errorf("proof has %d siblings, want %d", len(siblings), depth)
	}
	full := make([][]byte, depth)
	for i := range siblings {
		if len(siblings[i]) != HashSize {
			return nil, fmt.Errorf("proof sibling %d has length %d", i, len(siblings[i]))
		}
		full[i] = append([]byte(nil), siblings[i]...)
	}
	for level := len(siblings); level < depth; level++ {
		heap := ((uint32(1) << t.Depth) + index) >> uint(level)
		node, err := t.canopyNode(heap ^ 1)
		if err != nil {
			return nil, err
		}
		full[level] = node
	}
	return full, nil
}

func (t *Tree) canopyNode(heap uint32) ([]byte, error) {
	end := uint32(1) << (t.CanopyDepth + 1)
	if heap < 2 || heap >= end {
		return nil, fmt.Errorf("heap index %d is outside the canopy", heap)
	}
	idx := heap - 2
	if int(idx) >= len(t.Canopy) || len(t.Canopy[idx]) != HashSize {
		return nil, fmt.Errorf("canopy node %d is missing", heap)
	}
	return append([]byte(nil), t.Canopy[idx]...), nil
}
