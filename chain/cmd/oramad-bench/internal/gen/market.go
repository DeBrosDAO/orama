package gen

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
)

// MarketList is the type name of the listing of a compressed NFT for sale.
const MarketList = "market-list"

// listingPrice is what every bench asset is listed for, in norama.
const listingPrice = 1_000

func init() { register(MarketList, func() Generator { return &marketList{} }) }

// marketList lists assets each account minted to itself in setup. A listing only proves the leaf
// against the tree and records the sale; it does not change the tree, so the proofs computed once
// stay valid for the whole run.
type marketList struct {
	accounts []string
	trees    []treeRef
	leaves   [][]cnfttypes.Leaf
	siblings [][][][]byte
	roots    [][]byte
}

func (m *marketList) Name() string { return MarketList }

func (m *marketList) Describe() string {
	return "x/market MsgList: a compressed NFT the signer owns listed for sale, with its Merkle proof"
}

func (m *marketList) Prepare(ctx context.Context, env *Env, total int) error {
	n := len(env.Accounts)
	per := total/n + 1
	trees, err := createTrees(ctx, env, "list-"+env.RunID, depthFor(per))
	if err != nil {
		return err
	}
	m.trees = trees
	m.accounts = make([]string, n)
	m.leaves = make([][]cnfttypes.Leaf, n)
	m.siblings = make([][][][]byte, n)
	m.roots = make([][]byte, n)
	for i, a := range env.Accounts {
		m.accounts[i] = a.Address
	}
	env.Log("minting %d assets to each of %d accounts", per, n)
	return ops.Each(ctx, n, env.Parallel, func(ctx context.Context, i int) error {
		return m.mintFor(ctx, env, i, per)
	})
}

// mintFor mints per assets to account i in its tree and works out each asset's proof.
func (m *marketList) mintFor(ctx context.Context, env *Env, i, per int) error {
	acct := env.Accounts[i]
	creator := sdk.MustAccAddressFromBech32(acct.Address)
	leaves := make([]cnfttypes.Leaf, per)
	hashes := make([][]byte, per)
	for j := range leaves {
		leaves[j] = cnfttypes.Leaf{
			AssetId: assetID(env.RunID, i, j), Owner: acct.Address, MetadataCid: metadataCID,
			CreatorHash: cnfttypes.CreatorHash(creator), HashId: cnfttypes.HashIDSHA256,
		}
		h, err := cnfttypes.HashLeaf(leaves[j])
		if err != nil {
			return fmt.Errorf("hash asset %d: %w", j, err)
		}
		hashes[j] = h
	}
	anchor, err := treeRoot(ctx, env, m.trees[i].Tree)
	if err != nil {
		return err
	}
	if err := env.Chain.Batch(ctx, acct, 0, ops.DefaultBatchTimeout, mintLeaves(acct, m.trees[i].Tree, anchor, leaves)...); err != nil {
		return fmt.Errorf("mint the assets of account %d: %w", i, err)
	}
	siblings, root, err := proofSet(hashes, m.trees[i].Depth)
	if err != nil {
		return err
	}
	onChain, err := treeRoot(ctx, env, m.trees[i].Tree)
	if err != nil {
		return err
	}
	if !bytes.Equal(root, onChain) {
		return fmt.Errorf("tree %d has root %x after the mints, the leaves compute %x: the mints did not all execute", m.trees[i].Tree, onChain, root)
	}
	m.leaves[i], m.siblings[i], m.roots[i] = leaves, siblings, root
	return nil
}

func (m *marketList) Available() int { return len(m.accounts) * len(m.leaves[0]) }

func (m *marketList) Plan(k int) (Planned, error) {
	acct := roundRobin(k, len(m.accounts))
	j := k / len(m.accounts)
	if j >= len(m.leaves[acct]) {
		return Planned{}, fmt.Errorf("listing %d: account %d minted only %d assets", k, acct, len(m.leaves[acct]))
	}
	msg := &markettypes.MsgList{
		Seller: m.accounts[acct], TreeId: m.trees[acct].Tree, Leaf: m.leaves[acct][j],
		Proof: cnfttypes.MerkleProof{Root: m.roots[acct], Index: uint32(j), Siblings: m.siblings[acct][j]},
		Price: math.NewInt(listingPrice),
	}
	return Planned{Account: acct, Msg: msg}, nil
}
