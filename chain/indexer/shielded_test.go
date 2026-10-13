package indexer

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"
	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/testutil"
	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// shieldedBundle is a bundle of n actions whose commitments, nullifiers and ciphertexts are
// recognisable: action i of seed s has epk 0xe0+s.., a compact ciphertext filled with 0xc0+i and
// an out ciphertext filled with 0xd0+i.
func shieldedBundle(seed byte, n int) []byte {
	raw := testutil.Bundle{Seed: seed, ValueBalance: -1000}.Encode(n)
	for i := 0; i < n; i++ {
		at := 1 + i*bundle.ActionLen
		for j := 0; j < bundle.NodeLen; j++ {
			raw[at+128+j] = 0xe0 + seed
		}
		for j := 0; j < bundle.EncCompactLen; j++ {
			raw[at+160+j] = 0xc0 + byte(i)
		}
		for j := 0; j < bundle.OutLen; j++ {
			raw[at+740+j] = 0xd0 + byte(i)
		}
	}
	return raw
}

func subtreeEvent(index uint64, root byte) abci.Event {
	return abci.Event{Type: shieldedtypes.EventTypeSubtreeComplete, Attributes: []abci.EventAttribute{
		{Key: shieldedtypes.AttributeSubtreeIndex, Value: fmt.Sprint(index)},
		{Key: shieldedtypes.AttributeSubtreeRoot, Value: hex.EncodeToString(rootOf(root))},
	}}
}

func rootOf(b byte) []byte {
	out := make([]byte, bundle.NodeLen)
	for i := range out {
		out[i] = b
	}
	return out
}

// tree stands for the chain's note-commitment tree while a test builds its blocks: each bundle it
// is asked for carries the shielded_bundle event the chain emits, with the size the tree had
// before the append, and the tree grows by the bundle's actions.
type tree struct{ size uint64 }

// bundleEvent is the event a committed shielded message emits, for the message at msgIndex.
func bundleEvent(msgIndex int, before, n uint64) abci.Event {
	return abci.Event{Type: shieldedtypes.EventTypeBundle, Attributes: []abci.EventAttribute{
		{Key: shieldedtypes.AttributeKind, Value: "shield"},
		{Key: shieldedtypes.AttributeActions, Value: fmt.Sprint(n)},
		{Key: shieldedtypes.AttributeAmount, Value: "1000"},
		{Key: shieldedtypes.AttributeTreeSizeBefore, Value: fmt.Sprint(before)},
		{Key: shieldedtypes.AttributeCommitments, Value: fmt.Sprint(n)},
		{Key: msgIndexKey, Value: fmt.Sprint(msgIndex)},
	}}
}

// shield is a successful MsgShield of the bundle, with its bundle event and any other events.
func (tr *tree) shield(t *testing.T, raw []byte, events ...abci.Event) fakeTx {
	t.Helper()
	n := uint64(bundleActions(t, raw))
	tx := okTx(t, one(&shieldedtypes.MsgShield{Signer: addr(t, 1), Bundle: raw}), one(&shieldedtypes.MsgShieldResponse{}), addr(t, 1))
	tx.res.Events = append(tx.res.Events, bundleEvent(0, tr.size, n))
	tx.res.Events = append(tx.res.Events, events...)
	tr.size += n
	return tx
}

func (tr *tree) transfer(t *testing.T, raw []byte) fakeTx {
	t.Helper()
	n := uint64(bundleActions(t, raw))
	tx := okTx(t, one(&shieldedtypes.MsgShieldedTransfer{Signer: shieldedtypes.SignerlessAddress().String(), Bundle: raw}),
		one(&shieldedtypes.MsgShieldedTransferResponse{}))
	tx.res.Events = append(tx.res.Events, bundleEvent(0, tr.size, n))
	tr.size += n
	return tx
}

const queryTreeState = "/orama.shielded.v1.Query/TreeState"

// treeStateAsks counts the times the follower asked the chain for its tree state: never.
func treeStateAsks(chain *fakeChain) int {
	n := 0
	for _, a := range chain.asked {
		if len(a) >= len(queryTreeState) && a[:len(queryTreeState)] == queryTreeState {
			n++
		}
	}
	return n
}

