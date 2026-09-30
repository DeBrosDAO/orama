//go:build e2e_fleet

package chainservices

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// A piece of 4 KiB is four 1 KiB leaves and a padded tree of four
// (chain/piece: 1 KiB leaves, the leaf count padded to a power of two).
const (
	attestPieceBytes = 4096
	attestPieceLeafs = 4
	// leafSize is chain/piece.LeafSize, the size a piece_bytes value divides by.
	pieceLeafSize = 1024
)

// attestation is one archiver's claim about a canonical height range: x/archive
// ranges are params.range_blocks long and start at a multiple of it plus 1.
type attestation struct {
	start, end int64
	node       string
	cid        string
	hash, root []byte
	piece      []byte
	real, pad  uint64
	pieceBytes uint64
}

// rangeBlocks is x/archive's range width (orama.archive.v1 Params).
func rangeBlocks(t *testing.T, c *chain.Chain) int64 {
	t.Helper()
	var p struct {
		Params struct {
			RangeBlocks chain.Int `json:"range_blocks"`
		} `json:"params"`
	}
	c.Query(t, c.Node(t, 0), &p, "archive", "params")
	if p.Params.RangeBlocks.Int64() <= 0 {
		t.Fatalf("archive range_blocks is %s", p.Params.RangeBlocks.String())
	}
	return p.Params.RangeBlocks.Int64()
}

// newAttestation is a well-formed attestation of the canonical range number
// slot (0 is heights 1..width) by the archiver node nodeID.
func newAttestation(t *testing.T, width, slot int64, nodeID string) attestation {
	t.Helper()
	root := make([]byte, sha256Len)
	if _, err := rand.Read(root); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(root)
	start := slot*width + 1
	return attestation{start: start, end: start + width - 1, node: nodeID, cid: fmt.Sprintf("bafk-e2e-%x", hash[:6]), hash: hash[:], root: root,
		piece: hash[:], real: attestPieceLeafs, pad: attestPieceLeafs, pieceBytes: attestPieceBytes}
}

const sha256Len = 32

func (a attestation) msg(archiver string) chain.Msg {
	return chain.NewMsg("/orama.archive.v1.MsgAttest", map[string]any{
		"archiver": archiver, "start_height": fmt.Sprint(a.start), "end_height": fmt.Sprint(a.end), "bundle_cid": a.cid,
		"bundle_hash": base64.StdEncoding.EncodeToString(a.hash), "merkle_root": base64.StdEncoding.EncodeToString(a.root), "node_id": a.node,
		"piece_root": base64.StdEncoding.EncodeToString(a.piece), "real_leaf_count": fmt.Sprint(a.real),
		"padded_leaf_count": fmt.Sprint(a.pad), "piece_bytes": fmt.Sprint(a.pieceBytes),
	})
}

func attachMsg(archiver, nodeID string, start, end int64, ids ...string) chain.Msg {
	return chain.NewMsg("/orama.archive.v1.MsgAttachReplicas", map[string]any{
		"archiver": archiver, "start_height": fmt.Sprint(start), "end_height": fmt.Sprint(end), "deal_ids": ids, "node_id": nodeID,
	})
}

