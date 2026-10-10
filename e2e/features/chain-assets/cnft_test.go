//go:build e2e_fleet

package chainassets

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// Tree shape of the tests: 16 leaves, an 8-root changelog buffer, no canopy
// (so a proof carries every sibling).
const (
	treeDepth  = 4
	treeBuffer = 8
	royaltyBps = 250
)

// cnftFixture is a collection and a tree created by k, with the client-side
// model of the tree.
type cnftFixture struct {
	c          *chain.Chain
	k          chain.Key
	collection uint64
	tree       uint64
	model      *cnftTree
}

// newFixture creates a collection (royalty 2.5%) and a tree; the tree's
// deposit is locked through x/fees from the creator's earnings. Neither can
// be deleted (x/cnft has no such message); they stay on the disposable chain.
func newFixture(t *testing.T, c *chain.Chain, k chain.Key) *cnftFixture {
	t.Helper()
	col := chain.RequireOK(t, "create collection", c.Submit(t, k, chain.TxOptions{}, cnftMsg("MsgCreateCollection", map[string]any{
		"creator": k.Address, "name": chain.UniqueID(t, "e2e-col-"), "royalty_bps": royaltyBps})))
	f := &cnftFixture{c: c, k: k, collection: firstResponseID(t, col), model: newTree(treeDepth)}
	tr := chain.RequireOK(t, "create tree", c.Submit(t, k, chain.TxOptions{}, cnftMsg("MsgCreateTree", map[string]any{
		"creator": k.Address, "collection_id": fmt.Sprint(f.collection), "depth": treeDepth, "buffer": treeBuffer, "canopy": 0})))
	f.tree = firstResponseID(t, tr)
	if got := f.chainRoot(t); got != b64(f.model.root()) {
		t.Fatalf("new tree root %s, the empty depth-%d tree is %s", got, treeDepth, b64(f.model.root()))
	}
	return f
}

// chainRoot is the tree's current root as the chain reports it.
func (f *cnftFixture) chainRoot(t *testing.T) string {
	t.Helper()
	var r struct {
		Tree struct {
			ActiveIndex chain.Int `json:"active_index"`
			ChangeLogs  []struct {
				Root string `json:"root"`
			} `json:"change_logs"`
		} `json:"tree"`
	}
	f.c.Query(t, f.k.Node, &r, "cnft", "tree", fmt.Sprint(f.tree))
	i := int(r.Tree.ActiveIndex.Int64())
	if i >= len(r.Tree.ChangeLogs) {
		t.Fatalf("tree %d active index %d beyond %d changelogs", f.tree, i, len(r.Tree.ChangeLogs))
	}
	return r.Tree.ChangeLogs[i].Root
}

// mint appends assets owned by owner; the anchor is the current root.
func (f *cnftFixture) mint(t *testing.T, owners ...string) []asset {
	t.Helper()
	ch := creatorHashOf(t, f.k.Address)
	var assets []asset
	var leaves []any
	for i, o := range owners {
		a := newAsset(t, o, fmt.Sprintf("bafk-e2e-%d", i), ch)
		assets = append(assets, a)
		leaves = append(leaves, a.mintLeaf())
	}
	anchor := b64(f.model.root())
	chain.RequireOK(t, "mint", f.c.Submit(t, f.k, chain.TxOptions{}, cnftMsg("MsgMint", map[string]any{
		"creator": f.k.Address, "tree_id": fmt.Sprint(f.tree), "root": anchor, "leaves": leaves})))
	for i := range assets {
		assets[i].index = f.model.append(assets[i].hash(t))
	}
	f.requireRoot(t, "after mint")
	return assets
}

func (f *cnftFixture) requireRoot(t *testing.T, when string) {
	t.Helper()
	if got, want := f.chainRoot(t), b64(f.model.root()); got != want {
		t.Fatalf("tree %d root %s %s, the client computed %s", f.tree, got, when, want)
	}
}