func bundleActions(t *testing.T, raw []byte) int {
	t.Helper()
	actions, err := bundle.CompactActions(raw)
	require.NoError(t, err)
	return len(actions)
}

func actionsOf(page ShieldedActionsPage) []ShieldedAction {
	var out []ShieldedAction
	for _, b := range page.Blocks {
		out = append(out, b.Actions...)
	}
	return out
}

// Every action of a committed shielded transaction is indexed, in the order the chain appended its
// commitment, with the position its bundle's event gave it and the tree size after its block; a
// failed transaction leaves nothing, and the chain's state is never read.
func TestFollower_shieldedActionsAreIndexedWithPositions(t *testing.T) {
	chain := newFakeChain()
	tr := &tree{}
	first, second := shieldedBundle(1, 2), shieldedBundle(2, 1)
	chain.add(tr.shield(t, first))
	chain.add(okTx(t, one(&shieldedtypes.MsgShield{Signer: addr(t, 1), Bundle: shieldedBundle(9, 1)}), nil, addr(t, 1)))
	chain.blocks[1].results[0].Code = 5 // the chain refused it
	chain.add(tr.transfer(t, second), tr.shield(t, shieldedBundle(3, 1)))
	store := index(t, chain, 1)

	page, err := store.ShieldedActions(1, ActionsMaxLimit)
	require.NoError(t, err)
	require.Equal(t, int64(3), page.IndexedHeight)
	require.Equal(t, int64(1), page.StartHeight)
	require.False(t, page.HasMore)
	require.Equal(t, int64(4), page.NextHeight, "caught up: the next block to ask for is the one after the index")
	require.Len(t, page.Blocks, 2, "the refused transaction's block holds no action")
	require.Equal(t, int64(1), page.Blocks[0].Height)
	require.Equal(t, uint64(2), page.Blocks[0].TreeSize)
	require.Equal(t, int64(3), page.Blocks[1].Height)
	require.Equal(t, uint64(4), page.Blocks[1].TreeSize)

	got := actionsOf(page)
	require.Len(t, got, 4)
	for i, want := range []struct {
		tx, action uint32
		position   uint64
		seed       byte
		i          int
	}{{0, 0, 0, 1, 0}, {0, 1, 1, 1, 1}, {0, 0, 2, 2, 0}, {1, 0, 3, 3, 0}} {
		a := got[i]
		require.Equal(t, want.tx, a.TxIndex, "action %d", i)
		require.Equal(t, want.action, a.ActionIndex, "action %d", i)
		require.Equal(t, want.position, a.Position, "action %d", i)
		nf, cmx := testutil.Nullifier(want.seed, want.i), commitmentOf(want.seed, want.i)
		require.Equal(t, hex.EncodeToString(nf[:]), a.Nullifier)
		require.Equal(t, hex.EncodeToString(cmx[:]), a.Cmx)
		require.Equal(t, hex.EncodeToString(rootOf(0xe0+want.seed)), a.EphemeralKey)
		require.Equal(t, hex.EncodeToString(repeat(0xc0+byte(want.i), bundle.EncCompactLen)), a.EncCompact)
		require.Equal(t, hex.EncodeToString(repeat(0xd0+byte(want.i), bundle.OutLen)), a.OutCiphertext)
		require.Len(t, a.TxHash, 64)
	}
	require.Equal(t, got[0].TxHash, got[1].TxHash, "one transaction")
	require.NotEqual(t, got[1].TxHash, got[2].TxHash)
	require.Zero(t, treeStateAsks(chain), "positions come from the events, not from state")
}

func commitmentOf(seed byte, i int) [bundle.NodeLen]byte {
	var n [bundle.NodeLen]byte
	n[0], n[1], n[2] = 0x02, seed, byte(i)
	return n
}

func repeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// An index that begins after earlier shielded activity takes its first position from the first
// bundle event it sees: the size the tree had before that bundle.
func TestFollower_shieldedPositionsStartFromTheFirstEvent(t *testing.T) {
	chain := newFakeChain()
	tr := &tree{size: 7}
	chain.add(tr.transfer(t, shieldedBundle(1, 3)))
	store := index(t, chain, 1)
	page, err := store.ShieldedActions(1, ActionsMaxLimit)
	require.NoError(t, err)
	got := actionsOf(page)
	require.Len(t, got, 3)
	require.Equal(t, []uint64{7, 8, 9}, []uint64{got[0].Position, got[1].Position, got[2].Position})
	require.Equal(t, uint64(10), page.Blocks[0].TreeSize)
}

// A block with no shielded action costs the chain no query, and a chain with none has an empty page.
func TestFollower_noShieldedActionsNoQueryAndAnEmptyPage(t *testing.T) {
	chain := newFakeChain()
	chain.add(bankSend(t, addr(t, 1), addr(t, 2)))
	store := index(t, chain, 1)
	require.Zero(t, treeStateAsks(chain))
	page, err := store.ShieldedActions(1, 10)
	require.NoError(t, err)
	require.Empty(t, page.Blocks)
	require.NotNil(t, page.Blocks, "an empty page is [], not null")
	require.Equal(t, int64(2), page.NextHeight)
	require.False(t, page.HasMore)
}

// A node that pruned its state indexes the same: the follower does not read the tree state, so a
// chain that refuses every TreeState query changes nothing.
func TestFollower_shieldedNeedsNoState(t *testing.T) {
	chain := newFakeChain()
	tr := &tree{size: 5}
	chain.add(tr.transfer(t, shieldedBundle(1, 1)))
	chain.add(tr.shield(t, shieldedBundle(2, 2)))
	chain.failing = map[string]error{
		queryKey(queryTreeState, 1): prunedStateError(queryTreeState, 1),
		queryKey(queryTreeState, 2): prunedStateError(queryTreeState, 2),
	}
	store := index(t, chain, 1)
	page, err := store.ShieldedActions(1, ActionsMaxLimit)
	require.NoError(t, err)
	got := actionsOf(page)
	require.Len(t, got, 3)
	require.Equal(t, []uint64{5, 6, 7}, []uint64{got[0].Position, got[1].Position, got[2].Position})
	require.Zero(t, treeStateAsks(chain))
}

