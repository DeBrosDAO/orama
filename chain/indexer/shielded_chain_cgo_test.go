//go:build cgo && orchardffi

package indexer

import (
	"encoding/binary"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchard"
)

// goldenSubtrees is chain/x/shielded/orchardffi/testdata/subtree-roots.txt: the roots of the first
// two subtrees and the anchor of the tree of leaves 1 ..= 2 * 2^16, which the Rust test
// golden_subtree_roots_are_the_trees_and_recombine_to_its_anchor computes with upstream's tree and
// recombines (the roots fold, level by level, to the anchor).
func goldenSubtrees(t *testing.T) (roots [2]string, anchor string) {
	t.Helper()
	raw, err := os.ReadFile("../x/shielded/orchardffi/testdata/subtree-roots.txt")
	require.NoError(t, err)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 3 && f[0] == "subtree_root" && f[1] == "0":
			roots[0] = f[2]
		case len(f) == 3 && f[0] == "subtree_root" && f[1] == "1":
			roots[1] = f[2]
		case len(f) == 2 && f[0] == "anchor":
			anchor = f[1]
		}
	}
	require.Len(t, roots[0], 64)
	require.Len(t, roots[1], 64)
	require.Len(t, anchor, 64)
	return roots, anchor
}

// leaf i is i + 1 in little-endian, the commitment the Rust golden test appends at position i.
func goldenLeaf(i uint64) [bundle.NodeLen]byte {
	var cmx [bundle.NodeLen]byte
	binary.LittleEndian.PutUint64(cmx[:], i+1)
	return cmx
}

func goldenNullifier(i uint64) [bundle.NodeLen]byte {
	var nf [bundle.NodeLen]byte
	nf[0] = 0x01
	binary.LittleEndian.PutUint64(nf[1:], i)
	return nf
}

// The chain fills two real 2^16-leaf subtrees through shield bundles of 29 to 32 actions, so that
// both boundaries fall inside a bundle. The index follows the same blocks from their events alone.
// The roots the chain keeps in state, the roots its query pages, and the roots the index serves are
// the same, they are the roots upstream's tree computes for these leaves, and those roots recombine
// to the anchor the chain ends on (the Rust test that wrote the golden file recombines them).
func TestShieldedSubtrees_chainAndIndexAgreeAndRecombineToTheAnchor(t *testing.T) {
	wantRoots, wantAnchor := goldenSubtrees(t)
	total := 2 * shieldedtypes.SubtreeLeaves
	e := testutil.NewEnvWithTree(t, func(gs *shieldedtypes.GenesisState) { gs.Params.MaxActionsPerBundle = bundle.MaxActions }, orchard.NewTree())
	empty, err := e.Keeper.EmptyRoot()
	require.NoError(t, err)
	srv := keeper.NewMsgServerImpl(e.Keeper)
	signer := addr(t, 1)

	const bundlesPerBlock = 16
	sizes := []uint64{32, 31, 29}
	chain := newFakeChain()
	var next uint64 // the next leaf
	for round := 0; next < total; round++ {
		var block []fakeTx
		for len(block) < bundlesPerBlock && next < total {
			n := min(sizes[(round*bundlesPerBlock+len(block))%len(sizes)], total-next)
			var nfs, cmxs [][bundle.NodeLen]byte
			for i := uint64(0); i < n; i++ {
				nfs, cmxs = append(nfs, goldenNullifier(next+i)), append(cmxs, goldenLeaf(next+i))
			}
			raw := testutil.Bundle{Nullifiers: nfs, Commitments: cmxs, ValueBalance: -1000, Anchor: empty}.Encode(int(n))
			e.Bank.Fund(signer, 1000+int64(n))
			ctx := e.Ctx.WithEventManager(sdk.NewEventManager())
			_, err := srv.Shield(ctx, &shieldedtypes.MsgShield{Signer: signer, Bundle: raw})
			require.NoError(t, err, "bundle at leaf %d", next)

			tx := okTx(t, one(&shieldedtypes.MsgShield{Signer: signer, Bundle: raw}), one(&shieldedtypes.MsgShieldResponse{}), signer)
			tx.res.Events = append(tx.res.Events, withMsgIndex(ctx.EventManager().Events().ToABCIEvents(), 0)...)
			block = append(block, tx)
			next += n
		}
		chain.add(block...)
		e.EndBlock()
	}

	size, err := e.Keeper.TreeSize.Get(e.Ctx)
	require.NoError(t, err)
	require.Equal(t, total, size)
	anchor, err := e.Keeper.CurrentRoot.Get(e.Ctx)
	require.NoError(t, err)
	require.Equal(t, wantAnchor, hex.EncodeToString(anchor), "the chain's root is upstream's root for these leaves")

	// The chain's state.
	var inState []SubtreeRoot
	require.NoError(t, e.Keeper.Subtrees.Walk(e.Ctx, nil, func(index uint64, r shieldedtypes.SubtreeRoot) (bool, error) {
		require.Equal(t, index, r.Index)
		inState = append(inState, SubtreeRoot{Index: index, Root: hex.EncodeToString(r.Root), Height: r.Height})
		return false, nil
	}))
	require.Len(t, inState, 2)
	for i, r := range inState {
		require.Equal(t, wantRoots[i], r.Root, "subtree %d in state is upstream's root", i)
	}
	inv, err := e.Keeper.CheckLedgers(e.Ctx)
	require.NoError(t, err)
	require.True(t, inv.SubtreeRootsMatch, inv.Detail)

	// The chain's query.
	resp, err := keeper.NewQueryServerImpl(e.Keeper).SubtreeRoots(e.Ctx, &shieldedtypes.QuerySubtreeRootsRequest{})
	require.NoError(t, err)
	require.Equal(t, uint64(2), resp.Total)
	var queried []SubtreeRoot
	for _, r := range resp.Roots {
		queried = append(queried, SubtreeRoot{Index: r.Index, Root: hex.EncodeToString(r.Root), Height: r.Height})
	}
	require.Equal(t, inState, queried)

	// The index, from the events of the same blocks.
	store := index(t, chain, 1)
	page, err := store.SubtreeRoots(0, 10)
	require.NoError(t, err)
	require.Equal(t, inState, page.Roots, "the index serves what the chain keeps, with the block that filled each")

	// Every leaf is in the index at its position.
	var seen uint64
	for from := int64(1); ; {
		p, err := store.ShieldedActions(from, ActionsMaxLimit)
		require.NoError(t, err)
		for _, b := range p.Blocks {
			for _, a := range b.Actions {
				cmx := goldenLeaf(seen)
				require.Equal(t, seen, a.Position)
				require.Equal(t, hex.EncodeToString(cmx[:]), a.Cmx, "leaf %d", seen)
				seen++
			}
		}
		if !p.HasMore {
			break
		}
		from = p.NextHeight
	}
	require.Equal(t, total, seen)
}

// withMsgIndex gives the events of a message the attribute the SDK appends to them.
func withMsgIndex(events []abci.Event, msg int) []abci.Event {
	for i := range events {
		events[i].Attributes = append(events[i].Attributes, abci.EventAttribute{Key: msgIndexKey, Value: string(rune('0' + msg))})
	}
	return events
}
