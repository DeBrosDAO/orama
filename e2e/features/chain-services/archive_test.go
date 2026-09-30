//go:build e2e_fleet

package chainservices

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	mrand "math/rand/v2"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// rangeWidth is the width of the ranges this test attests. x/archive has no
// message that removes a range, so the records stay for the (disposable)
// run chain; they sit well away from height 1, where an orama-global
// archiver would start (docs/CHAIN.md "History archiver": ranges 1, 1+w, ...),
// so they never block one.
const rangeWidth = 10

// Where freeRange looks: aligned slots from minSlot up to headMargin below
// the head, at most freeRangeTries random picks.
const (
	minSlot        = 10
	headMargin     = 2 * rangeWidth
	freeRangeTries = 20
)

// freeRange picks an aligned range (start 1 + j*rangeWidth) below the head
// that no range occupies. Every range these tests pin is aligned and of the
// same width, so two of them either coincide or do not overlap at all: an
// exact range query that finds nothing proves the slot free of every other
// test's range, and a random slot keeps concurrent tests apart.
func freeRange(t *testing.T, c *chain.Chain) attestation {
	t.Helper()
	top := (c.Height(t) - headMargin - 1) / rangeWidth
	if top <= minSlot {
		t.Fatalf("the chain is at %d: too low for an aligned range above slot %d", c.Height(t), minSlot)
	}
	for i := 0; i < freeRangeTries; i++ {
		a := newAttestation(t, 1+(minSlot+mrand.Int64N(top-minSlot))*rangeWidth)
		out := c.QueryOut(t, c.Node(t, 0), "archive", "range", fmt.Sprint(a.start), fmt.Sprint(a.end))
		if out.Exit != 0 && chain.NotFound(out.Stdout+out.Stderr) {
			return a
		}
		if out.Exit != 0 {
			t.Fatalf("archive range %d %d: %s", a.start, a.end, out.Stderr)
		}
	}
	t.Fatalf("no free aligned range in %d random picks below height %d", freeRangeTries, top*rangeWidth)
	return attestation{}
}

// attestation is one archiver's claim about a height range.
type attestation struct {
	start, end int64
	cid        string
	hash, root []byte
}

func newAttestation(t *testing.T, start int64) attestation {
	t.Helper()
	root := make([]byte, 32)
	if _, err := rand.Read(root); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(root)
	return attestation{start: start, end: start + rangeWidth - 1, cid: fmt.Sprintf("bafk-e2e-%x", hash[:6]), hash: hash[:], root: root}
}

func (a attestation) msg(archiver string) chain.Msg {
	return chain.NewMsg("/orama.archive.v1.MsgAttest", map[string]any{
		"archiver": archiver, "start_height": fmt.Sprint(a.start), "end_height": fmt.Sprint(a.end), "bundle_cid": a.cid,
		"bundle_hash": base64.StdEncoding.EncodeToString(a.hash), "merkle_root": base64.StdEncoding.EncodeToString(a.root),
	})
}

func attachMsg(archiver string, start, end int64, ids ...string) chain.Msg {
	return chain.NewMsg("/orama.archive.v1.MsgAttachReplicas", map[string]any{
		"archiver": archiver, "start_height": fmt.Sprint(start), "end_height": fmt.Sprint(end), "deal_ids": ids,
	})
}

// rangeView is `oramad query archive range`.
type rangeView struct {
	Range struct {
		BundleCID string   `json:"bundle_cid"`
		Archivers []string `json:"archivers"`
		DealIDs   []string `json:"deal_ids"`
		Archived  bool     `json:"archived"`
	} `json:"range"`
}

func queryRange(t *testing.T, c *chain.Chain, a attestation) rangeView {
	t.Helper()
	var v rangeView
	c.Query(t, c.Node(t, 0), &v, "archive", "range", fmt.Sprint(a.start), fmt.Sprint(a.end))
	return v
}

