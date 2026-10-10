package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

type msgServer struct {
	Keeper
}

// NewMsgServer returns x/cnft's Msg implementation.
func NewMsgServer(k Keeper) types.MsgServer {
	return msgServer{Keeper: k}
}

func (m msgServer) CreateCollection(ctx context.Context, msg *types.MsgCreateCollection) (*types.MsgCreateCollectionResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("create collection: %w", err)
	}
	creator, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, fmt.Errorf("create collection: %w", err)
	}
	id, err := m.createCollection(ctx, creator, msg.Name, msg.RoyaltyBps)
	if err != nil {
		return nil, fmt.Errorf("create collection: %w", err)
	}
	return &types.MsgCreateCollectionResponse{Id: id}, nil
}

func (m msgServer) CreateTree(ctx context.Context, msg *types.MsgCreateTree) (*types.MsgCreateTreeResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("create tree: %w", err)
	}
	creator, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, fmt.Errorf("create tree: %w", err)
	}
	id, root, err := m.createTree(ctx, creator, msg.CollectionId, msg.Depth, msg.Buffer, msg.Canopy)
	if err != nil {
		return nil, fmt.Errorf("create tree: %w", err)
	}
	return &types.MsgCreateTreeResponse{Id: id, Root: root}, nil
}

func (m msgServer) Mint(ctx context.Context, msg *types.MsgMint) (*types.MsgMintResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("mint: %w", err)
	}
	creator, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, fmt.Errorf("mint: %w", err)
	}
	indices, root, err := m.mint(ctx, creator, msg.TreeId, msg.Root, msg.Leaves)
	if err != nil {
		return nil, fmt.Errorf("mint: %w", err)
	}
	return &types.MsgMintResponse{LeafIndices: indices, Root: root}, nil
}

func (m msgServer) Transfer(ctx context.Context, msg *types.MsgTransfer) (*types.MsgTransferResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("transfer: %w", err)
	}
	signer, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, fmt.Errorf("transfer: %w", err)
	}
	root, nonce, err := m.transfer(ctx, signer, msg.TreeId, msg.Current, msg.NewOwner, msg.NewDelegate, msg.Proof)
	if err != nil {
		return nil, fmt.Errorf("transfer: %w", err)
	}
	return &types.MsgTransferResponse{Root: root, Nonce: nonce}, nil
}

func (m msgServer) Burn(ctx context.Context, msg *types.MsgBurn) (*types.MsgBurnResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("burn: %w", err)
	}
	signer, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, fmt.Errorf("burn: %w", err)
	}
	root, err := m.burn(ctx, signer, msg.TreeId, msg.Current, msg.Proof)
	if err != nil {
		return nil, fmt.Errorf("burn: %w", err)
	}
	return &types.MsgBurnResponse{Root: root}, nil
}

func (m msgServer) UpdateMetadata(ctx context.Context, msg *types.MsgUpdateMetadata) (*types.MsgUpdateMetadataResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("update metadata: %w", err)
	}
	signer, err := sdk.AccAddressFromBech32(msg.Signer)
	if err != nil {
		return nil, fmt.Errorf("update metadata: %w", err)
	}
	root, nonce, err := m.updateMetadata(ctx, signer, msg.TreeId, msg.Current, msg.NewMetadataCid, msg.Proof)
	if err != nil {
		return nil, fmt.Errorf("update metadata: %w", err)
	}
	return &types.MsgUpdateMetadataResponse{Root: root, Nonce: nonce}, nil
}

func (m msgServer) Decompress(ctx context.Context, msg *types.MsgDecompress) (*types.MsgDecompressResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	owner, err := sdk.AccAddressFromBech32(msg.Owner)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	if err := m.decompress(ctx, owner, msg.TreeId, msg.Current, msg.Proof); err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	return &types.MsgDecompressResponse{}, nil
}

