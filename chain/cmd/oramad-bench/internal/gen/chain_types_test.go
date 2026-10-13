package gen

import (
	"bytes"
	"testing"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/account"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func TestStorageDeal_nonceIsUniquePerTransactionAndPerRun(t *testing.T) {
	a := addrs(t, 2)
	c := storagetypes.PieceCommitment{Root: bytes.Repeat([]byte{1}, 32), RealLeafCount: 1, PaddedLeafCount: 1, PieceBytes: 1024}
	one := &storageDeal{runID: "run1", accounts: a, commitment: c}
	two := &storageDeal{runID: "run2", accounts: a, commitment: c}
	seen := map[string]bool{}
	for _, g := range []*storageDeal{one, two} {
		for k := 0; k < 20; k++ {
			p, err := g.Plan(k)
			if err != nil {
				t.Fatal(err)
			}
			msg := p.Msg.(*storagetypes.MsgCreateDeal)
			if err := msg.ValidateBasic(); err != nil {
				t.Fatalf("k=%d: %v", k, err)
			}
			if seen[string(msg.DealNonce)] {
				t.Fatalf("run %s transaction %d repeats a deal nonce", g.runID, k)
			}
			seen[string(msg.DealNonce)] = true
		}
	}
}

func TestRegisterNode_bindingVerifiesAndEveryNodeIsDistinct(t *testing.T) {
	const chainID = "orama-localnet-1"
	g := &registerNode{chainID: chainID, runID: "run1", accounts: addrs(t, 3)}
	ids, hots, pubs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for k := 0; k < 12; k++ {
		p, err := g.Plan(k)
		if err != nil {
			t.Fatal(err)
		}
		msg := p.Msg.(*nodestypes.MsgRegisterNode)
		if err := msg.ValidateBasic(); err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
		if err := nodestypes.VerifyBinding(chainID, msg.Operator, msg.Bindings[0]); err != nil {
			t.Fatalf("k=%d: the chain would refuse the binding: %v", k, err)
		}
		if err := nodestypes.CheckHotKeyBinding(msg.HotKey, msg.Bindings); err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
		if ids[msg.NodeId] || hots[msg.HotKey] || pubs[string(msg.Bindings[0].Pubkey)] {
			t.Fatalf("k=%d repeats a node id, hot key or public key", k)
		}
		ids[msg.NodeId], hots[msg.HotKey], pubs[string(msg.Bindings[0].Pubkey)] = true, true, true
	}
	// A binding signed for another chain must not verify: the signature commits to the chain id.
	p, _ := g.Plan(0)
	msg := p.Msg.(*nodestypes.MsgRegisterNode)
	if nodestypes.VerifyBinding("orama-localnet-2", msg.Operator, msg.Bindings[0]) == nil {
		t.Fatal("a binding verified for a chain it was not signed for")
	}
}

func TestCnftMint_namesTheStepsRootAndMintsDistinctAssets(t *testing.T) {
	a := addrs(t, 2)
	root := bytes.Repeat([]byte{7}, 32)
	g := &cnftMint{runID: "run1", accounts: a, trees: []treeRef{{Tree: 5}, {Tree: 6}}, anchors: [][]byte{root, root}}
	seen := map[string]bool{}
	for k := 0; k < 10; k++ {
		p, err := g.Plan(k)
		if err != nil {
			t.Fatal(err)
		}
		msg := p.Msg.(*cnfttypes.MsgMint)
		if err := msg.ValidateBasic(); err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
		if msg.TreeId != uint64(5+k%2) || !bytes.Equal(msg.Root, root) || msg.Creator != a[k%2] {
			t.Fatalf("k=%d: %+v", k, msg)
		}
		id := string(msg.Leaves[0].AssetId)
		if seen[id] || len(id) != cnfttypes.HashSize {
			t.Fatalf("k=%d: asset id repeated or the wrong size", k)
		}
		seen[id] = true
	}
}