// What the index refuses, each with the reason, and nothing of the block written: a bundle event
// that does not follow the previous one (a gap or an overlap), an event whose count is not the
// bundle's actions, a committed shielded message with no event or with two, and malformed fields.
func TestFollower_shieldedBundleEventRules(t *testing.T) {
	step := func(t *testing.T, chain *fakeChain, steps int) error {
		store := openStore(t, t.TempDir())
		t.Cleanup(func() { require.NoError(t, store.Close()) })
		f, err := NewFollower(chain, store, 1)
		require.NoError(t, err)
		for i := 0; i < steps; i++ {
			if _, err = f.Step(context.Background()); err != nil {
				return err
			}
		}
		return nil
	}
	withEvents := func(t *testing.T, raw []byte, events ...abci.Event) fakeTx {
		tx := okTx(t, one(&shieldedtypes.MsgShield{Signer: addr(t, 1), Bundle: raw}), one(&shieldedtypes.MsgShieldResponse{}), addr(t, 1))
		tx.res.Events = append(tx.res.Events, events...)
		return tx
	}
	t.Run("a gap between blocks", func(t *testing.T) {
		chain := newFakeChain()
		chain.add(withEvents(t, shieldedBundle(1, 2), bundleEvent(0, 0, 2)))
		chain.add(withEvents(t, shieldedBundle(2, 1), bundleEvent(0, 3, 1)))
		err := step(t, chain, 2)
		require.ErrorContains(t, err, "took leaf 3 but the index holds 2 leaves")
	})
	t.Run("an overlap between blocks", func(t *testing.T) {
		chain := newFakeChain()
		chain.add(withEvents(t, shieldedBundle(1, 2), bundleEvent(0, 0, 2)))
		chain.add(withEvents(t, shieldedBundle(2, 1), bundleEvent(0, 1, 1)))
		require.ErrorContains(t, step(t, chain, 2), "took leaf 1 but the index holds 2 leaves")
	})
	t.Run("a gap inside a block", func(t *testing.T) {
		chain := newFakeChain()
		chain.add(withEvents(t, shieldedBundle(1, 2), bundleEvent(0, 0, 2)), withEvents(t, shieldedBundle(2, 1), bundleEvent(0, 5, 1)))
		require.ErrorContains(t, step(t, chain, 1), "took leaf 5, want 2")
	})
	t.Run("a count that is not the bundle's actions", func(t *testing.T) {
		chain := newFakeChain()
		chain.add(withEvents(t, shieldedBundle(1, 2), bundleEvent(0, 0, 3)))
		require.ErrorContains(t, step(t, chain, 1), "appended 3 commitments for a bundle of 2 actions")
	})
	t.Run("no event", func(t *testing.T) {
		chain := newFakeChain()
		chain.add(withEvents(t, shieldedBundle(1, 1)))
		require.ErrorContains(t, step(t, chain, 1), "emitted no shielded_bundle event")
	})
	t.Run("an event of another message", func(t *testing.T) {
		chain := newFakeChain()
		chain.add(withEvents(t, shieldedBundle(1, 1), bundleEvent(1, 0, 1)))
		require.ErrorContains(t, step(t, chain, 1), "emitted no shielded_bundle event")
	})
	t.Run("two events", func(t *testing.T) {
		chain := newFakeChain()
		chain.add(withEvents(t, shieldedBundle(1, 1), bundleEvent(0, 0, 1), bundleEvent(0, 1, 1)))
		require.ErrorContains(t, step(t, chain, 1), "emitted two shielded_bundle events")
	})
	t.Run("a field that is not a number", func(t *testing.T) {
		chain := newFakeChain()
		ev := bundleEvent(0, 0, 1)
		ev.Attributes[3].Value = "-4"
		chain.add(withEvents(t, shieldedBundle(1, 1), ev))
		require.ErrorContains(t, step(t, chain, 1), shieldedtypes.AttributeTreeSizeBefore)
	})
	t.Run("a field that is missing", func(t *testing.T) {
		chain := newFakeChain()
		ev := bundleEvent(0, 0, 1)
		ev.Attributes = append(ev.Attributes[:4:4], ev.Attributes[5:]...)
		chain.add(withEvents(t, shieldedBundle(1, 1), ev))
		require.ErrorContains(t, step(t, chain, 1), shieldedtypes.AttributeCommitments)
	})
	t.Run("a second message in a transaction takes the next leaves", func(t *testing.T) {
		chain := newFakeChain()
		tx := okTx(t, []gogoproto.Message{
			&shieldedtypes.MsgShield{Signer: addr(t, 1), Bundle: shieldedBundle(1, 2)},
			&shieldedtypes.MsgShield{Signer: addr(t, 1), Bundle: shieldedBundle(2, 1)},
		}, []gogoproto.Message{&shieldedtypes.MsgShieldResponse{}, &shieldedtypes.MsgShieldResponse{}}, addr(t, 1))
		tx.res.Events = append(tx.res.Events, bundleEvent(0, 4, 2), bundleEvent(1, 6, 1))
		chain.add(tx)
		store := index(t, chain, 1)
		page, err := store.ShieldedActions(1, ActionsMaxLimit)
		require.NoError(t, err)
		got := actionsOf(page)
		require.Len(t, got, 3)
		require.Equal(t, []uint64{4, 5, 6}, []uint64{got[0].Position, got[1].Position, got[2].Position})
		require.Equal(t, []uint32{0, 1, 2}, []uint32{got[0].ActionIndex, got[1].ActionIndex, got[2].ActionIndex})
	})
}

