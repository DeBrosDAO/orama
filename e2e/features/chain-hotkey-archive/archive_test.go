//go:build e2e_fleet

package chainhotkeyarchive

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// A piece of 4 KiB is four 1 KiB leaves and a padded tree of four (chain/piece:
// 1 KiB leaves, the leaf count padded to a power of two).
const (
	dealPieceBytes = 4096
	dealPieceLeafs = 4
	// dealRangeEnd is a height every run chain is past when its tests run.
	dealRangeEnd = 10
	// futureBlocks puts a range's end well beyond the head.
	futureBlocks = 1000
	sha256Len    = 32
)

// dealSpec is one MsgCreateArchiveDeal.
type dealSpec struct {
	start, end                int64
	nodeID                    string
	root                      []byte
	real, padded, pieceLength uint64
}

func newDealSpec(t *testing.T, nodeID string) dealSpec {
	t.Helper()
	root := make([]byte, sha256Len)
	copy(root, chain.UniqueID(t, "e2e-piece-"))
	return dealSpec{start: 1, end: dealRangeEnd, nodeID: nodeID, root: root, real: dealPieceLeafs, padded: dealPieceLeafs, pieceLength: dealPieceBytes}
}

func (d dealSpec) msg(archiver string) chain.Msg {
	return chain.NewMsg("/orama.archive.v1.MsgCreateArchiveDeal", map[string]any{
		"archiver": archiver, "start_height": fmt.Sprint(d.start), "end_height": fmt.Sprint(d.end), "node_id": d.nodeID,
		"piece_root": b64(d.root), "real_leaf_count": fmt.Sprint(d.real), "padded_leaf_count": fmt.Sprint(d.padded),
		"piece_bytes": fmt.Sprint(d.pieceLength),
	})
}

// TestArchiveDeal_shapeRefusals: the message's own shape is checked before
// any state: heights from 1 and not descending, a node id, a 32-byte piece
// root, a piece of at least one byte whose leaf counts are the ones its size
// implies.
func TestArchiveDeal_shapeRefusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 0, chain.Orama(1))
	cases := []struct {
		name string
		mut  func(*dealSpec)
		want string
	}{
		{"a start height of 0", func(d *dealSpec) { d.start = 0 }, "start height must be at least 1"},
		{"an end below the start", func(d *dealSpec) { d.end = d.start - 1 }, "is below start height"},
		{"no node id", func(d *dealSpec) { d.nodeID = "" }, "node id length must be"},
		{"a 31-byte piece root", func(d *dealSpec) { d.root = d.root[:sha256Len-1] }, "piece_root must be 32 bytes"},
		{"a piece of zero bytes", func(d *dealSpec) { d.pieceLength = 0 }, "piece_bytes must be in"},
		{"the wrong real leaf count", func(d *dealSpec) { d.real = dealPieceLeafs - 1 }, "implies"},
		{"the wrong padded leaf count", func(d *dealSpec) { d.padded = dealPieceLeafs * 2 }, "padded_leaf_count must be"},
	}
	for _, tc := range cases {
		d := newDealSpec(t, "e2e-archiver")
		tc.mut(&d)
		chain.RequireRefused(t, tc.name, c.Submit(t, k, chain.TxOptions{}, d.msg(k.Address)), tc.want)
	}
}

// TestArchiveDeal_notFinalizedRangeRefused: a deal is opened only for a
// finalised range: one that ends at or after the executing block is refused.
func TestArchiveDeal_notFinalizedRangeRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	d := newDealSpec(t, "e2e-archiver")
	d.start, d.end = c.Height(t)+1, c.Height(t)+futureBlocks
	chain.RequireRefused(t, "a range in the future", c.Submit(t, k, chain.TxOptions{}, d.msg(k.Address)), "height range is not finalized")
}

// TestArchiveDeal_onlyAnActiveArchiverOpensDeals: the signer must be the hot
// key of an active node with a bonded ARCHIVER role, which no account of a run
// chain can be (a role bond is a bank balance no run account holds, and a
// range needs the attestations of three such operators); so a finalised
// range's deal is refused for an
// unknown node, for a node that only registered the ARCHIVER role, and for a
// node with another role, before x/archive looks at the range or x/storage at
// a price. Every module's invariants hold after the refusals.
func TestArchiveDeal_onlyAnActiveArchiverOpensDeals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, k)
	archiver := c.RegisterProvenNode(t, k, []string{chain.RoleArchiver}, "archiver")
	relay := c.RegisterProvenNode(t, k, []string{chain.RoleRelay}, "relay")
	cases := []struct {
		name, nodeID, want string
	}{
		{"an unknown node", chain.UniqueID(t, "e2e-none-"), "not found"},
		{"a registered ARCHIVER node with no bond", archiver.ID, "has no active ARCHIVER role"},
		{"a relay node", relay.ID, "has no active ARCHIVER role"},
	}
	for _, tc := range cases {
		chain.RequireRefused(t, tc.name, c.Submit(t, k, chain.TxOptions{}, newDealSpec(t, tc.nodeID).msg(k.Address)), tc.want)
	}
	c.RequireInvariants(t, "refused archive deals")
}