func (m msgServer) Compress(ctx context.Context, msg *types.MsgCompress) (*types.MsgCompressResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("compress: %w", err)
	}
	owner, err := sdk.AccAddressFromBech32(msg.Owner)
	if err != nil {
		return nil, fmt.Errorf("compress: %w", err)
	}
	treeID, index, root, nonce, err := m.compress(ctx, owner, msg.AssetId, msg.Root)
	if err != nil {
		return nil, fmt.Errorf("compress: %w", err)
	}
	return &types.MsgCompressResponse{TreeId: treeID, LeafIndex: index, Root: root, Nonce: nonce}, nil
}

func (m msgServer) RecordSnapshot(ctx context.Context, msg *types.MsgRecordSnapshot) (*types.MsgRecordSnapshotResponse, error) {
	if err := msg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("record snapshot: %w", err)
	}
	creator, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, fmt.Errorf("record snapshot: %w", err)
	}
	id, err := m.recordSnapshot(ctx, creator, msg.TreeId, msg.Cid)
	if err != nil {
		return nil, fmt.Errorf("record snapshot: %w", err)
	}
	return &types.MsgRecordSnapshotResponse{Id: id}, nil
}

func (k Keeper) createCollection(ctx context.Context, creator sdk.AccAddress, name string, royalty uint32) (uint64, error) {
	id, err := k.takeID(ctx, k.NextCollectionID, "collection")
	if err != nil {
		return 0, err
	}
	col := types.Collection{Id: id, Creator: creator.String(), RoyaltyBps: royalty, Name: name}
	if err := k.Collections.Set(ctx, id, col); err != nil {
		return 0, fmt.Errorf("failed to store collection %d: %w", id, err)
	}
	return id, nil
}

func (k Keeper) createTree(ctx context.Context, creator sdk.AccAddress, collectionID uint64, depth, buffer, canopy uint32) (uint64, []byte, error) {
	col, err := k.loadCollection(ctx, collectionID)
	if err != nil {
		return 0, nil, err
	}
	same, err := types.SameAccount(creator.String(), col.Creator)
	if err != nil {
		return 0, nil, err
	}
	if !same {
		return 0, nil, fmt.Errorf("only the collection creator can create a tree")
	}
	amount, err := types.TreeDeposit(depth, buffer, canopy)
	if err != nil {
		return 0, nil, err
	}
	id, err := k.takeID(ctx, k.NextTreeID, "tree")
	if err != nil {
		return 0, nil, err
	}
	depositID := types.TreeDepositID(id)
	if err := k.fees.LockDeposit(ctx, creator, depositID, amount); err != nil {
		return 0, nil, fmt.Errorf("failed to lock tree deposit: %w", err)
	}
	tree, err := types.NewTree(id, collectionID, creator.String(), depth, buffer, canopy)
	if err != nil {
		return 0, nil, err
	}
	tree.DepositId = depositID
	if err := k.storeTree(ctx, *tree); err != nil {
		return 0, nil, err
	}
	return id, tree.Root(), nil
}

func (k Keeper) mint(ctx context.Context, creator sdk.AccAddress, treeID uint64, anchor []byte, leaves []types.MintLeaf) ([]uint32, []byte, error) {
	tree, err := k.loadTree(ctx, treeID)
	if err != nil {
		return nil, nil, err
	}
	same, err := types.SameAccount(creator.String(), tree.Creator)
	if err != nil {
		return nil, nil, err
	}
	if !same {
		return nil, nil, fmt.Errorf("only the tree creator can mint")
	}
	col, err := k.loadCollection(ctx, tree.CollectionId)
	if err != nil {
		return nil, nil, err
	}
	colCreator, err := sdk.AccAddressFromBech32(col.Creator)
	if err != nil {
		return nil, nil, fmt.Errorf("collection creator: %w", err)
	}
	creatorHash := types.CreatorHash(colCreator)
	hashes := make([][]byte, 0, len(leaves))
	for i, in := range leaves {
		taken, err := k.Decompressed.Has(ctx, in.AssetId)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to check asset: %w", err)
		}
		if taken {
			return nil, nil, fmt.Errorf("asset %x is already decompressed", in.AssetId)
		}
		leaf := types.Leaf{
			AssetId:     append([]byte(nil), in.AssetId...),
			Owner:       in.Owner,
			Delegate:    in.Delegate,
			MetadataCid: in.MetadataCid,
			CreatorHash: creatorHash,
			Nonce:       0,
			HashId:      types.HashIDSHA256,
		}
		hash, err := types.HashLeaf(leaf)
		if err != nil {
			return nil, nil, fmt.Errorf("leaf %d: %w", i, err)
		}
		hashes = append(hashes, hash)
	}
	indices, err := tree.AppendBatch(anchor, hashes)
	if err != nil {
		return nil, nil, err
	}
	if err := k.storeTree(ctx, tree); err != nil {
		return nil, nil, err
	}
	return indices, tree.Root(), nil
}

