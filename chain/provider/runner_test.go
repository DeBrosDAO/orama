package provider

import (
	"os"
	"path/filepath"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/piece"
)

func TestNewRunner_refusesMissingConfig(t *testing.T) {
	store, err := Open(t.TempDir(), nil, nil)
	require.NoError(t, err)
	state := filepath.Join(t.TempDir(), "state.json")
	ok := Config{NodeID: "n1", Signer: "orama1x", StatePath: state, StartHeight: 1}
	for name, cfg := range map[string]Config{
		"no node":      {Signer: "s", StatePath: state, StartHeight: 1},
		"no signer":    {NodeID: "n1", StatePath: state, StartHeight: 1},
		"no state":     {NodeID: "n1", Signer: "s", StartHeight: 1},
		"start height": {NodeID: "n1", Signer: "s", StatePath: state, StartHeight: 0},
	} {
		_, err := NewRunner(store, nil, ok)
		require.Error(t, err, "nil chain")
		_, err = NewRunner(store, stubChain{}, cfg)
		require.Errorf(t, err, name)
	}
}

func TestLoadState_startsBeforeStartHeightAndRefusesCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	_, err := loadState(filepath.Join(dir, "absent.json"), 0)
	require.Error(t, err, "a new state needs a start height")
	st, err := loadState(filepath.Join(dir, "absent.json"), 40)
	require.NoError(t, err)
	require.Equal(t, int64(39), st.Height)

	bad := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte("{"), 0o600))
	_, err = loadState(bad, 1)
	require.Error(t, err)

	neg := filepath.Join(dir, "neg.json")
	require.NoError(t, os.WriteFile(neg, []byte(`{"height":-3}`), 0o600))
	_, err = loadState(neg, 1)
	require.Error(t, err)
}

func TestSlotEvent_needsDealSlotAndNode(t *testing.T) {
	ev := func(kv ...string) abci.Event {
		e := abci.Event{Type: eventAssigned}
		for i := 0; i < len(kv); i += 2 {
			e.Attributes = append(e.Attributes, abci.EventAttribute{Key: kv[i], Value: kv[i+1]})
		}
		return e
	}
	deal, slot, node, ok := slotEvent(ev("deal_id", "7", "slot", "2", "node_id", "n1"))
	require.True(t, ok)
	require.Equal(t, uint64(7), deal)
	require.Equal(t, uint32(2), slot)
	require.Equal(t, "n1", node)

	for _, bad := range []abci.Event{
		ev("slot", "2", "node_id", "n1"),
		ev("deal_id", "0", "slot", "2", "node_id", "n1"),
		ev("deal_id", "7", "slot", "x", "node_id", "n1"),
		ev("deal_id", "7", "slot", "4294967296", "node_id", "n1"),
		ev("deal_id", "7", "slot", "2"),
	} {
		_, _, _, ok := slotEvent(bad)
		require.False(t, ok)
	}
}

func TestApplyEvents_recordsOnlyThisNodesAssignmentsOnce(t *testing.T) {
	r := &Runner{nodeID: "n1"}
	assigned := abci.Event{Type: eventAssigned, Attributes: []abci.EventAttribute{
		{Key: "deal_id", Value: "3"}, {Key: "slot", Value: "0"}, {Key: "node_id", Value: "n1"},
	}}
	other := abci.Event{Type: eventAssigned, Attributes: []abci.EventAttribute{
		{Key: "deal_id", Value: "3"}, {Key: "slot", Value: "1"}, {Key: "node_id", Value: "n2"},
	}}
	evicted := abci.Event{Type: "storage_slot_evicted", Attributes: assigned.Attributes}
	require.True(t, r.applyEvents([]abci.Event{assigned, other, evicted}))
	require.False(t, r.applyEvents([]abci.Event{assigned}), "the same slot is not added twice")
	require.Len(t, r.state.Pending, 1)
	require.False(t, r.Assigned(""), "a slot whose root is not read yet accepts no upload")
}

func TestRelease_keepsAPieceAnotherSlotStillBinds(t *testing.T) {
	s, err := Open(t.TempDir(), nil, nil)
	require.NoError(t, err)
	data := make([]byte, 3000)
	root := commitRoot(t, data)
	dec, err := s.Ingest("c1", data, root)
	require.NoError(t, err)
	require.True(t, dec.Accept)
	require.NoError(t, s.Bind("c1", 1, 0))
	require.NoError(t, s.Bind("c1", 2, 0))

	require.NoError(t, s.Release(1, 0, nil))
	require.True(t, s.Has("c1"), "deal 2 still binds the piece")
	require.NoError(t, s.Release(1, 0, nil), "releasing an unbound slot is a no-op")

	require.NoError(t, s.Release(2, 0, func(cid string) bool { return cid == "c1" }))
	require.True(t, s.Has("c1"), "a piece a waiting slot claims is kept")
	require.NoError(t, s.Bind("c1", 3, 0))
	require.NoError(t, s.Release(3, 0, nil))
	require.False(t, s.Has("c1"))
	bound, err := s.Assignments()
	require.NoError(t, err)
	require.Empty(t, bound)
	used, err := s.UsedBytes()
	require.NoError(t, err)
	require.Zero(t, used)
}

func commitRoot(t *testing.T, data []byte) []byte {
	t.Helper()
	c, err := piece.Commit(data)
	require.NoError(t, err)
	return c.Root
}

// stubChain satisfies Chain for constructor tests. No method is called.
type stubChain struct{ Chain }
