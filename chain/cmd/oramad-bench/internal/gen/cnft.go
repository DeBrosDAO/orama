package gen

import (
	"context"
	"crypto/sha256"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/client/tx"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ops"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

// CnftMint is the type name of the minting of a compressed NFT.
const CnftMint = "cnft-mint"

const (
	// metadataCID is the metadata every bench asset points at.
	metadataCID = "bafybench"
	// mintTreeSlack is how many times the leaves a run mints a tree is sized for.
	mintTreeSlack = 2
)

func init() { register(CnftMint, func() Generator { return &cnftMint{} }) }

// treeRef is a collection and the tree created under it for one account.
type treeRef struct {
	Collection uint64
	Tree       uint64
	Depth      uint32
}

// createTrees creates, for every account, a collection and a tree under it. The accounts work in
// parallel; each one's two transactions are in order.
func createTrees(ctx context.Context, env *Env, label string, depth uint32) ([]treeRef, error) {
	refs := make([]treeRef, len(env.Accounts))
	err := ops.Each(ctx, len(env.Accounts), env.Parallel, func(ctx context.Context, i int) error {
		acct := env.Accounts[i]
		var col cnfttypes.MsgCreateCollectionResponse
		name := fmt.Sprintf("%s-%d", label, i)
		if err := env.Chain.SubmitResponse(ctx, acct, &cnfttypes.MsgCreateCollection{Creator: acct.Address, Name: name}, &col); err != nil {
			return fmt.Errorf("create collection %s: %w", name, err)
		}
		var tree cnfttypes.MsgCreateTreeResponse
		create := &cnfttypes.MsgCreateTree{Creator: acct.Address, CollectionId: col.Id, Depth: depth, Buffer: cnfttypes.MaxBuffer}
		if err := env.Chain.SubmitResponse(ctx, acct, create, &tree); err != nil {
			return fmt.Errorf("create the tree of collection %d: %w", col.Id, err)
		}
		refs[i] = treeRef{Collection: col.Id, Tree: tree.Id, Depth: depth}
		return nil
	})
	return refs, err
}

// treeRoot reads a tree's current root from the chain.
func treeRoot(ctx context.Context, env *Env, id uint64) ([]byte, error) {
	var resp cnfttypes.QueryTreeResponse
	if err := env.Chain.Client.Query(ctx, "/orama.cnft.v1.Query/Tree", &cnfttypes.QueryTreeRequest{Id: id}, &resp); err != nil {
		return nil, fmt.Errorf("read tree %d: %w", id, err)
	}
	return resp.Tree.Root(), nil
}

// assetID is the 32-byte id of the j-th asset a run mints for an account.
func assetID(runID string, account, j int) []byte {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|asset|%d|%d", runID, account, j)))
	return sum[:]
}

// cnftMint mints one asset per transaction into a tree its signer created. The tree's changelog
// buffer lets a mint name any root from the last 2,048 changes, so every mint of a step names the
// root the step began with and the pre-signed transactions do not depend on each other's results.
type cnftMint struct {
	runID    string
	accounts []string
	trees    []treeRef
	anchors  [][]byte
}

func (m *cnftMint) Name() string { return CnftMint }

func (m *cnftMint) Describe() string {
	return "x/cnft MsgMint: one compressed NFT appended to a Merkle tree (depth sized for the run, changelog buffer 2048) its signer created"
}

func (m *cnftMint) Prepare(ctx context.Context, env *Env, total int) error {
	m.runID = env.RunID
	m.accounts = make([]string, len(env.Accounts))
	for i, a := range env.Accounts {
		m.accounts[i] = a.Address
	}
	perTree := (total/len(env.Accounts) + 1) * mintTreeSlack
	trees, err := createTrees(ctx, env, "mint-"+env.RunID, depthFor(perTree))
	if err != nil {
		return err
	}
	m.trees = trees
	return m.BeginStep(ctx, env)
}

// BeginStep reads each tree's current root, which the step's mints name.
func (m *cnftMint) BeginStep(ctx context.Context, env *Env) error {
	m.anchors = make([][]byte, len(m.trees))
	for i, t := range m.trees {
		root, err := treeRoot(ctx, env, t.Tree)
		if err != nil {
			return err
		}
		m.anchors[i] = root
	}
	return nil
}

func (m *cnftMint) Available() int { return Unlimited }

func (m *cnftMint) Plan(k int) (Planned, error) {
	acct := roundRobin(k, len(m.accounts))
	j := k / len(m.accounts)
	msg := &cnfttypes.MsgMint{
		Creator: m.accounts[acct], TreeId: m.trees[acct].Tree, Root: m.anchors[acct],
		Leaves: []cnfttypes.MintLeaf{{AssetId: assetID(m.runID, acct, j), Owner: m.accounts[acct], MetadataCid: metadataCID}},
	}
	return Planned{Account: acct, Msg: msg}, nil
}

// mintLeaves is the MsgMint messages that mint the leaves to their owner, at most
// cnfttypes.MaxMintBatch to a message, all naming the tree's root before the first of them.
func mintLeaves(acct tx.Account, treeID uint64, anchor []byte, leaves []cnfttypes.Leaf) []sdk.Msg {
	var msgs []sdk.Msg
	for start := 0; start < len(leaves); start += cnfttypes.MaxMintBatch {
		end := min(start+cnfttypes.MaxMintBatch, len(leaves))
		batch := make([]cnfttypes.MintLeaf, 0, end-start)
		for _, l := range leaves[start:end] {
			batch = append(batch, cnfttypes.MintLeaf{AssetId: l.AssetId, Owner: l.Owner, MetadataCid: l.MetadataCid})
		}
		msgs = append(msgs, &cnfttypes.MsgMint{Creator: acct.Address, TreeId: treeID, Root: anchor, Leaves: batch})
	}
	return msgs
}