// transfer moves a compressed leaf. It does not credit earnings: a transfer that
// is not a market sale pays no royalty.
func (k Keeper) transfer(ctx context.Context, signer sdk.AccAddress, treeID uint64, current types.Leaf, newOwner, newDelegate string, proof types.MerkleProof) ([]byte, uint64, error) {
	if err := signerCanWrite(signer.String(), current.Owner, current.Delegate); err != nil {
		return nil, 0, err
	}
	next, err := advance(current, newOwner, newDelegate, current.MetadataCid)
	if err != nil {
		return nil, 0, err
	}
	nextHash, err := types.HashLeaf(next)
	if err != nil {
		return nil, 0, err
	}
	root, err := k.commit(ctx, treeID, current, proof, nextHash)
	if err != nil {
		return nil, 0, err
	}
	return root, next.Nonce, nil
}

func (k Keeper) burn(ctx context.Context, signer sdk.AccAddress, treeID uint64, current types.Leaf, proof types.MerkleProof) ([]byte, error) {
	if err := signerCanWrite(signer.String(), current.Owner, current.Delegate); err != nil {
		return nil, err
	}
	return k.commit(ctx, treeID, current, proof, types.ZeroLeaf())
}

func (k Keeper) updateMetadata(ctx context.Context, signer sdk.AccAddress, treeID uint64, current types.Leaf, metadata string, proof types.MerkleProof) ([]byte, uint64, error) {
	if err := signerCanWrite(signer.String(), current.Owner, current.Delegate); err != nil {
		return nil, 0, err
	}
	next, err := advance(current, current.Owner, current.Delegate, metadata)
	if err != nil {
		return nil, 0, err
	}
	nextHash, err := types.HashLeaf(next)
	if err != nil {
		return nil, 0, err
	}
	root, err := k.commit(ctx, treeID, current, proof, nextHash)
	if err != nil {
		return nil, 0, err
	}
	return root, next.Nonce, nil
}

func (k Keeper) decompress(ctx context.Context, owner sdk.AccAddress, treeID uint64, current types.Leaf, proof types.MerkleProof) error {
	taken, err := k.Decompressed.Has(ctx, current.AssetId)
	if err != nil {
		return fmt.Errorf("failed to check asset: %w", err)
	}
	if taken {
		return fmt.Errorf("asset %x is already decompressed", current.AssetId)
	}
	tree, err := k.loadTree(ctx, treeID)
	if err != nil {
		return err
	}
	if _, err := k.commit(ctx, treeID, current, proof, types.ZeroLeaf()); err != nil {
		return err
	}
	asset := types.DecompressedAsset{
		AssetId:      append([]byte(nil), current.AssetId...),
		TreeId:       treeID,
		LeafIndex:    proof.Index,
		Owner:        owner.String(),
		Delegate:     current.Delegate,
		MetadataCid:  current.MetadataCid,
		CreatorHash:  append([]byte(nil), current.CreatorHash...),
		Nonce:        current.Nonce,
		HashId:       current.HashId,
		CollectionId: tree.CollectionId,
	}
	if err := k.Decompressed.Set(ctx, asset.AssetId, asset); err != nil {
		return fmt.Errorf("failed to store decompressed asset: %w", err)
	}
	return nil
}