// Pages are whole blocks: walking them by next_height reads every action exactly once whatever the
// limit, a block larger than the limit is a page of its own, and has_more says whether to go on.
func TestShieldedActions_pagingReadsEveryActionOnce(t *testing.T) {
	chain := newFakeChain()
	tr := &tree{}
	sizes := []int{3, 1, 1, 5, 2}
	for i, n := range sizes {
		chain.add(tr.shield(t, shieldedBundle(byte(i+1), n)))
		chain.add(bankSend(t, addr(t, 1), addr(t, 2)))
	}
	store := index(t, chain, 1)

	for limit := 1; limit <= 12; limit++ {
		var seen []uint64
		from, pages := int64(1), 0
		for {
			page, err := store.ShieldedActions(from, limit)
			require.NoError(t, err)
			pages++
			require.LessOrEqual(t, pages, 20, "a walk that does not end")
			for _, b := range page.Blocks {
				require.Equal(t, from <= b.Height, true)
				if len(page.Blocks) > 1 {
					require.LessOrEqual(t, len(actionsOf(page)), limit, "a page of several blocks stays within the limit")
				}
				for _, a := range b.Actions {
					seen = append(seen, a.Position)
				}
			}
			if !page.HasMore {
				require.Equal(t, int64(11), page.NextHeight)
				break
			}
			require.Greater(t, page.NextHeight, from, "the walk moves forward")
			from = page.NextHeight
		}
		want := make([]uint64, 12)
		for i := range want {
			want[i] = uint64(i)
		}
		require.Equal(t, want, seen, "limit %d", limit)
	}
}

// from_height past the index is an empty page that points at the next block to ask for; from_height
// in the middle starts at the first block with actions at or after it.
func TestShieldedActions_fromHeight(t *testing.T) {
	chain := newFakeChain()
	tr := &tree{}
	chain.add(tr.shield(t, shieldedBundle(1, 1)))
	chain.add(bankSend(t, addr(t, 1), addr(t, 2)))
	chain.add(tr.shield(t, shieldedBundle(2, 1)))
	store := index(t, chain, 1)

	page, err := store.ShieldedActions(2, 10)
	require.NoError(t, err)
	require.Len(t, page.Blocks, 1)
	require.Equal(t, int64(3), page.Blocks[0].Height)

	page, err = store.ShieldedActions(40, 10)
	require.NoError(t, err)
	require.Empty(t, page.Blocks)
	require.Equal(t, int64(40), page.NextHeight, "never points backwards")
	require.False(t, page.HasMore)
}

// A subtree the block fills is recorded from the chain's event with the block that filled it, and a
// block that fills none leaves the list as it was.
func TestFollower_subtreeRootsAreRecorded(t *testing.T) {
	chain := newFakeChain()
	l := shieldedtypes.SubtreeLeaves
	tr := &tree{size: l - 2}
	chain.add(tr.shield(t, shieldedBundle(1, 3), subtreeEvent(0, 0xa0))) // leaves l-2, l-1, l
	chain.add(tr.shield(t, shieldedBundle(2, 2)))
	store := index(t, chain, 1)

	page, err := store.SubtreeRoots(0, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1<<16), page.SubtreeLeaves)
	require.NotNil(t, page.FirstIndex)
	require.Equal(t, uint64(0), *page.FirstIndex)
	require.Equal(t, []SubtreeRoot{{Index: 0, Root: hex.EncodeToString(rootOf(0xa0)), Height: 1}}, page.Roots)
	require.Equal(t, uint64(1), page.NextIndex)
	require.False(t, page.HasMore)
	require.Equal(t, int64(2), page.IndexedHeight)

	// Positions straddle the boundary: the first block's last action is leaf l.
	actions, err := store.ShieldedActions(1, 100)
	require.NoError(t, err)
	got := actionsOf(actions)
	require.Equal(t, l-2, got[0].Position)
	require.Equal(t, l, got[2].Position)
	require.Equal(t, l+1, got[3].Position)

	past, err := store.SubtreeRoots(5, 10)
	require.NoError(t, err)
	require.Empty(t, past.Roots)
	require.Equal(t, uint64(5), past.NextIndex, "nothing at or after 5: stay where asked")
}