// TestArchive_attestShapeRefusals: MsgAttest's own shape is checked before
// any state (x/archive/types ValidateAttestation): heights from 1 and not
// descending, a node id, a printable CID of 1-128 bytes, 32-byte hashes and
// roots, and a piece commitment whose leaf counts are the ones piece_bytes
// implies.
func TestArchive_attestShapeRefusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 2, chain.Orama(1))
	width := rangeBlocks(t, c)
	bad := map[string]struct {
		mut  func(*attestation)
		want string
	}{
		"start 0":             {func(x *attestation) { x.start = 0 }, "start height must be at least 1"},
		"end below start":     {func(x *attestation) { x.end = x.start - 1 }, "is below start height"},
		"no node id":          {func(x *attestation) { x.node = "" }, "node id length must be"},
		"empty cid":           {func(x *attestation) { x.cid = "" }, "bundle cid length must be"},
		"cid with a space":    {func(x *attestation) { x.cid = "bafk e2e" }, "printable ASCII without spaces"},
		"cid over 128 bytes":  {func(x *attestation) { x.cid = strings.Repeat("c", 129) }, "bundle cid length must be"},
		"31-byte root":        {func(x *attestation) { x.root = x.root[:sha256Len-1] }, "merkle_root must be 32 bytes"},
		"33-byte hash":        {func(x *attestation) { x.hash = append(x.hash, 0) }, "bundle_hash must be 32 bytes"},
		"31-byte piece root":  {func(x *attestation) { x.piece = x.piece[:sha256Len-1] }, "piece_root must be 32 bytes"},
		"a piece of no bytes": {func(x *attestation) { x.pieceBytes = 0 }, "piece_bytes must be in"},
		"wrong real leaves":   {func(x *attestation) { x.real = attestPieceLeafs - 1 }, "implies"},
		"wrong padded leaves": {func(x *attestation) { x.pad = attestPieceLeafs * 2 }, "padded_leaf_count must be"},
	}
	for name, tc := range bad {
		x := newAttestation(t, width, 0, "e2e-archiver")
		tc.mut(&x)
		chain.RequireRefused(t, name, c.Submit(t, k, chain.TxOptions{}, x.msg(k.Address)), tc.want)
	}
	chain.RequireRefused(t, "replicas with no node id", c.Submit(t, k, chain.TxOptions{}, attachMsg(k.Address, "", 1, width, "1")), "node id length must be")
	chain.RequireRefused(t, "no deal id", c.Submit(t, k, chain.TxOptions{}, attachMsg(k.Address, "e2e-archiver", 1, width)), "at least one deal id is required")
	chain.RequireRefused(t, "duplicate deal id", c.Submit(t, k, chain.TxOptions{}, attachMsg(k.Address, "e2e-archiver", 1, width, "7", "7")), "duplicate deal id")
	chain.RequireRefused(t, "a deal id that is not decimal", c.Submit(t, k, chain.TxOptions{}, attachMsg(k.Address, "e2e-archiver", 1, width, "e2e-1")), "is not a decimal x/storage deal id")
}

// TestArchive_attestRangeRefusals: state-free rules of the keeper come before
// the archiver is looked up: a range must be one of the canonical ranges
// (start at a multiple of range_blocks plus 1, exactly range_blocks long), a
// piece may not exceed max_piece_bytes, and a range that ends at or after
// the executing block is not finalised.
func TestArchive_attestRangeRefusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 1, chain.Orama(1))
	width := rangeBlocks(t, c)
	off := newAttestation(t, width, 0, "e2e-archiver")
	off.start, off.end = 2, width+1
	chain.RequireRefused(t, "a range that does not start at a multiple plus 1", c.Submit(t, k, chain.TxOptions{}, off.msg(k.Address)), "height range is not a canonical range")
	short := newAttestation(t, width, 0, "e2e-archiver")
	short.end = width - 1
	chain.RequireRefused(t, "a range shorter than range_blocks", c.Submit(t, k, chain.TxOptions{}, short.msg(k.Address)), "height range is not a canonical range")
	huge := newAttestation(t, width, 0, "e2e-archiver")
	var params struct {
		Params struct {
			MaxPieceBytes chain.Int `json:"max_piece_bytes"`
		} `json:"params"`
	}
	c.Query(t, c.Node(t, 0), &params, "archive", "params")
	huge.pieceBytes = uint64(params.Params.MaxPieceBytes.Int64()) + 1
	huge.real = (huge.pieceBytes + pieceLeafSize - 1) / pieceLeafSize
	huge.pad = uint64(1)
	for huge.pad < huge.real {
		huge.pad <<= 1
	}
	chain.RequireRefused(t, "a piece over max_piece_bytes", c.Submit(t, k, chain.TxOptions{}, huge.msg(k.Address)), "larger than max_piece_bytes")
	future := newAttestation(t, width, c.Height(t)/width+1, "e2e-archiver")
	chain.RequireRefused(t, "a range in the future", c.Submit(t, k, chain.TxOptions{}, future.msg(k.Address)), "height range is not finalized")
	chain.RequireRefused(t, "replicas for a range in the future", c.Submit(t, k, chain.TxOptions{}, attachMsg(k.Address, "e2e-archiver", future.start, future.end, "1")), "height range is not finalized")
}

