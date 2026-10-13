//go:build e2e_fleet

package chainassets

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// TestCnft_proofRefusals: a proof against a root the tree never had, a proof
// whose sibling was altered, and a proof that was valid before the leaf
// changed (stale: its root is still in the changelog buffer) are refused
// (x/cnft/types errors.go); so is a write by someone who is neither owner nor
// delegate, and a transfer that changes nothing.
func TestCnft_proofRefusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	a := c.FundedValidator(t, 0, chain.Orama(1))
	b := c.FundedValidator(t, 1, chain.Orama(1))
	f := newFixture(t, c, a)
	x := f.mint(t, a.Address)[0]
	oldProof := f.model.proof(x.index)
	transfer := func(signer chain.Key, cur asset, to string, proof map[string]any) chain.Result {
		return c.Submit(t, signer, chain.TxOptions{}, cnftMsg("MsgTransfer", map[string]any{"signer": signer.Address,
			"tree_id": fmt.Sprint(f.tree), "current": cur.leaf(), "new_owner": to, "new_delegate": "", "proof": proof}))
	}
	unknown := f.model.proof(x.index)
	unknown["root"] = b64(hashPair(f.model.root(), f.model.root()))
	chain.RequireRefused(t, "root never in the buffer", transfer(a, x, b.Address, unknown), "merkle root is not in the changelog buffer")
	altered := f.model.proof(x.index)
	sib := altered["siblings"].([]any)
	sib[0] = b64(hashPair(emptyLeaf, emptyLeaf))
	chain.RequireRefused(t, "altered sibling", transfer(a, x, b.Address, altered), "invalid merkle proof")
	chain.RequireRefused(t, "write by a stranger", transfer(b, x, b.Address, f.model.proof(x.index)), "signer is not the owner or delegate")
	chain.RequireRefused(t, "transfer that changes nothing", transfer(a, x, a.Address, f.model.proof(x.index)), "transfer does not change the leaf")
	moved := f.transfer(t, a, x, b.Address)
	chain.RequireRefused(t, "stale proof of the old leaf", transfer(a, x, b.Address, oldProof), "stale merkle proof")
	f.transfer(t, b, moved, a.Address)
	c.RequireInvariants(t, "refused cNFT proofs")
}

// TestCnft_ownershipRules: only the collection creator makes a tree, only the
// tree creator mints and records snapshots, only the leaf owner decompresses,
// and only the decompressed owner compresses; a full tree takes no more
// leaves; an asset id minted twice is refused in one batch.
func TestCnft_ownershipRules(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	a := c.FundedValidator(t, 0, chain.Orama(1))
	b := c.FundedValidator(t, 2, chain.Orama(1))
	f := newFixture(t, c, a)
	chain.RequireRefused(t, "tree in someone else's collection", c.Submit(t, b, chain.TxOptions{}, cnftMsg("MsgCreateTree", map[string]any{
		"creator": b.Address, "collection_id": fmt.Sprint(f.collection), "depth": 3, "buffer": 2, "canopy": 0})),
		"only the collection creator can create a tree")
	x := newAsset(t, b.Address, "bafk-e2e", creatorHashOf(t, a.Address))
	chain.RequireRefused(t, "mint by a non-creator", c.Submit(t, b, chain.TxOptions{}, cnftMsg("MsgMint", map[string]any{
		"creator": b.Address, "tree_id": fmt.Sprint(f.tree), "root": b64(f.model.root()), "leaves": []any{x.mintLeaf()}})),
		"only the tree creator can mint")
	chain.RequireRefused(t, "duplicate asset in a batch", c.Submit(t, a, chain.TxOptions{}, cnftMsg("MsgMint", map[string]any{
		"creator": a.Address, "tree_id": fmt.Sprint(f.tree), "root": b64(f.model.root()), "leaves": []any{x.mintLeaf(), x.mintLeaf()}})),
		"duplicate asset_id in mint batch")
	chain.RequireRefused(t, "snapshot by a non-creator", c.Submit(t, b, chain.TxOptions{}, cnftMsg("MsgRecordSnapshot", map[string]any{
		"creator": b.Address, "tree_id": fmt.Sprint(f.tree), "cid": "bafk-e2e"})), "only the tree creator can record a snapshot")
	own := f.mint(t, a.Address)[0]
	chain.RequireRefused(t, "decompress by a non-owner", c.Submit(t, b, chain.TxOptions{}, cnftMsg("MsgDecompress", map[string]any{
		"owner": b.Address, "tree_id": fmt.Sprint(f.tree), "current": own.leaf(), "proof": f.model.proof(own.index)})),
		"decompress signer is not the leaf owner")
	chain.RequireRefused(t, "compress what is not decompressed", c.Submit(t, a, chain.TxOptions{}, cnftMsg("MsgCompress", map[string]any{
		"owner": a.Address, "asset_id": b64(own.id), "root": b64(f.model.root())})), "is not decompressed")
	fillTree(t, f)
	chain.RequireRefused(t, "mint into a full tree", c.Submit(t, a, chain.TxOptions{}, cnftMsg("MsgMint", map[string]any{
		"creator": a.Address, "tree_id": fmt.Sprint(f.tree), "root": b64(f.model.root()),
		"leaves": []any{newAsset(t, a.Address, "bafk-e2e-full", creatorHashOf(t, a.Address)).mintLeaf()}})), "tree is full")
}