// subtree rules at the follower: an event for a subtree the block did not fill stops it with the
// reason, and an event in a block with no action does too; a fill with no event before the chain
// has reported any is not an error and leaves the list empty.
func TestFollower_subtreeRootRules(t *testing.T) {
	l := shieldedtypes.SubtreeLeaves
	step := func(chain *fakeChain) error {
		store := openStore(t, t.TempDir())
		t.Cleanup(func() { require.NoError(t, store.Close()) })
		f, err := NewFollower(chain, store, 1)
		require.NoError(t, err)
		_, err = f.Step(context.Background())
		return err
	}
	t.Run("an event for a subtree that did not fill", func(t *testing.T) {
		chain := newFakeChain()
		chain.add((&tree{}).shield(t, shieldedBundle(1, 1), subtreeEvent(0, 1)))
		require.ErrorContains(t, step(chain), "reported subtree 0 filled")
	})
	t.Run("an event in a block with no action", func(t *testing.T) {
		chain := newFakeChain()
		tx := bankSend(t, addr(t, 1), addr(t, 2))
		tx.res.Events = append(tx.res.Events, subtreeEvent(0, 1))
		chain.add(tx)
		require.ErrorContains(t, step(chain), "no shielded action")
	})
	t.Run("a fill without an event before any event", func(t *testing.T) {
		chain := newFakeChain()
		chain.add((&tree{size: l - 1}).shield(t, shieldedBundle(1, 2)))
		store := index(t, chain, 1)
		page, err := store.SubtreeRoots(0, 10)
		require.NoError(t, err)
		require.Empty(t, page.Roots)
		require.NotNil(t, page.Roots)
		require.Nil(t, page.FirstIndex)
	})
}

// recordSubtrees is the rule itself, on a writer, for sizes a bundle of at most 16 actions cannot
// reach in a test.
func TestRecordSubtrees(t *testing.T) {
	l := shieldedtypes.SubtreeLeaves
	record := func(w *writer, base, end uint64, roots map[uint64][]byte) error {
		return w.recordSubtrees(7, base, end, roots)
	}
	open := func(t *testing.T) *writer {
		store := openStore(t, t.TempDir())
		t.Cleanup(func() { require.NoError(t, store.Close()) })
		w := store.newWriter()
		t.Cleanup(func() { _ = w.close() })
		return w
	}
	t.Run("a fill without its event after the first one", func(t *testing.T) {
		w := open(t)
		require.NoError(t, record(w, l-1, l+1, map[uint64][]byte{0: rootOf(1)}))
		err := record(w, 2*l-1, 2*l+1, nil)
		require.ErrorContains(t, err, "subtree 1 filled at height 7 with no shielded_subtree_complete event")
	})
	t.Run("a skipped index", func(t *testing.T) {
		w := open(t)
		require.NoError(t, record(w, l-1, l+1, map[uint64][]byte{0: rootOf(1)}))
		err := record(w, 3*l-1, 3*l+1, map[uint64][]byte{2: rootOf(2)})
		require.ErrorContains(t, err, "index expected 1")
	})
	t.Run("an index outside the fill", func(t *testing.T) {
		w := open(t)
		require.ErrorContains(t, record(w, 10, 12, map[uint64][]byte{0: rootOf(1)}), "reported subtree 0 filled")
		require.ErrorContains(t, record(w, l-1, l+1, map[uint64][]byte{1: rootOf(1)}), "reported subtree 1 filled")
	})
	t.Run("a chain that starts reporting at a later subtree", func(t *testing.T) {
		w := open(t)
		require.NoError(t, record(w, l-1, l+1, nil), "before the first event a fill needs none")
		require.NoError(t, record(w, 2*l-1, 2*l+1, map[uint64][]byte{1: rootOf(1)}))
		first, ok, err := getUint(w.b, []byte(keySubtreesFrom))
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, uint64(1), first)
	})
}

