package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	gogoproto "github.com/cosmos/gogoproto/proto"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"

	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	markettypes "github.com/DeBrosOfficial/network/chain/x/market/types"
)

const queryTree = "/orama.cnft.v1.Query/Tree"

// applier folds one successful transaction's x/cnft and x/market messages
// into the asset index. Other messages leave it unchanged.
type applier struct {
	ctx    context.Context
	chain  Chain
	w      *writer
	height int64
	tx     string
}

// handler decodes a message and its response into fresh values and applies them.
type handler struct {
	msg, resp func() gogoproto.Message
	apply     func(a applier, msg, resp gogoproto.Message) error
}

func typeURL(m gogoproto.Message) string { return "/" + gogoproto.MessageName(m) }

var handlers = map[string]handler{
	typeURL(&cnfttypes.MsgCreateTree{}):      {newMsg[cnfttypes.MsgCreateTree], newMsg[cnfttypes.MsgCreateTreeResponse], applyCreateTree},
	typeURL(&cnfttypes.MsgMint{}):            {newMsg[cnfttypes.MsgMint], newMsg[cnfttypes.MsgMintResponse], applyMint},
	typeURL(&cnfttypes.MsgTransfer{}):        {newMsg[cnfttypes.MsgTransfer], newMsg[cnfttypes.MsgTransferResponse], applyTransfer},
	typeURL(&cnfttypes.MsgBurn{}):            {newMsg[cnfttypes.MsgBurn], newMsg[cnfttypes.MsgBurnResponse], applyBurn},
	typeURL(&cnfttypes.MsgUpdateMetadata{}):  {newMsg[cnfttypes.MsgUpdateMetadata], newMsg[cnfttypes.MsgUpdateMetadataResponse], applyUpdateMetadata},
	typeURL(&cnfttypes.MsgDecompress{}):      {newMsg[cnfttypes.MsgDecompress], newMsg[cnfttypes.MsgDecompressResponse], applyDecompress},
	typeURL(&cnfttypes.MsgCompress{}):        {newMsg[cnfttypes.MsgCompress], newMsg[cnfttypes.MsgCompressResponse], applyCompress},
	typeURL(&markettypes.MsgList{}):          {newMsg[markettypes.MsgList], newMsg[markettypes.MsgListResponse], applyList},
	typeURL(&markettypes.MsgBid{}):           {newMsg[markettypes.MsgBid], newMsg[markettypes.MsgBidResponse], applyBid},
	typeURL(&markettypes.MsgCancelListing{}): {newMsg[markettypes.MsgCancelListing], newMsg[markettypes.MsgCancelListingResponse], applyCancelListing},
	typeURL(&markettypes.MsgCancelBid{}):     {newMsg[markettypes.MsgCancelBid], newMsg[markettypes.MsgCancelBidResponse], applyCancelBid},
	typeURL(&markettypes.MsgSettle{}):        {newMsg[markettypes.MsgSettle], newMsg[markettypes.MsgSettleResponse], applySettle},
}

func newMsg[T any, P interface {
	*T
	gogoproto.Message
}]() gogoproto.Message {
	return P(new(T))
}

func (a applier) apply(msg, resp *codectypes.Any) error {
	h, ok := handlers[msg.TypeUrl]
	if !ok {
		return nil
	}
	m, r := h.msg(), h.resp()
	if err := gogoproto.Unmarshal(msg.Value, m); err != nil {
		return fmt.Errorf("decode message: %w", err)
	}
	if resp.TypeUrl != typeURL(r) {
		return fmt.Errorf("response is %s, want %s", resp.TypeUrl, typeURL(r))
	}
	if err := gogoproto.Unmarshal(resp.Value, r); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return h.apply(a, m, r)
}

func applyCreateTree(a applier, msg, resp gogoproto.Message) error {
	m, r := msg.(*cnfttypes.MsgCreateTree), resp.(*cnfttypes.MsgCreateTreeResponse)
	return a.w.putTree(r.Id, m.CollectionId)
}

// applyMint writes each new leaf. x/cnft fills creator_hash with SHA-256 of
// the collection creator's account bytes, and only that creator can mint, so
// it is the hash of the mint signer.
func applyMint(a applier, msg, resp gogoproto.Message) error {
	m, r := msg.(*cnfttypes.MsgMint), resp.(*cnfttypes.MsgMintResponse)
	if len(r.LeafIndices) != len(m.Leaves) {
		return fmt.Errorf("mint of %d leaves answered %d leaf indices", len(m.Leaves), len(r.LeafIndices))
	}
	_, creator, err := canonicalAccount(m.Creator)
	if err != nil {
		return fmt.Errorf("mint creator: %w", err)
	}
	creatorHash := sha256.Sum256(creator)
	for i, in := range m.Leaves {
		leaf := cnfttypes.Leaf{
			AssetId: in.AssetId, Owner: in.Owner, Delegate: in.Delegate, MetadataCid: in.MetadataCid,
			CreatorHash: creatorHash[:], Nonce: 0, HashId: cnfttypes.HashIDSHA256,
		}
		if err := a.putLeaf(leaf, m.TreeId, r.LeafIndices[i], AssetCompressed); err != nil {
			return err
		}
	}
	return nil
}

func applyTransfer(a applier, msg, resp gogoproto.Message) error {
	m, r := msg.(*cnfttypes.MsgTransfer), resp.(*cnfttypes.MsgTransferResponse)
	next := m.Current
	next.Owner, next.Delegate, next.Nonce = m.NewOwner, m.NewDelegate, r.Nonce
	return a.putLeaf(next, m.TreeId, m.Proof.Index, AssetCompressed)
}