// TestCnft_lifecycle: mint two assets, transfer one to another validator and
// back, update its metadata, decompress it (the leaf empties and the asset is
// queryable), compress it again (appended with the next nonce), record a
// snapshot, and burn the other; after every step the chain's root equals the
// root the client computes (docs/whitepaper/technical-reference/vol2/45-nfts-and-the-market.md x/cnft; x/cnft/types merkle.go).
func TestCnft_lifecycle(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	a := c.FundedValidator(t, 0, chain.Orama(1))
	b := c.FundedValidator(t, 1, chain.Orama(1))
	f := newFixture(t, c, a)
	var d struct {
		Deposit struct {
			Owner string `json:"owner"`
		} `json:"deposit"`
	}
	c.Query(t, a.Node, &d, "fees", "deposit", fmt.Sprintf("cnft/tree/%d", f.tree))
	if d.Deposit.Owner != a.Address {
		t.Errorf("tree deposit owner %q, want %s", d.Deposit.Owner, a.Address)
	}
	var col struct {
		Collection struct {
			Creator    string `json:"creator"`
			RoyaltyBps int    `json:"royalty_bps"`
		} `json:"collection"`
	}
	c.Query(t, a.Node, &col, "cnft", "collection", fmt.Sprint(f.collection))
	if col.Collection.Creator != a.Address || col.Collection.RoyaltyBps != royaltyBps {
		t.Errorf("collection %+v", col.Collection)
	}
	assets := f.mint(t, a.Address, a.Address)
	x := f.transfer(t, a, assets[0], b.Address)
	x = f.transfer(t, b, x, a.Address)
	x = f.updateMetadata(t, a, x, "bafk-e2e-updated")
	f.decompress(t, a, x)
	f.compress(t, a, x)
	f.burn(t, a, assets[1])
	snap := chain.RequireOK(t, "record snapshot", c.Submit(t, a, chain.TxOptions{}, cnftMsg("MsgRecordSnapshot", map[string]any{
		"creator": a.Address, "tree_id": fmt.Sprint(f.tree), "cid": "bafk-e2e-snapshot"})))
	var s struct {
		Snapshots []struct {
			Cid string `json:"cid"`
		} `json:"snapshots"`
	}
	c.Query(t, a.Node, &s, "cnft", "snapshots", fmt.Sprint(f.tree))
	if len(s.Snapshots) != 1 || s.Snapshots[0].Cid != "bafk-e2e-snapshot" || firstResponseID(t, snap) != 1 {
		t.Errorf("snapshots %+v", s.Snapshots)
	}
	c.RequireInvariants(t, "a cNFT lifecycle")
}

func (f *cnftFixture) transfer(t *testing.T, signer chain.Key, x asset, to string) asset {
	t.Helper()
	msg := cnftMsg("MsgTransfer", map[string]any{"signer": signer.Address, "tree_id": fmt.Sprint(f.tree), "current": x.leaf(),
		"new_owner": to, "new_delegate": "", "proof": f.model.proof(x.index)})
	chain.RequireOK(t, "transfer", f.c.Submit(t, signer, chain.TxOptions{}, msg))
	x.owner, x.delegate, x.nonce = to, "", x.nonce+1
	f.model.leaves[x.index] = x.hash(t)
	f.requireRoot(t, "after transfer")
	return x
}

func (f *cnftFixture) updateMetadata(t *testing.T, signer chain.Key, x asset, cid string) asset {
	t.Helper()
	msg := cnftMsg("MsgUpdateMetadata", map[string]any{"signer": signer.Address, "tree_id": fmt.Sprint(f.tree), "current": x.leaf(),
		"new_metadata_cid": cid, "proof": f.model.proof(x.index)})
	chain.RequireOK(t, "update metadata", f.c.Submit(t, signer, chain.TxOptions{}, msg))
	x.cid, x.nonce = cid, x.nonce+1
	f.model.leaves[x.index] = x.hash(t)
	f.requireRoot(t, "after metadata update")
	return x
}

func (f *cnftFixture) decompress(t *testing.T, owner chain.Key, x asset) {
	t.Helper()
	msg := cnftMsg("MsgDecompress", map[string]any{"owner": owner.Address, "tree_id": fmt.Sprint(f.tree), "current": x.leaf(),
		"proof": f.model.proof(x.index)})
	chain.RequireOK(t, "decompress", f.c.Submit(t, owner, chain.TxOptions{}, msg))
	f.model.leaves[x.index] = emptyLeaf
	f.requireRoot(t, "after decompress")
	var d struct {
		Asset struct {
			Owner string `json:"owner"`
		} `json:"asset"`
	}
	f.c.Query(t, owner.Node, &d, "cnft", "decompressed", hex.EncodeToString(x.id))
	if d.Asset.Owner != owner.Address {
		t.Errorf("decompressed asset owner %q, want %s", d.Asset.Owner, owner.Address)
	}
	again := f.c.Submit(t, owner, chain.TxOptions{}, msg)
	chain.RequireRefused(t, "decompress twice", again, "is already decompressed")
}

func (f *cnftFixture) compress(t *testing.T, owner chain.Key, x asset) asset {
	t.Helper()
	msg := cnftMsg("MsgCompress", map[string]any{"owner": owner.Address, "asset_id": b64(x.id), "root": b64(f.model.root())})
	chain.RequireOK(t, "compress", f.c.Submit(t, owner, chain.TxOptions{}, msg))
	x.nonce++
	x.index = f.model.append(x.hash(t))
	f.requireRoot(t, "after compress")
	if out := f.c.QueryFails(t, owner.Node, "cnft", "decompressed", hex.EncodeToString(x.id)); !strings.Contains(out, "is not decompressed") {
		t.Errorf("asset still decompressed after compress: %s", out)
	}
	return x
}

func (f *cnftFixture) burn(t *testing.T, signer chain.Key, x asset) {
	t.Helper()
	msg := cnftMsg("MsgBurn", map[string]any{"signer": signer.Address, "tree_id": fmt.Sprint(f.tree), "current": x.leaf(),
		"proof": f.model.proof(x.index)})
	chain.RequireOK(t, "burn", f.c.Submit(t, signer, chain.TxOptions{}, msg))
	f.model.leaves[x.index] = emptyLeaf
	f.requireRoot(t, "after burn")
}