func TestMintLeaves_splitsIntoBatchesOfAtMostTheChainsLimit(t *testing.T) {
	accts, err := account.DeriveN("gen-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	leaves := make([]cnfttypes.Leaf, 2*cnfttypes.MaxMintBatch+3)
	for i := range leaves {
		leaves[i] = cnfttypes.Leaf{AssetId: assetID("r", 0, i), Owner: accts[0].Address, MetadataCid: metadataCID}
	}
	msgs := mintLeaves(accts[0], 9, []byte{1}, leaves)
	if len(msgs) != 3 {
		t.Fatalf("%d messages", len(msgs))
	}
	total := 0
	for _, m := range msgs {
		n := len(m.(*cnfttypes.MsgMint).Leaves)
		if n > cnfttypes.MaxMintBatch {
			t.Fatalf("a message mints %d leaves, the limit is %d", n, cnfttypes.MaxMintBatch)
		}
		total += n
	}
	if total != len(leaves) {
		t.Fatalf("%d leaves minted of %d", total, len(leaves))
	}
	if len(mintLeaves(accts[0], 9, nil, nil)) != 0 {
		t.Fatal("no leaves must mean no messages")
	}
}

// The proofs a listing carries must be accepted by the chain's own concurrent Merkle tree, which
// is the code MsgList runs them through.
func TestMarketProofs_areAcceptedByTheChainsTree(t *testing.T) {
	const depth, n = 4, 11
	accts := addrs(t, 1)
	creator := sdk.MustAccAddressFromBech32(accts[0])
	leaves := make([]cnfttypes.Leaf, n)
	hashes := make([][]byte, n)
	for i := range leaves {
		leaves[i] = cnfttypes.Leaf{AssetId: assetID("r", 0, i), Owner: accts[0], MetadataCid: metadataCID, CreatorHash: cnfttypes.CreatorHash(creator), HashId: cnfttypes.HashIDSHA256}
		h, err := cnfttypes.HashLeaf(leaves[i])
		if err != nil {
			t.Fatal(err)
		}
		hashes[i] = h
	}
	tree, err := cnfttypes.NewTree(1, 1, accts[0], depth, 8, 0)
	if err != nil {
		t.Fatal(err)
	}
	anchor := tree.Root()
	if _, err := tree.AppendBatch(anchor, hashes); err != nil {
		t.Fatal(err)
	}
	siblings, root, err := proofSet(hashes, depth)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(root, tree.Root()) {
		t.Fatalf("computed root %x, the tree's is %x", root, tree.Root())
	}
	for i := range leaves {
		if err := tree.Prove(root, hashes[i], uint32(i), siblings[i]); err != nil {
			t.Fatalf("leaf %d: the chain's tree refuses the proof: %v", i, err)
		}
	}
	// A proof for another leaf must not pass.
	if err := tree.Prove(root, hashes[0], 1, siblings[1]); err == nil {
		t.Fatal("a proof was accepted for a leaf that is not at that index")
	}
}

func TestMarketList_planIsBoundedByWhatWasMinted(t *testing.T) {
	a := addrs(t, 2)
	leaf := cnfttypes.Leaf{AssetId: bytes.Repeat([]byte{1}, 32), Owner: a[0], MetadataCid: metadataCID, CreatorHash: bytes.Repeat([]byte{2}, 32), HashId: cnfttypes.HashIDSHA256}
	m := &marketList{
		accounts: a, trees: []treeRef{{Tree: 1}, {Tree: 2}},
		leaves:   [][]cnfttypes.Leaf{{leaf, leaf}, {leaf, leaf}},
		siblings: [][][][]byte{{nil, nil}, {nil, nil}},
		roots:    [][]byte{bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32)},
	}
	if m.Available() != 4 {
		t.Fatalf("available %d", m.Available())
	}
	p, err := m.Plan(3)
	if err != nil {
		t.Fatal(err)
	}
	msg := p.Msg.(*markettypes.MsgList)
	if msg.TreeId != 2 || msg.Proof.Index != 1 || msg.Seller != a[1] {
		t.Fatalf("message %+v", msg)
	}
	if _, err := m.Plan(4); err == nil {
		t.Fatal("a fifth listing was planned from four minted assets")
	}
}
