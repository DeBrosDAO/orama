package wasmbindings

import (
	"context"
	"encoding/json"

	sdk "github.com/cosmos/cosmos-sdk/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

const (
	// VerifyProofBaseGas is charged for every verify_proof query: one leaf hash and the root lookup.
	VerifyProofBaseGas uint64 = 20_000
	// VerifyProofPerSiblingGas is charged per proof sibling: one SHA-256 pair hash each.
	VerifyProofPerSiblingGas uint64 = 3_000
)

// Query is the JSON a contract puts in QueryRequest::Custom. Exactly one field is set.
//
//	{"cnft":{"verify_proof":{"tree_id":1,"leaf":{...},"proof":{...}}}}
type Query struct {
	Token  *TokenQuery  `json:"token,omitempty"`
	CNFT   *CNFTQuery   `json:"cnft,omitempty"`
	Market *MarketQuery `json:"market,omitempty"`
}

// TokenQuery reads x/token.
type TokenQuery struct {
	Info *struct {
		Denom string `json:"denom"`
	} `json:"info,omitempty"`
}

// CNFTQuery reads x/cnft.
type CNFTQuery struct {
	Tree *struct {
		TreeID uint64 `json:"tree_id"`
	} `json:"tree,omitempty"`
	VerifyProof *struct {
		TreeID uint64                `json:"tree_id"`
		Leaf   cnfttypes.Leaf        `json:"leaf"`
		Proof  cnfttypes.MerkleProof `json:"proof"`
	} `json:"verify_proof,omitempty"`
}

// MarketQuery reads x/market.
type MarketQuery struct {
	Listing *struct {
		ID uint64 `json:"id"`
	} `json:"listing,omitempty"`
}

// TreeInfo is the answer to a cnft tree query.
type TreeInfo struct {
	ID           uint64 `json:"id"`
	Creator      string `json:"creator"`
	CollectionID uint64 `json:"collection_id"`
	Depth        uint32 `json:"depth"`
	Root         []byte `json:"root"`
	Sequence     uint64 `json:"sequence"`
}

// ProofResult is the answer to a cnft verify_proof query. Reason is empty when Valid.
type ProofResult struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

// TokenReader is the part of x/token's query server the bindings use.
type TokenReader interface {
	Token(context.Context, *tokentypes.QueryTokenRequest) (*tokentypes.QueryTokenResponse, error)
}

// TreeReader is the part of x/cnft's keeper the bindings use.
type TreeReader interface {
	GetTree(ctx context.Context, id uint64) (cnfttypes.Tree, error)
}

// ListingReader is the part of x/market's query server the bindings use.
type ListingReader interface {
	Listing(context.Context, *markettypes.QueryListingRequest) (*markettypes.QueryListingResponse, error)
}

// Querier answers CosmWasm custom queries. Its Query method has wasmd's CustomQuerier shape.
type Querier struct {
	tokens   TokenReader
	trees    TreeReader
	listings ListingReader
}

// NewQuerier builds the custom querier.
func NewQuerier(tokens TokenReader, trees TreeReader, listings ListingReader) Querier {
	return Querier{tokens: tokens, trees: trees, listings: listings}
}

// Query decodes and answers one custom query. A shielded query returns NOT_LINKED.
func (q Querier) Query(ctx sdk.Context, raw json.RawMessage) ([]byte, error) {
	var req struct {
		Query
		Shielded json.RawMessage `json:"shielded,omitempty"`
	}
	if err := decodeStrict(raw, &req); err != nil {
		return nil, err
	}
	if req.Shielded != nil {
		return nil, ErrNotLinked
	}
	var (
		out any
		err error
	)
	switch {
	case req.Token != nil && req.Token.Info != nil:
		out, err = q.tokenInfo(ctx, req.Token.Info.Denom)
	case req.CNFT != nil && req.CNFT.Tree != nil:
		out, err = q.tree(ctx, req.CNFT.Tree.TreeID)
	case req.CNFT != nil && req.CNFT.VerifyProof != nil:
		v := req.CNFT.VerifyProof
		out, err = q.verifyProof(ctx, v.TreeID, v.Leaf, v.Proof)
	case req.Market != nil && req.Market.Listing != nil:
		out, err = q.listing(ctx, req.Market.Listing.ID)
	default:
		return nil, ErrBadMessage.Wrap("no known query variant")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func (q Querier) tokenInfo(ctx sdk.Context, denom string) (any, error) {
	res, err := q.tokens.Token(ctx, &tokentypes.QueryTokenRequest{Denom: denom})
	if err != nil {
		return nil, err
	}
	return res.Token, nil
}

func (q Querier) tree(ctx sdk.Context, id uint64) (any, error) {
	tree, err := q.trees.GetTree(ctx, id)
	if err != nil {
		return nil, err
	}
	return treeInfo(&tree), nil
}

func treeInfo(tree *cnfttypes.Tree) TreeInfo {
	return TreeInfo{
		ID: tree.Id, Creator: tree.Creator, CollectionID: tree.CollectionId,
		Depth: tree.Depth, Root: tree.Root(), Sequence: tree.Sequence,
	}
}

func (q Querier) listing(ctx sdk.Context, id uint64) (any, error) {
	res, err := q.listings.Listing(ctx, &markettypes.QueryListingRequest{Id: id})
	if err != nil {
		return nil, err
	}
	return res.Listing, nil
}

// verifyProof charges gas by proof length, then checks the proof against the tree's changelog
// buffer, the same check x/cnft runs before it changes a leaf. A proof that does not verify is an
// answer (Valid false), not an error; a missing tree or an unreadable leaf is an error.
func (q Querier) verifyProof(ctx sdk.Context, treeID uint64, leaf cnfttypes.Leaf, proof cnfttypes.MerkleProof) (any, error) {
	ctx.GasMeter().ConsumeGas(VerifyProofBaseGas+VerifyProofPerSiblingGas*uint64(len(proof.Siblings)), "orama cnft verify_proof")
	tree, err := q.trees.GetTree(ctx, treeID)
	if err != nil {
		return nil, err
	}
	if err := proof.ValidateBasic(); err != nil {
		return nil, ErrBadMessage.Wrapf("%v", err)
	}
	hash, err := cnfttypes.HashLeaf(leaf)
	if err != nil {
		return nil, ErrBadMessage.Wrapf("%v", err)
	}
	if err := tree.Prove(proof.Root, hash, proof.Index, proof.Siblings); err != nil {
		return ProofResult{Valid: false, Reason: err.Error()}, nil
	}
	return ProofResult{Valid: true}, nil
}