// fillTree mints until the tree's 2^depth leaves are used.
func fillTree(t *testing.T, f *cnftFixture) {
	t.Helper()
	left := len(f.model.leaves) - f.model.next
	owners := make([]string, left)
	for i := range owners {
		owners[i] = f.k.Address
	}
	if left > 0 {
		f.mint(t, owners...)
	}
}

// TestCnft_shapeRefusals: collection names of 0 and 65 bytes, a royalty over
// 100%, tree depths 0 and 31, buffers 0 and 2049, a canopy not below the
// depth, a mint batch of 0 or 65 leaves, and queries of ids that do not exist
// (boundary values, x/cnft/types keys.go and deposit.go).
func TestCnft_shapeRefusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	a := c.FundedValidator(t, 0, chain.Orama(1))
	col := func(name string, bps int) chain.Msg {
		return cnftMsg("MsgCreateCollection", map[string]any{"creator": a.Address, "name": name, "royalty_bps": bps})
	}
	tree := func(depth, buffer, canopy int) chain.Msg {
		return cnftMsg("MsgCreateTree", map[string]any{"creator": a.Address, "collection_id": "1", "depth": depth, "buffer": buffer, "canopy": canopy})
	}
	cases := map[string]struct {
		m    chain.Msg
		want string
	}{
		"empty collection name":     {col("", 0), "collection name must be 1..64 bytes"},
		"65-byte collection name":   {col(strings.Repeat("n", 65), 0), "collection name must be 1..64 bytes"},
		"royalty over 100%":         {col("e2e", 10_001), "royalty_bps 10001 exceeds 10000"},
		"depth 0":                   {tree(0, 1, 0), "tree depth must be in [1, 30]"},
		"depth 31":                  {tree(31, 1, 0), "tree depth must be in [1, 30]"},
		"buffer 0":                  {tree(3, 0, 0), "tree buffer must be in [1, 2048]"},
		"buffer 2049":               {tree(3, 2049, 0), "tree buffer must be in [1, 2048]"},
		"canopy equal to the depth": {tree(3, 1, 3), "tree canopy must be less than depth"},
		"empty mint batch": {cnftMsg("MsgMint", map[string]any{"creator": a.Address, "tree_id": "1",
			"root": b64(emptyLeaf), "leaves": []any{}}), "mint batch must contain 1..64 leaves"},
	}
	for name, tc := range cases {
		chain.RequireRefused(t, name, c.Submit(t, a, chain.TxOptions{}, tc.m), tc.want)
	}
	for _, q := range [][]string{{"collection", "999999999"}, {"tree", "999999999"}} {
		if out := c.QueryFails(t, a.Node, append([]string{"cnft"}, q...)...); !chain.NotFound(out) {
			t.Errorf("cnft %v: %s", q, out)
		}
	}
}
