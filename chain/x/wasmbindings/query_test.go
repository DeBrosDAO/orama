package wasmbindings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
	"github.com/DeBrosOfficial/network/chain/x/wasmbindings"
)

type fakeTokens struct{}

func (fakeTokens) Token(_ context.Context, req *tokentypes.QueryTokenRequest) (*tokentypes.QueryTokenResponse, error) {
	if req.Denom != "factory/x/gold" {
		return nil, fmt.Errorf("token %s not found", req.Denom)
	}
	return &tokentypes.QueryTokenResponse{Token: tokentypes.Token{Denom: req.Denom, Symbol: "GLD"}}, nil
}

type fakeTrees struct{ trees map[uint64]cnfttypes.Tree }

func (f fakeTrees) GetTree(_ context.Context, id uint64) (cnfttypes.Tree, error) {
	tree, ok := f.trees[id]
	if !ok {
		return cnfttypes.Tree{}, fmt.Errorf("tree %d does not exist", id)
	}
	return tree, nil
}

type fakeListings struct{}

func (fakeListings) Listing(_ context.Context, req *markettypes.QueryListingRequest) (*markettypes.QueryListingResponse, error) {
	return &markettypes.QueryListingResponse{Listing: markettypes.Listing{Id: req.Id, Seller: "s"}}, nil
}

const proofDepth = 4

// provenTree builds a tree with one leaf and returns it with that leaf and a valid proof.
func provenTree(t *testing.T) (cnfttypes.Tree, cnfttypes.Leaf, cnfttypes.MerkleProof) {
	t.Helper()
	leaf := cnfttypes.Leaf{
		AssetId: make([]byte, cnfttypes.HashSize), Owner: user.String(), MetadataCid: "bafy",
		CreatorHash: make([]byte, cnfttypes.HashSize), Nonce: 1, HashId: cnfttypes.HashIDSHA256,
	}
	hash, err := cnfttypes.HashLeaf(leaf)
	require.NoError(t, err)
	tree, err := cnfttypes.NewTree(1, 1, user.String(), proofDepth, 8, 0)
	require.NoError(t, err)
	require.NoError(t, tree.Append(tree.Root(), hash))
	siblings, root, err := cnfttypes.Proof([][]byte{hash}, 0, proofDepth)
	require.NoError(t, err)
	return *tree, leaf, cnfttypes.MerkleProof{Root: root, Index: 0, Siblings: siblings}
}

func newQuerier(trees map[uint64]cnfttypes.Tree) wasmbindings.Querier {
	return wasmbindings.NewQuerier(fakeTokens{}, fakeTrees{trees: trees}, fakeListings{})
}

func queryCtx(t *testing.T, limit uint64) sdk.Context {
	t.Helper()
	return msgCtx(t).WithGasMeter(storetypes.NewGasMeter(limit))
}

func ask(t *testing.T, q wasmbindings.Querier, ctx sdk.Context, request any) ([]byte, error) {
	t.Helper()
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	return q.Query(ctx, raw)
}

func TestQuerier_verifyProofAcceptsAValidProofAndChargesGas(t *testing.T) {
	tree, leaf, proof := provenTree(t)
	q := newQuerier(map[uint64]cnfttypes.Tree{1: tree})
	ctx := queryCtx(t, 1_000_000)

	out, err := ask(t, q, ctx, map[string]any{"cnft": map[string]any{"verify_proof": map[string]any{"tree_id": 1, "leaf": leaf, "proof": proof}}})
	require.NoError(t, err)
	var res wasmbindings.ProofResult
	require.NoError(t, json.Unmarshal(out, &res))
	require.True(t, res.Valid, res.Reason)
	want := wasmbindings.VerifyProofBaseGas + wasmbindings.VerifyProofPerSiblingGas*uint64(len(proof.Siblings))
	require.GreaterOrEqual(t, ctx.GasMeter().GasConsumed(), want)
}