// TestArchive_firstAttestationPinsTheRange: the first attestation of a
// finalised range pins its root, bundle hash and CID; another archiver with a
// different root or bundle is refused; the same archiver again changes
// nothing; a range only becomes archived with three matching archivers AND
// three replica deal ids (docs/CHAIN.md "History archiver"); an archived
// range that does not start at 1 does not move the contiguous last archived
// height.
func TestArchive_firstAttestationPinsTheRange(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	keys := []chain.Key{c.FundedValidator(t, 0, chain.Orama(1)), c.FundedValidator(t, 1, chain.Orama(1)), c.FundedValidator(t, 2, chain.Orama(1))}
	a := freeRange(t, c)
	lastBefore := lastArchived(t, c)
	chain.RequireOK(t, "first attestation", c.Submit(t, keys[0], chain.TxOptions{}, a.msg(keys[0].Address)))
	wrongRoot, wrongBundle := a, a
	wrongRoot.root = append([]byte{a.root[0] ^ 0xff}, a.root[1:]...)
	wrongBundle.cid += "-other"
	chain.RequireRefused(t, "different root", c.Submit(t, keys[1], chain.TxOptions{}, wrongRoot.msg(keys[1].Address)), "wrong merkle root")
	chain.RequireRefused(t, "different bundle", c.Submit(t, keys[1], chain.TxOptions{}, wrongBundle.msg(keys[1].Address)), "bundle does not match the pinned range")
	chain.RequireOK(t, "same archiver again", c.Submit(t, keys[0], chain.TxOptions{}, a.msg(keys[0].Address)))
	if v := queryRange(t, c, a); len(v.Range.Archivers) != 1 || v.Range.BundleCID != a.cid || v.Range.Archived {
		t.Fatalf("range after one archiver (twice): %+v", v.Range)
	}
	for _, k := range keys[1:] {
		chain.RequireOK(t, "matching attestation", c.Submit(t, k, chain.TxOptions{}, a.msg(k.Address)))
	}
	chain.RequireOK(t, "two replicas", c.Submit(t, keys[0], chain.TxOptions{}, attachMsg(keys[0].Address, a.start, a.end, "e2e-1", "e2e-2")))
	if v := queryRange(t, c, a); len(v.Range.Archivers) != 3 || v.Range.Archived {
		t.Fatalf("three archivers and two deal ids must not archive: %+v", v.Range)
	}
	chain.RequireOK(t, "third replica", c.Submit(t, keys[1], chain.TxOptions{}, attachMsg(keys[1].Address, a.start, a.end, "e2e-2", "e2e-3")))
	if v := queryRange(t, c, a); !v.Range.Archived || len(v.Range.DealIDs) != 3 {
		t.Fatalf("three archivers and three deal ids must archive: %+v", v.Range)
	}
	if got := lastArchived(t, c); got != lastBefore {
		t.Errorf("last archived height moved %d -> %d for a range that does not start at 1", lastBefore, got)
	}
}

// TestArchive_rangeRefusals: a range overlapping a pinned one, a range that
// is not finalised (it ends at or after the executing block), malformed
// heights, CID and hashes, and replicas for an unknown range are refused.
func TestArchive_rangeRefusals(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	k := c.FundedValidator(t, 2, chain.Orama(1))
	a := freeRange(t, c)
	chain.RequireOK(t, "pin a range", c.Submit(t, k, chain.TxOptions{}, a.msg(k.Address)))
	overlap := newAttestation(t, a.start+rangeWidth/2)
	chain.RequireRefused(t, "overlapping range", c.Submit(t, k, chain.TxOptions{}, overlap.msg(k.Address)), "overlaps an existing range")
	future := newAttestation(t, c.Height(t)+1000)
	chain.RequireRefused(t, "range in the future", c.Submit(t, k, chain.TxOptions{}, future.msg(k.Address)), "height range is not finalized")
	bad := map[string]struct {
		mut  func(*attestation)
		want string
	}{
		"start 0":            {func(x *attestation) { x.start = 0 }, "start height must be at least 1"},
		"end below start":    {func(x *attestation) { x.end = x.start - 1 }, "is below start height"},
		"empty cid":          {func(x *attestation) { x.cid = "" }, "bundle cid length must be"},
		"cid with a space":   {func(x *attestation) { x.cid = "bafk e2e" }, "printable ASCII without spaces"},
		"31-byte root":       {func(x *attestation) { x.root = x.root[:31] }, "merkle_root must be 32 bytes"},
		"33-byte hash":       {func(x *attestation) { x.hash = append(x.hash, 0) }, "bundle_hash must be 32 bytes"},
		"cid over 128 bytes": {func(x *attestation) { x.cid = strings.Repeat("c", 129) }, "bundle cid length must be"},
	}
	for name, tc := range bad {
		x := newAttestation(t, 1)
		tc.mut(&x)
		chain.RequireRefused(t, name, c.Submit(t, k, chain.TxOptions{}, x.msg(k.Address)), tc.want)
	}
	chain.RequireRefused(t, "replicas for an unknown range", c.Submit(t, k, chain.TxOptions{}, attachMsg(k.Address, 7, 8, "e2e-x")), "unknown height range")
	chain.RequireRefused(t, "duplicate deal id", c.Submit(t, k, chain.TxOptions{}, attachMsg(k.Address, a.start, a.end, "e2e-d", "e2e-d")), "duplicate deal id")
	chain.RequireRefused(t, "no deal id", c.Submit(t, k, chain.TxOptions{}, attachMsg(k.Address, a.start, a.end)), "at least one deal id is required")
}

func lastArchived(t *testing.T, c *chain.Chain) int64 {
	t.Helper()
	var r struct {
		LastArchivedHeight chain.Int `json:"last_archived_height"`
	}
	c.Query(t, c.Node(t, 0), &r, "archive", "last-archived-height")
	return r.LastArchivedHeight.Int64()
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