// TestArchive_onlyAnActiveArchiverAttests: the signer of MsgAttest and
// MsgAttachReplicas must be the hot key of an active node with a bonded
// ARCHIVER role, which no account of a run chain can be (a role bond is a bank
// balance no run account holds, and a range is decided only by three such
// operators). So the first attestation that pins a range, three archivers
// and three replica deals archiving it, overlapping and conflicting
// attestations are blocked; what is reachable is the refusal of an unknown
// node, of a node that registered the ARCHIVER role and never bonded it, and
// of a node of another role, for a finalised canonical range.
func TestArchive_onlyAnActiveArchiverAttests(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, chain.OperatorNode, chain.Orama(1))
	c.EnsureOperator(t, k)
	width := rangeBlocks(t, c)
	if c.Height(t) <= width {
		harness.SkipNotApplicable(t, fmt.Sprintf("the run chain is at height %d and the first canonical range ends at %d: it applies once the chain is that old", c.Height(t), width))
	}
	archiver := c.RegisterProvenNode(t, k, []string{chain.RoleArchiver}, "archiver")
	relay := c.RegisterProvenNode(t, k, []string{chain.RoleRelay}, "relay")
	cases := map[string]string{"an unknown node": chain.UniqueID(t, "e2e-none-"), "an ARCHIVER node with no bond": archiver.ID, "a relay node": relay.ID}
	for name, id := range cases {
		want := "has no active ARCHIVER role"
		if strings.Contains(name, "unknown") {
			want = "not found"
		}
		a := newAttestation(t, width, 0, id)
		chain.RequireRefused(t, "attest by "+name, c.Submit(t, k, chain.TxOptions{}, a.msg(k.Address)), want)
		chain.RequireRefused(t, "replicas by "+name, c.Submit(t, k, chain.TxOptions{}, attachMsg(k.Address, id, a.start, a.end, "1")), want)
	}
	c.RequireInvariants(t, "refused archive attestations")
}

// TestArchive_retainHeightQuery: the retain height is the lower of the last
// archived height and tip minus the 14-day window, reported with its inputs
// (x/archive/types/retain.go RetainHeight); params carry the window, which
// is at least 14 days of 6-second blocks (201,600).
func TestArchive_retainHeightQuery(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	var r struct {
		RetainHeight          chain.Int `json:"retain_height"`
		Tip                   chain.Int `json:"tip"`
		LastArchivedHeight    chain.Int `json:"last_archived_height"`
		RetentionWindowBlocks chain.Int `json:"retention_window_blocks"`
	}
	c.Query(t, c.Node(t, 0), &r, "archive", "retain-height")
	floor := r.Tip.Int64() - r.RetentionWindowBlocks.Int64()
	want := floor
	if r.LastArchivedHeight.Int64() < floor {
		want = r.LastArchivedHeight.Int64()
	}
	if r.RetainHeight.Int64() != want || r.RetentionWindowBlocks.Int64() < 201_600 {
		t.Errorf("retain height %d (tip %d, last archived %d, window %d), want %d with a window of at least 201600",
			r.RetainHeight.Int64(), r.Tip.Int64(), r.LastArchivedHeight.Int64(), r.RetentionWindowBlocks.Int64(), want)
	}
	var p struct {
		Params struct {
			RetentionWindowBlocks chain.Int `json:"retention_window_blocks"`
		} `json:"params"`
	}
	c.Query(t, c.Node(t, 0), &p, "archive", "params")
	if p.Params.RetentionWindowBlocks.Cmp(r.RetentionWindowBlocks) != 0 {
		t.Errorf("params window %s, retain-height window %s", p.Params.RetentionWindowBlocks.String(), r.RetentionWindowBlocks.String())
	}
}