func TestQuerier_verifyProofRejectsTamperedProofsWithoutAnError(t *testing.T) {
	tree, leaf, proof := provenTree(t)
	q := newQuerier(map[uint64]cnfttypes.Tree{1: tree})

	wrongLeaf := leaf
	wrongLeaf.Nonce = 99
	badSibling := cnfttypes.MerkleProof{Root: proof.Root, Index: proof.Index, Siblings: append([][]byte{bytes.Repeat([]byte{0xff}, cnfttypes.HashSize)}, proof.Siblings[1:]...)}
	unknownRoot := cnfttypes.MerkleProof{Root: make([]byte, cnfttypes.HashSize), Index: proof.Index, Siblings: proof.Siblings}
	for name, c := range map[string]struct {
		leaf  cnfttypes.Leaf
		proof cnfttypes.MerkleProof
	}{
		"other leaf":   {wrongLeaf, proof},
		"bad sibling":  {leaf, badSibling},
		"unknown root": {leaf, unknownRoot},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := ask(t, q, queryCtx(t, 1_000_000), map[string]any{"cnft": map[string]any{"verify_proof": map[string]any{"tree_id": 1, "leaf": c.leaf, "proof": c.proof}}})
			require.NoError(t, err)
			var res wasmbindings.ProofResult
			require.NoError(t, json.Unmarshal(out, &res))
			require.False(t, res.Valid)
			require.NotEmpty(t, res.Reason)
		})
	}
}

func TestQuerier_verifyProofOutOfGasAndBadInput(t *testing.T) {
	tree, leaf, proof := provenTree(t)
	q := newQuerier(map[uint64]cnfttypes.Tree{1: tree})
	request := map[string]any{"cnft": map[string]any{"verify_proof": map[string]any{"tree_id": 1, "leaf": leaf, "proof": proof}}}

	require.Panics(t, func() { _, _ = ask(t, q, queryCtx(t, wasmbindings.VerifyProofBaseGas-1), request) }, "the gas meter aborts a query that cannot pay")

	_, err := ask(t, q, queryCtx(t, 1_000_000), map[string]any{"cnft": map[string]any{"verify_proof": map[string]any{"tree_id": 7, "leaf": leaf, "proof": proof}}})
	require.ErrorContains(t, err, "does not exist")
	_, err = ask(t, q, queryCtx(t, 1_000_000), map[string]any{"cnft": map[string]any{"verify_proof": map[string]any{"tree_id": 1, "leaf": cnfttypes.Leaf{}, "proof": proof}}})
	require.ErrorIs(t, err, wasmbindings.ErrBadMessage)
}

func TestQuerier_treeTokenAndListing(t *testing.T) {
	tree, _, _ := provenTree(t)
	q := newQuerier(map[uint64]cnfttypes.Tree{1: tree})
	ctx := queryCtx(t, 1_000_000)

	out, err := ask(t, q, ctx, map[string]any{"cnft": map[string]any{"tree": map[string]any{"tree_id": 1}}})
	require.NoError(t, err)
	var info wasmbindings.TreeInfo
	require.NoError(t, json.Unmarshal(out, &info))
	require.Equal(t, uint64(1), info.ID)
	require.Equal(t, tree.Root(), info.Root)

	out, err = ask(t, q, ctx, map[string]any{"token": map[string]any{"info": map[string]any{"denom": "factory/x/gold"}}})
	require.NoError(t, err)
	require.Contains(t, string(out), "GLD")
	_, err = ask(t, q, ctx, map[string]any{"token": map[string]any{"info": map[string]any{"denom": "factory/x/none"}}})
	require.ErrorContains(t, err, "not found")

	out, err = ask(t, q, ctx, map[string]any{"market": map[string]any{"listing": map[string]any{"id": 5}}})
	require.NoError(t, err)
	require.Contains(t, string(out), `"id":5`)
}

func TestQuerier_unknownAndShieldedQueries(t *testing.T) {
	q := newQuerier(nil)
	ctx := queryCtx(t, 1_000_000)
	_, err := q.Query(ctx, json.RawMessage(`{}`))
	require.ErrorIs(t, err, wasmbindings.ErrBadMessage)
	_, err = q.Query(ctx, json.RawMessage(`{"token":{}}`))
	require.ErrorIs(t, err, wasmbindings.ErrBadMessage)
	_, err = q.Query(ctx, json.RawMessage(`{"bank":{}}`))
	require.ErrorIs(t, err, wasmbindings.ErrBadMessage)
	_, err = q.Query(ctx, json.RawMessage(`{"shielded":{"balance":{}}}`))
	require.ErrorIs(t, err, wasmbindings.ErrNotLinked)
}
