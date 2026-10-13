//go:build e2e_fleet

package chainassets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// The client side of a compressed NFT tree, written from x/cnft/types
// (leaf.go EncodeLeaf/LeafHash, merkle.go hashPair, proof.go Proof): the
// test keeps every leaf, recomputes the root and builds the proofs a wallet
// would, and the chain checks them.
const (
	hashSize     = 32
	hashIDSHA256 = 1
)

var emptyLeaf = make([]byte, hashSize)

func hashPair(l, r []byte) []byte {
	sum := sha256.Sum256(append(append([]byte{}, l...), r...))
	return sum[:]
}

// cnftTree is a full binary tree of 2^depth leaf hashes (zero = empty).
type cnftTree struct {
	depth  uint32
	leaves [][]byte
	next   int
}

func newTree(depth uint32) *cnftTree {
	m := &cnftTree{depth: depth, leaves: make([][]byte, 1<<depth)}
	for i := range m.leaves {
		m.leaves[i] = emptyLeaf
	}
	return m
}

// levels returns every level bottom-up; levels[depth][0] is the root.
func (m *cnftTree) levels() [][][]byte {
	out := [][][]byte{m.leaves}
	for cur := m.leaves; len(cur) > 1; {
		next := make([][]byte, len(cur)/2)
		for i := range next {
			next[i] = hashPair(cur[2*i], cur[2*i+1])
		}
		out = append(out, next)
		cur = next
	}
	return out
}

func (m *cnftTree) root() []byte { lv := m.levels(); return lv[len(lv)-1][0] }

func (m *cnftTree) siblings(index int) [][]byte {
	lv := m.levels()
	out := make([][]byte, m.depth)
	for h := 0; h < int(m.depth); h++ {
		out[h] = lv[h][index^1]
		index /= 2
	}
	return out
}

// proof is a MerkleProof against the current root.
func (m *cnftTree) proof(index int) map[string]any {
	sib := make([]any, 0, m.depth)
	for _, s := range m.siblings(index) {
		sib = append(sib, b64(s))
	}
	return map[string]any{"root": b64(m.root()), "index": index, "siblings": sib}
}

func (m *cnftTree) append(h []byte) int {
	i := m.next
	m.leaves[i] = h
	m.next++
	return i
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// asset is one compressed NFT as its owner tracks it.
type asset struct {
	id          []byte
	owner       string
	delegate    string
	cid         string
	creatorHash []byte
	nonce       uint64
	index       int
}

func newAsset(t *testing.T, owner, cid string, creatorHash []byte) asset {
	t.Helper()
	id := make([]byte, hashSize)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return asset{id: id, owner: owner, cid: cid, creatorHash: creatorHash}
}

func accBytes(t *testing.T, addr string) []byte {
	t.Helper()
	if addr == "" {
		return nil
	}
	b, err := chain.Bech32Decode(chain.AccountPrefix, addr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func lenBytes(dst, field []byte) []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(field)))
	return append(append(dst, n[:]...), field...)
}

// hash is x/cnft LeafHash of the asset.
func (a asset) hash(t *testing.T) []byte {
	t.Helper()
	var enc []byte
	enc = lenBytes(enc, a.id)
	enc = lenBytes(enc, accBytes(t, a.owner))
	enc = lenBytes(enc, accBytes(t, a.delegate))
	enc = lenBytes(enc, []byte(a.cid))
	enc = lenBytes(enc, a.creatorHash)
	enc = binary.BigEndian.AppendUint64(enc, a.nonce)
	enc = binary.BigEndian.AppendUint32(enc, hashIDSHA256)
	sum := sha256.Sum256(enc)
	return sum[:]
}

// leaf is the asset as an orama.cnft.v1.Leaf.
func (a asset) leaf() map[string]any {
	return map[string]any{"asset_id": b64(a.id), "owner": a.owner, "delegate": a.delegate, "metadata_cid": a.cid,
		"creator_hash": b64(a.creatorHash), "nonce": fmt.Sprint(a.nonce), "hash_id": hashIDSHA256}
}

func (a asset) mintLeaf() map[string]any {
	return map[string]any{"asset_id": b64(a.id), "owner": a.owner, "delegate": a.delegate, "metadata_cid": a.cid}
}

// creatorHashOf is x/cnft CreatorHash: sha256 of the collection creator's bytes.
func creatorHashOf(t *testing.T, creator string) []byte {
	t.Helper()
	sum := sha256.Sum256(accBytes(t, creator))
	return sum[:]
}

func cnftMsg(typ string, fields map[string]any) chain.Msg {
	return chain.NewMsg("/orama.cnft.v1."+typ, fields)
}

// firstResponseID is field 1 (the new id) of the transaction's first
// message response.
func firstResponseID(t *testing.T, r chain.Result) uint64 {
	t.Helper()
	resp, err := r.MsgResponses()
	if err != nil || len(resp) == 0 {
		t.Fatalf("transaction %s has no message response: %v", r.TxHash, err)
	}
	return resp[0].Varint(1)
}