func applyBurn(a applier, msg, _ gogoproto.Message) error {
	m := msg.(*cnfttypes.MsgBurn)
	return a.putLeaf(m.Current, m.TreeId, m.Proof.Index, AssetBurned)
}

func applyUpdateMetadata(a applier, msg, resp gogoproto.Message) error {
	m, r := msg.(*cnfttypes.MsgUpdateMetadata), resp.(*cnfttypes.MsgUpdateMetadataResponse)
	next := m.Current
	next.MetadataCid, next.Nonce = m.NewMetadataCid, r.Nonce
	return a.putLeaf(next, m.TreeId, m.Proof.Index, AssetCompressed)
}

// applyDecompress keeps the record at its old location, as x/cnft's
// DecompressedAsset does, with the leaf cleared on chain.
func applyDecompress(a applier, msg, _ gogoproto.Message) error {
	m := msg.(*cnfttypes.MsgDecompress)
	next := m.Current
	next.Owner = m.Owner
	return a.putLeaf(next, m.TreeId, m.Proof.Index, AssetDecompressed)
}

// applyCompress moves a decompressed asset to the new leaf the chain appended.
func applyCompress(a applier, msg, resp gogoproto.Message) error {
	m, r := msg.(*cnfttypes.MsgCompress), resp.(*cnfttypes.MsgCompressResponse)
	prev, err := a.decompressedAsset(m.AssetId)
	if err != nil {
		return err
	}
	next := cnfttypes.Leaf{
		AssetId: m.AssetId, Owner: m.Owner, Delegate: prev.Delegate, MetadataCid: prev.MetadataCid,
		CreatorHash: prev.CreatorHash, Nonce: r.Nonce, HashId: prev.HashId,
	}
	return a.putLeaf(next, r.TreeId, r.LeafIndex, AssetCompressed)
}

// putLeaf writes the asset at (tree, index) in the given state.
func (a applier) putLeaf(leaf cnfttypes.Leaf, tree uint64, index uint32, state string) error {
	leaf, err := canonicalLeaf(leaf)
	if err != nil {
		return err
	}
	collection, err := a.collection(tree)
	if err != nil {
		return err
	}
	asset := Asset{
		ID: hex.EncodeToString(leaf.AssetId), State: state, TreeID: tree, LeafIndex: index,
		CollectionID: collection, Owner: leaf.Owner, Delegate: leaf.Delegate, MetadataCID: leaf.MetadataCid,
		CreatorHash: hex.EncodeToString(leaf.CreatorHash), Nonce: leaf.Nonce, HashID: leaf.HashId,
		UpdatedHeight: a.height, UpdatedTx: a.tx,
	}
	if state == AssetCompressed {
		hash, err := leafHash(leaf)
		if err != nil {
			return fmt.Errorf("asset %s: %w", asset.ID, err)
		}
		asset.LeafHash = hex.EncodeToString(hash)
	}
	return a.w.putAsset(location(leaf.AssetId, tree, index), asset)
}

// canonicalLeaf rewrites owner and delegate to their lowercase encoding. The
// chain accepts either case and hashes the address bytes, so both spellings
// are the same leaf.
func canonicalLeaf(leaf cnfttypes.Leaf) (cnfttypes.Leaf, error) {
	owner, _, err := canonicalAccount(leaf.Owner)
	if err != nil {
		return leaf, fmt.Errorf("leaf owner: %w", err)
	}
	leaf.Owner = owner
	if leaf.Delegate != "" {
		delegate, _, err := canonicalAccount(leaf.Delegate)
		if err != nil {
			return leaf, fmt.Errorf("leaf delegate: %w", err)
		}
		leaf.Delegate = delegate
	}
	return leaf, nil
}

// leafHash is cnfttypes.LeafHash over the account bytes of owner and
// delegate. It decodes them itself so it does not depend on the SDK's global
// bech32 prefix.
func leafHash(leaf cnfttypes.Leaf) ([]byte, error) {
	_, owner, err := canonicalAccount(leaf.Owner)
	if err != nil {
		return nil, fmt.Errorf("leaf owner: %w", err)
	}
	var delegate []byte
	if leaf.Delegate != "" {
		if _, delegate, err = canonicalAccount(leaf.Delegate); err != nil {
			return nil, fmt.Errorf("leaf delegate: %w", err)
		}
	}
	return cnfttypes.LeafHash(leaf.AssetId, owner, delegate, []byte(leaf.MetadataCid), leaf.CreatorHash, leaf.Nonce, leaf.HashId), nil
}

// collection returns the collection of a tree. A tree created before the
// index's start height is read from the chain once; x/cnft never changes a
// tree's collection or deletes a tree, so the latest state answers for any
// height.
func (a applier) collection(tree uint64) (uint64, error) {
	id, ok, err := a.w.treeCollection(tree)
	if err != nil || ok {
		return id, err
	}
	var resp cnfttypes.QueryTreeResponse
	if err := a.chain.QueryAt(a.ctx, 0, queryTree, &cnfttypes.QueryTreeRequest{Id: tree}, &resp); err != nil {
		return 0, fmt.Errorf("read tree %d: %w", tree, err)
	}
	if err := a.w.putTree(tree, resp.Tree.CollectionId); err != nil {
		return 0, err
	}
	return resp.Tree.CollectionId, nil
}
