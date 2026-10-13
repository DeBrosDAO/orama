package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DefaultGenesisState returns an empty x/cnft genesis. Collection and tree ids start at 1.
func DefaultGenesisState() *GenesisState {
	return &GenesisState{
		Collections:      []Collection{},
		Trees:            []Tree{},
		Decompressed:     []DecompressedAsset{},
		Snapshots:        []Snapshot{},
		NextCollectionId: 1,
		NextTreeId:       1,
	}
}

// Validate checks genesis state for internal consistency.
func (gs GenesisState) Validate() error {
	if gs.NextCollectionId == 0 || gs.NextTreeId == 0 {
		return fmt.Errorf("next collection and tree ids must be positive")
	}
	collections := make(map[uint64]Collection, len(gs.Collections))
	var maxCollection uint64
	for _, c := range gs.Collections {
		if c.Id == 0 {
			return fmt.Errorf("collection id is required")
		}
		if _, ok := collections[c.Id]; ok {
			return fmt.Errorf("duplicate collection id %d", c.Id)
		}
		if err := validateBech32("creator", c.Creator); err != nil {
			return fmt.Errorf("collection %d: %w", c.Id, err)
		}
		if c.Name == "" || len(c.Name) > MaxNameLen {
			return fmt.Errorf("collection %d name must be 1..%d bytes", c.Id, MaxNameLen)
		}
		if c.RoyaltyBps > RoyaltyBasisPoints {
			return fmt.Errorf("collection %d royalty_bps %d exceeds %d", c.Id, c.RoyaltyBps, RoyaltyBasisPoints)
		}
		collections[c.Id] = c
		if c.Id > maxCollection {
			maxCollection = c.Id
		}
	}
	if maxCollection >= gs.NextCollectionId {
		return fmt.Errorf("next_collection_id %d is not greater than existing id %d", gs.NextCollectionId, maxCollection)
	}

	trees := make(map[uint64]struct{}, len(gs.Trees))
	var maxTree uint64
	for _, tree := range gs.Trees {
		if err := tree.ValidateShape(collections); err != nil {
			return err
		}
		if _, ok := trees[tree.Id]; ok {
			return fmt.Errorf("duplicate tree id %d", tree.Id)
		}
		trees[tree.Id] = struct{}{}
		if tree.Id > maxTree {
			maxTree = tree.Id
		}
	}
	if maxTree >= gs.NextTreeId {
		return fmt.Errorf("next_tree_id %d is not greater than existing id %d", gs.NextTreeId, maxTree)
	}

	assets := make(map[string]struct{}, len(gs.Decompressed))
	for _, asset := range gs.Decompressed {
		if len(asset.AssetId) != HashSize {
			return fmt.Errorf("decompressed asset_id must be %d bytes", HashSize)
		}
		key := string(asset.AssetId)
		if _, ok := assets[key]; ok {
			return fmt.Errorf("duplicate decompressed asset")
		}
		assets[key] = struct{}{}
		if _, ok := trees[asset.TreeId]; !ok {
			return fmt.Errorf("decompressed asset references unknown tree %d", asset.TreeId)
		}
		if err := validateBech32("owner", asset.Owner); err != nil {
			return fmt.Errorf("decompressed asset: %w", err)
		}
		if err := validateDelegate(asset.Delegate); err != nil {
			return err
		}
		if asset.HashId != HashIDSHA256 || len(asset.CreatorHash) != HashSize {
			return fmt.Errorf("decompressed asset has a bad creator hash or hash id")
		}
	}

	type snapKey struct {
		tree uint64
		id   uint64
	}
	seenSnaps := make(map[snapKey]struct{}, len(gs.Snapshots))
	for _, snap := range gs.Snapshots {
		if _, ok := trees[snap.TreeId]; !ok {
			return fmt.Errorf("snapshot references unknown tree %d", snap.TreeId)
		}
		if snap.Id == 0 {
			return fmt.Errorf("snapshot id is required")
		}
		if err := validateCID("cid", snap.Cid); err != nil {
			return fmt.Errorf("snapshot %d: %w", snap.Id, err)
		}
		key := snapKey{snap.TreeId, snap.Id}
		if _, ok := seenSnaps[key]; ok {
			return fmt.Errorf("duplicate snapshot %d on tree %d", snap.Id, snap.TreeId)
		}
		seenSnaps[key] = struct{}{}
	}
	return nil
}

// ValidateShape checks a tree record's structure. It does not rehash the leaves.
func (t Tree) ValidateShape(collections map[uint64]Collection) error {
	if t.Id == 0 {
		return fmt.Errorf("tree id is required")
	}
	if _, ok := collections[t.CollectionId]; !ok {
		return fmt.Errorf("tree %d references unknown collection %d", t.Id, t.CollectionId)
	}
	if err := ValidateTreeShape(t.Depth, t.BufferSize, t.CanopyDepth); err != nil {
		return fmt.Errorf("tree %d: %w", t.Id, err)
	}
	if _, err := sdk.AccAddressFromBech32(t.Creator); err != nil {
		return fmt.Errorf("tree %d creator: %w", t.Id, err)
	}
	if t.DepositId == "" {
		return fmt.Errorf("tree %d is missing its deposit id", t.Id)
	}
	if uint64(len(t.ChangeLogs)) != uint64(t.BufferSize) {
		return fmt.Errorf("tree %d changelog length %d != buffer %d", t.Id, len(t.ChangeLogs), t.BufferSize)
	}
	if t.FilledBuffer == 0 || t.FilledBuffer > uint64(t.BufferSize) || t.ActiveIndex >= uint64(t.BufferSize) {
		return fmt.Errorf("tree %d changelog cursor is invalid", t.Id)
	}
	if t.FilledBuffer < uint64(t.BufferSize) && t.ActiveIndex != t.FilledBuffer-1 {
		return fmt.Errorf("tree %d active index does not match the filled buffer", t.Id)
	}
	for i := uint64(0); i < uint64(len(t.ChangeLogs)); i++ {
		if !t.changelogFilled(i) {
			continue
		}
		entry := t.ChangeLogs[i]
		if len(entry.Root) != HashSize || len(entry.Path) != int(t.Depth) {
			return fmt.Errorf("tree %d changelog %d has a bad root or path", t.Id, i)
		}
		for _, node := range entry.Path {
			if len(node) != HashSize {
				return fmt.Errorf("tree %d changelog %d has a short path node", t.Id, i)
			}
		}
	}
	if len(t.Rightmost.Proof) != int(t.Depth) || len(t.Rightmost.Leaf) != HashSize {
		return fmt.Errorf("tree %d rightmost path has the wrong shape", t.Id)
	}
	if uint64(t.Rightmost.Index) > uint64(1)<<t.Depth {
		return fmt.Errorf("tree %d rightmost index is past the tree", t.Id)
	}
	if uint64(len(t.Canopy)) != CanopyNodes(t.CanopyDepth) {
		return fmt.Errorf("tree %d canopy length %d is wrong", t.Id, len(t.Canopy))
	}
	for _, node := range t.Canopy {
		if len(node) != HashSize {
			return fmt.Errorf("tree %d canopy node has the wrong size", t.Id)
		}
	}
	return nil
}

func (t Tree) changelogFilled(i uint64) bool {
	if t.FilledBuffer == uint64(t.BufferSize) {
		return true
	}
	return i < t.FilledBuffer
}