func (k Keeper) compress(ctx context.Context, owner sdk.AccAddress, assetID, anchor []byte) (uint64, uint32, []byte, uint64, error) {
	asset, err := k.Decompressed.Get(ctx, assetID)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, 0, nil, 0, fmt.Errorf("asset %x is not decompressed", assetID)
		}
		return 0, 0, nil, 0, fmt.Errorf("failed to load decompressed asset: %w", err)
	}
	same, err := types.SameAccount(owner.String(), asset.Owner)
	if err != nil {
		return 0, 0, nil, 0, err
	}
	if !same {
		return 0, 0, nil, 0, fmt.Errorf("signer is not the decompressed owner")
	}
	if asset.Nonce == ^uint64(0) {
		return 0, 0, nil, 0, fmt.Errorf("nonce overflow")
	}
	next := types.Leaf{
		AssetId:     append([]byte(nil), asset.AssetId...),
		Owner:       owner.String(),
		Delegate:    asset.Delegate,
		MetadataCid: asset.MetadataCid,
		CreatorHash: append([]byte(nil), asset.CreatorHash...),
		Nonce:       asset.Nonce + 1,
		HashId:      asset.HashId,
	}
	hash, err := types.HashLeaf(next)
	if err != nil {
		return 0, 0, nil, 0, err
	}
	tree, err := k.loadTree(ctx, asset.TreeId)
	if err != nil {
		return 0, 0, nil, 0, err
	}
	indices, err := tree.AppendBatch(anchor, [][]byte{hash})
	if err != nil {
		return 0, 0, nil, 0, err
	}
	if err := k.storeTree(ctx, tree); err != nil {
		return 0, 0, nil, 0, err
	}
	if err := k.Decompressed.Remove(ctx, assetID); err != nil {
		return 0, 0, nil, 0, fmt.Errorf("failed to remove decompressed asset: %w", err)
	}
	return asset.TreeId, indices[0], tree.Root(), next.Nonce, nil
}

func (k Keeper) recordSnapshot(ctx context.Context, creator sdk.AccAddress, treeID uint64, cid string) (uint64, error) {
	tree, err := k.loadTree(ctx, treeID)
	if err != nil {
		return 0, err
	}
	same, err := types.SameAccount(creator.String(), tree.Creator)
	if err != nil {
		return 0, err
	}
	if !same {
		return 0, fmt.Errorf("only the tree creator can record a snapshot")
	}
	id, err := k.nextSnapshotID(ctx, treeID)
	if err != nil {
		return 0, err
	}
	snap := types.Snapshot{TreeId: treeID, Id: id, Cid: cid, Sequence: tree.Sequence}
	if err := k.Snapshots.Set(ctx, collections.Join(treeID, id), snap); err != nil {
		return 0, fmt.Errorf("failed to store snapshot: %w", err)
	}
	return id, nil
}

func (k Keeper) nextSnapshotID(ctx context.Context, treeID uint64) (uint64, error) {
	id, err := k.NextSnapshot.Get(ctx, treeID)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return 0, fmt.Errorf("failed to load next snapshot id: %w", err)
		}
		id = 1
	}
	if id == 0 {
		id = 1
	}
	if err := k.NextSnapshot.Set(ctx, treeID, id+1); err != nil {
		return 0, fmt.Errorf("failed to store next snapshot id: %w", err)
	}
	return id, nil
}

// CollectionRoyalty is the creator and basis-point rate of a collection.
func (k Keeper) CollectionRoyalty(ctx context.Context, collectionID uint64) (sdk.AccAddress, uint32, error) {
	col, err := k.loadCollection(ctx, collectionID)
	if err != nil {
		return nil, 0, err
	}
	creator, err := sdk.AccAddressFromBech32(col.Creator)
	if err != nil {
		return nil, 0, fmt.Errorf("collection %d creator: %w", collectionID, err)
	}
	return creator, col.RoyaltyBps, nil
}

