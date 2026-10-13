package stategen

import (
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app"
	cnfttypes "github.com/DeBrosOfficial/network/chain/x/cnft/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/nullifier"
)

const (
	// assetTrees is how many trees the decompressed assets are spread over, and assetTreeDepth their
	// depth: 16 trees of 2^20 leaves hold 16M assets, past the 1M target.
	assetTrees      = 16
	assetTreeDepth  = 20
	assetTreeBuffer = 8
	assetCanopy     = 0
	collectionName  = "bench"
	// firstID is the id x/cnft gives its first collection and tree.
	firstID = 1
	// metadataCIDPrefix starts every generated metadata CID; the index follows in hex.
	metadataCIDPrefix = "bafybench"
)

// writeStep writes the share of the plan that step owns, into the state of the block being finalized.
func writeStep(a *app.OramaApp, ctx sdk.Context, p Plan, step uint64) error {
	if err := writeNullifiers(a, ctx, p, step); err != nil {
		return err
	}
	if err := writeAssets(a, ctx, p, step); err != nil {
		return err
	}
	return writeAccounts(a, ctx, p, step)
}

// writeNullifiers records a step's nullifiers the way a block does: the records go to the nullifier
// database at the block's height, and the running accumulator and the count in x/shielded's state
// fold them in order, so the node's start-up check (CheckNullifierStore) accepts the database.
func writeNullifiers(a *app.OramaApp, ctx sdk.Context, p Plan, step uint64) error {
	from, to := p.Share(p.Nullifiers, step)
	if from == to {
		return nil
	}
	k := a.ShieldedKeeper
	acc, count, err := readAccumulator(ctx, a)
	if err != nil {
		return err
	}
	nfs := make([][bundle.NodeLen]byte, 0, to-from)
	for i := from; i < to; i++ {
		nf := Nullifier(p.Seed, i)
		nfs = append(nfs, nf)
		acc = nullifier.Fold(acc, nf)
	}
	if err := a.NullifierStore().Commit(ctx.BlockHeight(), nfs); err != nil {
		return fmt.Errorf("failed to record the nullifiers of step %d: %w", step, err)
	}
	if err := k.Accumulator.Set(ctx, acc[:]); err != nil {
		return fmt.Errorf("failed to write the nullifier accumulator: %w", err)
	}
	if err := k.NullifierCount.Set(ctx, count+uint64(len(nfs))); err != nil {
		return fmt.Errorf("failed to write the nullifier count: %w", err)
	}
	return nil
}

// readAccumulator is the accumulator and the count the committed x/shielded state holds; a chain
// that has spent nothing has neither.
func readAccumulator(ctx sdk.Context, a *app.OramaApp) (acc [bundle.NodeLen]byte, count uint64, err error) {
	raw, err := a.ShieldedKeeper.Accumulator.Get(ctx)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return acc, 0, fmt.Errorf("failed to read the nullifier accumulator: %w", err)
	}
	copy(acc[:], raw)
	count, err = a.ShieldedKeeper.NullifierCount.Get(ctx)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return acc, 0, fmt.Errorf("failed to read the nullifier count: %w", err)
	}
	return acc, count, nil
}

// writeAssets writes a step's decompressed assets, after creating the collection and trees they
// belong to in step 0. The assets are x/cnft's own records, written through its collections; the
// compressed leaves of a tree are not chain state (the tree keeps a root), so they are not written.
func writeAssets(a *app.OramaApp, ctx sdk.Context, p Plan, step uint64) error {
	if p.Assets == 0 {
		return nil
	}
	if step == 0 {
		if err := createTrees(a, ctx, p.Seed); err != nil {
			return err
		}
	}
	from, to := p.Share(p.Assets, step)
	creatorHash := cnfttypes.CreatorHash(Account(p.Seed, 0))
	for i := from; i < to; i++ {
		asset := cnfttypes.DecompressedAsset{
			AssetId:      AssetID(p.Seed, i),
			TreeId:       firstID + i%assetTrees,
			LeafIndex:    uint32(i / assetTrees),
			Owner:        Owner(p.Seed, i, p.Assets).String(),
			MetadataCid:  fmt.Sprintf("%s%016x", metadataCIDPrefix, i),
			CreatorHash:  creatorHash,
			HashId:       cnfttypes.HashIDSHA256,
			CollectionId: firstID,
		}
		if err := a.CnftKeeper.Decompressed.Set(ctx, asset.AssetId, asset); err != nil {
			return fmt.Errorf("failed to write asset %d: %w", i, err)
		}
	}
	return nil
}

// createTrees writes the one collection and the trees of the generated assets. It refuses a chain
// that already has any, because the assets name them by id.
func createTrees(a *app.OramaApp, ctx sdk.Context, seed uint64) error {
	k := a.CnftKeeper
	nextCollection, err := k.NextCollectionID.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to read the next collection id: %w", err)
	}
	nextTree, err := k.NextTreeID.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to read the next tree id: %w", err)
	}
	if nextCollection != firstID || nextTree != firstID {
		return fmt.Errorf("x/cnft already has collections or trees (next ids %d and %d); the generator needs an empty genesis", nextCollection, nextTree)
	}
	creator := Account(seed, 0).String()
	col := cnfttypes.Collection{Id: firstID, Creator: creator, Name: collectionName}
	if err := k.Collections.Set(ctx, col.Id, col); err != nil {
		return fmt.Errorf("failed to write the collection: %w", err)
	}
	for id := uint64(firstID); id < firstID+assetTrees; id++ {
		tree, err := cnfttypes.NewTree(id, col.Id, creator, assetTreeDepth, assetTreeBuffer, assetCanopy)
		if err != nil {
			return fmt.Errorf("failed to build tree %d: %w", id, err)
		}
		tree.DepositId = cnfttypes.TreeDepositID(id)
		if err := k.Trees.Set(ctx, id, *tree); err != nil {
			return fmt.Errorf("failed to write tree %d: %w", id, err)
		}
	}
	if err := k.NextCollectionID.Set(ctx, firstID+1); err != nil {
		return fmt.Errorf("failed to advance the collection id: %w", err)
	}
	return k.NextTreeID.Set(ctx, firstID+assetTrees)
}

// writeAccounts writes a step's filler accounts through x/auth's keeper: a new account number and
// the account record, which is how every address that ever received funds enters the state.
func writeAccounts(a *app.OramaApp, ctx sdk.Context, p Plan, step uint64) error {
	from, to := p.Share(p.Accounts(), step)
	for i := from; i < to; i++ {
		acc := a.AccountKeeper.NewAccountWithAddress(ctx, Account(p.Seed, i))
		a.AccountKeeper.SetAccount(ctx, acc)
	}
	return nil
}
