package keeper_test

import (
	"encoding/hex"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

// eventsOfType returns the attributes of each event of typ the context's event manager holds.
func eventsOfType(ctx sdk.Context, typ string) []map[string]string {
	var out []map[string]string
	for _, e := range ctx.EventManager().Events() {
		if e.Type != typ {
			continue
		}
		attrs := map[string]string{}
		for _, a := range e.Attributes {
			attrs[a.Key] = a.Value
		}
		out = append(out, attrs)
	}
	return out
}

// decompressedFixture mints a leaf for owner in a new tree and decompresses it.
func decompressedFixture(t *testing.T, f *fixture, owner string) (treeID uint64, body types.Leaf) {
	t.Helper()
	creator := bech(1)
	collection := f.createCollection(t, creator, 0)
	treeID, root := f.createTree(t, creator, collection, 4, 4, 0)
	body = leafBody(t, creator, owner, assetID(9), "cid-event", 0)
	_, err := f.msg.Mint(f.ctx, &types.MsgMint{
		Creator: creator, TreeId: treeID, Root: root,
		Leaves: []types.MintLeaf{{AssetId: body.AssetId, Owner: owner, MetadataCid: body.MetadataCid}},
	})
	require.NoError(t, err)
	_, err = f.msg.Decompress(f.ctx, &types.MsgDecompress{
		Owner: owner, TreeId: treeID, Current: body, Proof: prove(t, [][]byte{mustHash(t, body)}, 0, 4),
	})
	require.NoError(t, err)
	return treeID, body
}

func TestCompress_emitsTheConsumedRecordAndTheNewLeaf(t *testing.T) {
	f := newFixture(t)
	owner := bech(2)
	treeID, body := decompressedFixture(t, f, owner)
	record, err := f.keeper.Decompressed.Get(f.ctx, body.AssetId)
	require.NoError(t, err)
	tree, err := f.keeper.GetTree(f.ctx, treeID)
	require.NoError(t, err)

	ctx := f.ctx.WithEventManager(sdk.NewEventManager())
	res, err := f.msg.Compress(ctx, &types.MsgCompress{Owner: owner, AssetId: body.AssetId, Root: tree.Root()})
	require.NoError(t, err)

	got := eventsOfType(ctx, types.EventTypeCompress)
	require.Len(t, got, 1)
	require.Equal(t, map[string]string{
		types.AttributeKeyAssetID:     hex.EncodeToString(record.AssetId),
		types.AttributeKeyDelegate:    record.Delegate,
		types.AttributeKeyMetadataCID: record.MetadataCid,
		types.AttributeKeyCreatorHash: hex.EncodeToString(record.CreatorHash),
		types.AttributeKeyHashID:      strconv.FormatUint(uint64(record.HashId), 10),
		types.AttributeKeyTreeID:      strconv.FormatUint(res.TreeId, 10),
		types.AttributeKeyLeafIndex:   strconv.FormatUint(uint64(res.LeafIndex), 10),
	}, got[0])
	require.NotEmpty(t, got[0][types.AttributeKeyCreatorHash], "the creator hash is what the indexer cannot recompute")
}

func TestCompress_failedCompressEmitsNothing(t *testing.T) {
	f := newFixture(t)
	owner := bech(2)
	treeID, body := decompressedFixture(t, f, owner)
	tree, err := f.keeper.GetTree(f.ctx, treeID)
	require.NoError(t, err)

	ctx := f.ctx.WithEventManager(sdk.NewEventManager())
	_, err = f.msg.Compress(ctx, &types.MsgCompress{Owner: bech(3), AssetId: body.AssetId, Root: tree.Root()})
	require.Error(t, err, "only the decompressed owner can compress")
	_, err = f.msg.Compress(ctx, &types.MsgCompress{Owner: owner, AssetId: assetID(77), Root: tree.Root()})
	require.Error(t, err, "an asset that is not decompressed")
	require.Empty(t, eventsOfType(ctx, types.EventTypeCompress))
}