func TestNoteSubtrees_refusesMalformedEvents(t *testing.T) {
	for name, ev := range map[string]abci.Event{
		"short root": {Type: shieldedtypes.EventTypeSubtreeComplete, Attributes: []abci.EventAttribute{
			{Key: "index", Value: "0"}, {Key: "root", Value: "abcd"}}},
		"bad index": {Type: shieldedtypes.EventTypeSubtreeComplete, Attributes: []abci.EventAttribute{
			{Key: "index", Value: "-1"}, {Key: "root", Value: hex.EncodeToString(rootOf(1))}}},
		"bad hex": {Type: shieldedtypes.EventTypeSubtreeComplete, Attributes: []abci.EventAttribute{
			{Key: "index", Value: "0"}, {Key: "root", Value: "zz"}}},
	} {
		var b blockShielded
		require.Error(t, b.noteSubtrees([]abci.Event{ev}), name)
	}
	var b blockShielded
	require.Error(t, b.noteSubtrees([]abci.Event{subtreeEvent(0, 1), subtreeEvent(0, 2)}), "the same subtree twice")
	require.NoError(t, b.noteSubtrees([]abci.Event{{Type: "message"}}))
}

// The routes validate their query, answer JSON with the documented fields, and are cached by what
// they depend on: a page that stopped at its limit is fixed, the page at the head is not.
func TestAPI_shieldedRoutes(t *testing.T) {
	chain := newFakeChain()
	tr := &tree{}
	for i := 0; i < 3; i++ {
		chain.add(tr.shield(t, shieldedBundle(byte(i+1), 2)))
	}
	store := index(t, chain, 1)
	api := NewAPI(store, fixedTip{earliest: 1, latest: 3})

	code, body := get(t, api, http.MethodGet, "/index/v1/shielded/actions?from_height=1&limit=4")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, true, body["has_more"])
	require.Equal(t, float64(3), body["next_height"])
	require.Equal(t, float64(3), body["indexed_height"])
	require.Len(t, body["blocks"], 2)
	first := body["blocks"].([]any)[0].(map[string]any)
	require.Equal(t, float64(2), first["tree_size"])
	action := first["actions"].([]any)[0].(map[string]any)
	for _, k := range []string{"tx_index", "action_index", "position", "tx_hash", "nullifier", "cmx", "ephemeral_key", "enc_compact", "out_ciphertext"} {
		require.Contains(t, action, k)
	}
	require.Len(t, action["enc_compact"], 2*bundle.EncCompactLen)
	require.Len(t, action["out_ciphertext"], 2*bundle.OutLen)

	cache := func(target string) string {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec.Header().Get("Cache-Control")
	}
	require.Equal(t, "public, max-age=600", cache("/index/v1/shielded/actions?limit=4"), "a page with more behind it is fixed")
	require.Equal(t, publicMaxAge(HeadMaxAge), cache("/index/v1/shielded/actions"), "the page at the head is not")
	require.Equal(t, "no-store", cache("/index/v1/shielded/actions?limit=0"), "an error is never cached")

	for _, bad := range []string{
		"?from_height=0", "?from_height=-1", "?from_height=x", "?from_height=1&from_height=2", "?limit=0",
		fmt.Sprintf("?limit=%d", ActionsMaxLimit+1), "?page=1", "?cursor=x", "?from_height=99999999999999999999",
	} {
		code, _ := get(t, api, http.MethodGet, "/index/v1/shielded/actions"+bad)
		require.Equal(t, http.StatusBadRequest, code, bad)
	}
	code, _ = get(t, api, http.MethodPost, "/index/v1/shielded/actions")
	require.Equal(t, http.StatusMethodNotAllowed, code)

	code, body = get(t, api, http.MethodGet, "/index/v1/shielded/subtree-roots")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []any{}, body["roots"])
	require.Equal(t, float64(1<<16), body["subtree_leaves"])
	require.NotContains(t, body, "first_index")
	for _, bad := range []string{"?start_index=-1", "?start_index=01", "?start_index=x", "?limit=0", fmt.Sprintf("?limit=%d", SubtreesMaxLimit+1), "?from_height=1"} {
		code, _ := get(t, api, http.MethodGet, "/index/v1/shielded/subtree-roots"+bad)
		require.Equal(t, http.StatusBadRequest, code, bad)
	}
	code, _ = get(t, api, http.MethodGet, "/index/v1/shielded/subtree-roots?start_index=0")
	require.Equal(t, http.StatusOK, code)
	code, _ = get(t, api, http.MethodGet, "/index/v1/shielded/other")
	require.Equal(t, http.StatusNotFound, code)
}