// ProveOwned checks that owner currently holds leaf in the tree. The tree is not modified.
func (k Keeper) ProveOwned(ctx context.Context, treeID uint64, leaf types.Leaf, proof types.MerkleProof, owner sdk.AccAddress) (uint64, error) {
	if err := leaf.Validate(); err != nil {
		return 0, err
	}
	if err := proof.ValidateBasic(); err != nil {
		return 0, err
	}
	same, err := types.SameAccount(leaf.Owner, owner.String())
	if err != nil {
		return 0, err
	}
	if !same {
		return 0, fmt.Errorf("leaf owner is not %s", owner)
	}
	return k.prove(ctx, treeID, leaf, proof)
}

// TransferForSale moves a compressed leaf from seller to buyer. The caller must
// already have taken payment. This path does not credit earnings.
func (k Keeper) TransferForSale(ctx context.Context, treeID uint64, leaf types.Leaf, proof types.MerkleProof, seller, buyer sdk.AccAddress) error {
	if _, err := k.ProveOwned(ctx, treeID, leaf, proof, seller); err != nil {
		return err
	}
	if seller.Equals(buyer) {
		return fmt.Errorf("seller and buyer are the same account")
	}
	next, err := advance(leaf, buyer.String(), "", leaf.MetadataCid)
	if err != nil {
		return err
	}
	nextHash, err := types.HashLeaf(next)
	if err != nil {
		return err
	}
	_, err = k.commit(ctx, treeID, leaf, proof, nextHash)
	return err
}

func (k Keeper) prove(ctx context.Context, treeID uint64, leaf types.Leaf, proof types.MerkleProof) (uint64, error) {
	tree, err := k.loadTree(ctx, treeID)
	if err != nil {
		return 0, err
	}
	hash, err := types.HashLeaf(leaf)
	if err != nil {
		return 0, err
	}
	if zeroHash(hash) {
		return 0, fmt.Errorf("leaf is empty")
	}
	if err := tree.Prove(proof.Root, hash, proof.Index, proof.Siblings); err != nil {
		return 0, err
	}
	return tree.CollectionId, nil
}

func (k Keeper) commit(ctx context.Context, treeID uint64, current types.Leaf, proof types.MerkleProof, nextHash []byte) ([]byte, error) {
	tree, err := k.loadTree(ctx, treeID)
	if err != nil {
		return nil, err
	}
	currentHash, err := types.HashLeaf(current)
	if err != nil {
		return nil, err
	}
	if zeroHash(currentHash) {
		return nil, fmt.Errorf("leaf is empty")
	}
	if err := tree.SetLeaf(proof.Root, currentHash, nextHash, proof.Index, proof.Siblings); err != nil {
		return nil, err
	}
	if err := k.storeTree(ctx, tree); err != nil {
		return nil, err
	}
	return tree.Root(), nil
}

func signerCanWrite(signer, owner, delegate string) error {
	ok, err := types.SameAccount(signer, owner)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if delegate != "" {
		ok, err = types.SameAccount(signer, delegate)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("signer is not the owner or delegate")
}

func advance(current types.Leaf, owner, delegate, metadata string) (types.Leaf, error) {
	if current.Nonce == ^uint64(0) {
		return types.Leaf{}, fmt.Errorf("nonce overflow")
	}
	out := current
	out.AssetId = append([]byte(nil), current.AssetId...)
	out.CreatorHash = append([]byte(nil), current.CreatorHash...)
	out.Owner = owner
	out.Delegate = delegate
	out.MetadataCid = metadata
	out.Nonce++
	return out, nil
}

func zeroHash(node []byte) bool {
	return len(node) == types.HashSize && bytes.Equal(node, make([]byte, types.HashSize))
}
