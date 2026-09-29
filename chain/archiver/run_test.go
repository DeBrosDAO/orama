package archiver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	cmttypes "github.com/cometbft/cometbft/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/piece"
	"github.com/DeBrosOfficial/network/chain/x/archive/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

type fakeChain struct {
	blocks  map[int64]Block
	tip     int64
	ranges  map[[2]int64]types.RangeRecord
	submits int
	fail    error
	// deals is x/storage's deal table; pending counts the deals each range start waits on the way
	// x/archive does, and lastArchived is the chain's archived prefix.
	deals        map[uint64]storagetypes.DealStatus
	pending      map[int64]int
	lastArchived int64
	attaches     int
}

func realBlock(t *testing.T, h int64) Block {
	t.Helper()
	b := cmttypes.MakeBlock(h, []cmttypes.Tx{cmttypes.Tx([]byte{byte(h)})}, &cmttypes.Commit{}, nil)
	b.ChainID = "orama-test-1"
	b.ValidatorsHash = make([]byte, 32)
	b.ProposerAddress = make([]byte, 20)
	b.Time = time.Unix(1_700_000_000+h, 0).UTC()
	pb, err := b.ToProto()
	require.NoError(t, err)
	body, err := pb.Marshal()
	require.NoError(t, err)
	return Block{Height: h, Hash: b.Hash(), Proto: body}
}

func newFakeChain(t *testing.T, tip int64) *fakeChain {
	c := &fakeChain{
		blocks: map[int64]Block{}, ranges: map[[2]int64]types.RangeRecord{},
		deals: map[uint64]storagetypes.DealStatus{}, pending: map[int64]int{},
	}
	c.grow(t, tip)
	return c
}

func (c *fakeChain) grow(t *testing.T, tip int64) {
	for h := c.tip + 1; h <= tip; h++ {
		c.blocks[h] = realBlock(t, h)
	}
	c.tip = tip
}

func (c *fakeChain) LatestHeight(context.Context) (int64, error) { return c.tip, nil }
func (c *fakeChain) Block(_ context.Context, h int64) (Block, error) {
	b, ok := c.blocks[h]
	if !ok {
		return Block{}, errors.New("no block")
	}
	return b, nil
}
func (c *fakeChain) Range(_ context.Context, s, e int64) (types.RangeRecord, bool, error) {
	r, ok := c.ranges[[2]int64{s, e}]
	return r, ok, nil
}
func (c *fakeChain) Submit(_ context.Context, msgs ...sdk.Msg) error {
	if c.fail != nil {
		err := c.fail
		c.fail = nil
		return err
	}
	for _, m := range msgs {
		if at, ok := m.(*types.MsgAttachReplicas); ok {
			c.attach(at)
			continue
		}
		a := m.(*types.MsgAttest)
		rec := c.ranges[[2]int64{a.StartHeight, a.EndHeight}]
		rec.StartHeight, rec.EndHeight, rec.BundleCid = a.StartHeight, a.EndHeight, a.BundleCid
		rec.BundleHash, rec.MerkleRoot = a.BundleHash, a.MerkleRoot
		rec.Archivers = append(rec.Archivers, a.Archiver)
		c.ranges[[2]int64{a.StartHeight, a.EndHeight}] = rec
		c.submits++
	}
	return nil
}

// CreateArchiveDeal mirrors x/archive: a range holds at most MaxLiveDealsPerRange live deals,
// recorded or waiting, and a new deal is OPEN.
func (c *fakeChain) CreateArchiveDeal(_ context.Context, msg *types.MsgCreateArchiveDeal) (uint64, error) {
	key := [2]int64{msg.StartHeight, msg.EndHeight}
	rec := c.ranges[key]
	live := c.pending[msg.StartHeight]
	for _, id := range rec.DealIds {
		var n uint64
		fmt.Sscanf(id, "%d", &n)
		if c.deals[n] == storagetypes.DealStatus_DEAL_STATUS_ACTIVE {
			live++
		}
	}
	if live >= types.MaxLiveDealsPerRange {
		return 0, fmt.Errorf("transaction failed: %w", types.ErrDealsFull)
	}
	id := uint64(len(c.deals) + 1)
	c.deals[id] = storagetypes.DealStatus_DEAL_STATUS_OPEN
	c.pending[msg.StartHeight]++
	return id, nil
}

func (c *fakeChain) attach(msg *types.MsgAttachReplicas) {
	key := [2]int64{msg.StartHeight, msg.EndHeight}
	rec := c.ranges[key]
	rec.DealIds = append(rec.DealIds, msg.DealIds...)
	c.pending[msg.StartHeight] -= len(msg.DealIds)
	rec.Archived = len(rec.DealIds) >= types.MinReplicaDeals
	c.ranges[key] = rec
	if rec.Archived {
		c.lastArchived = max(c.lastArchived, msg.EndHeight)
	}
	c.attaches++
}

func (c *fakeChain) DealStatus(_ context.Context, id uint64) (storagetypes.DealStatus, bool, error) {
	st, ok := c.deals[id]
	return st, ok, nil
}

func (c *fakeChain) LastArchivedHeight(context.Context) (int64, error) { return c.lastArchived, nil }

// assign gives every OPEN deal its first provider, as x/storage does in the next block.
func (c *fakeChain) assign() {
	for id, st := range c.deals {
		if st == storagetypes.DealStatus_DEAL_STATUS_OPEN {
			c.deals[id] = storagetypes.DealStatus_DEAL_STATUS_ACTIVE
		}
	}
}

func TestRunner_attestsFinalisedRangesAndResumesAfterRestart(t *testing.T) {
	chain := newFakeChain(t, 25)
	dir := t.TempDir()
	r, err := NewRunner(chain, "orama1archiver", "node-1", dir, 10)
	require.NoError(t, err)
	n, err := r.Step(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, n, "1-10 and 11-20; 21-30 is not finalised")
	cursor, err := LoadCursor(r.cursorPath())
	require.NoError(t, err)
	require.Equal(t, int64(20), cursor)

	restarted, err := NewRunner(chain, "orama1archiver", "node-1", dir, 10)
	require.NoError(t, err)
	n, err = restarted.Step(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 2, chain.submits)

	chain.grow(t, 31)
	n, err = restarted.Step(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	for _, rng := range [][2]int64{{1, 10}, {11, 20}, {21, 30}} {
		body, err := os.ReadFile(BundlePath(dir, rng[0], rng[1]))
		require.NoError(t, err)
		rec := chain.ranges[rng]
		require.Equal(t, CID(body), rec.BundleCid)
		blocks, err := Verify(body, rec)
		require.NoError(t, err)
		require.Len(t, blocks, 10)
	}
}

func TestRunner_retriesADroppedAttestationAndStopsOnAConflictingRoot(t *testing.T) {
	chain := newFakeChain(t, 12)
	dir := t.TempDir()
	r, err := NewRunner(chain, "orama1archiver", "node-1", dir, 10)
	require.NoError(t, err)
	chain.fail = errors.New("not included")
	_, err = r.Step(context.Background())
	require.Error(t, err)
	cursor, _ := LoadCursor(r.cursorPath())
	require.Zero(t, cursor, "a dropped attestation does not move the cursor")
	n, err := r.Step(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	other := newFakeChain(t, 22)
	other.ranges[[2]int64{1, 10}] = types.RangeRecord{StartHeight: 1, EndHeight: 10, MerkleRoot: make([]byte, 32)}
	dir2 := t.TempDir()
	r2, err := NewRunner(other, "orama1archiver", "node-1", dir2, 10)
	require.NoError(t, err)
	n, err = r2.Step(context.Background())
	require.ErrorIs(t, err, ErrRootConflict)
	require.Equal(t, 1, n, "the range after the conflict is still attested")
	require.Equal(t, 1, other.submits, "the conflicting range is not attested")
	_, statErr := os.Stat(ConflictPath(dir2, 1, 10))
	require.NoError(t, statErr, "the conflict is recorded")
	cursor, _ = LoadCursor(r2.cursorPath())
	require.Equal(t, int64(20), cursor)
}

func TestRunner_skipsARangeItAlreadyAttested(t *testing.T) {
	chain := newFakeChain(t, 12)
	r, err := NewRunner(chain, "orama1a", "node-1", t.TempDir(), 10)
	require.NoError(t, err)
	_, err = r.Step(context.Background())
	require.NoError(t, err)
	require.NoError(t, SaveCursor(r.cursorPath(), 0))
	_, err = r.Step(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, chain.submits, "the same archiver does not attest twice")
	require.NoError(t, SaveCursor(r.cursorPath(), 5))
	_, err = r.Step(context.Background())
	require.ErrorContains(t, err, "range boundary")
}

func TestVerify_refusesTamperedTruncatedAndMismatchedBundles(t *testing.T) {
	chain := newFakeChain(t, 12)
	dir := t.TempDir()
	r, err := NewRunner(chain, "orama1archiver", "node-1", dir, 10)
	require.NoError(t, err)
	_, err = r.Step(context.Background())
	require.NoError(t, err)
	body, err := os.ReadFile(BundlePath(dir, 1, 10))
	require.NoError(t, err)
	rec := chain.ranges[[2]int64{1, 10}]

	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-3] ^= 0xff
	_, err = Verify(tampered, rec)
	require.ErrorIs(t, err, ErrBundle)
	_, err = Verify(body[:len(body)-1], rec)
	require.ErrorIs(t, err, ErrBundle)
	_, err = Verify(append(append([]byte(nil), body...), 0), rec)
	require.ErrorIs(t, err, ErrBundle)

	wrong := rec
	wrong.MerkleRoot = make([]byte, 32)
	_, err = Verify(body, wrong)
	require.ErrorIs(t, err, ErrBundle)
	shifted := rec
	shifted.StartHeight = 2
	_, err = Verify(body, shifted)
	require.ErrorIs(t, err, ErrBundle)
	_, err = Decode([]byte("ORBX"))
	require.ErrorIs(t, err, ErrBundle)
	empty := append([]byte("ORBH\x01"), make([]byte, 12)...)
	empty[12] = 1
	_, err = Decode(empty)
	require.ErrorIs(t, err, ErrBundle, "a zero-block bundle is refused")
}

func TestEncode_refusesGapsAndEmptyRanges(t *testing.T) {
	_, err := Encode(nil)
	require.Error(t, err)
	a, b := realBlock(t, 1), realBlock(t, 3)
	_, err = Encode([]Block{a, b})
	require.ErrorContains(t, err, "consecutive")
}

func TestNewRunner_needsANodeID(t *testing.T) {
	_, err := NewRunner(newFakeChain(t, 1), "orama1a", "", t.TempDir(), 10)
	require.ErrorContains(t, err, "node id")
}

func stepOnce(t *testing.T, r *Runner) {
	t.Helper()
	_, err := r.Step(context.Background())
	require.NoError(t, err)
}

func TestRunner_opensThreeArchiveDealsThenRecordsThemOnceTheyHaveProviders(t *testing.T) {
	chain := newFakeChain(t, 12)
	dir := t.TempDir()
	r, err := NewRunner(chain, "orama1archiver", "node-1", dir, 10)
	require.NoError(t, err)

	stepOnce(t, r)
	require.Len(t, chain.deals, types.MaxLiveDealsPerRange, "the range gets the deals it needs and no more")
	require.Zero(t, chain.attaches, "an OPEN deal has no provider yet and cannot be recorded")
	require.False(t, chain.ranges[[2]int64{1, 10}].Archived)

	stepOnce(t, r)
	require.Len(t, chain.deals, types.MaxLiveDealsPerRange, "a second pass does not open more deals")

	chain.assign()
	stepOnce(t, r)
	require.Equal(t, 1, chain.attaches)
	require.Len(t, chain.ranges[[2]int64{1, 10}].DealIds, types.MinReplicaDeals)
	require.True(t, chain.ranges[[2]int64{1, 10}].Archived)

	stepOnce(t, r)
	_, statErr := os.Stat(dealStatePath(dir, 1, 10))
	require.True(t, os.IsNotExist(statErr), "an archived range is no longer followed")
	require.Len(t, chain.deals, types.MinReplicaDeals)
	require.Equal(t, 1, chain.attaches)
}

func TestRunner_commitsTheBundleFileNotTheBlocks(t *testing.T) {
	chain := newFakeChain(t, 12)
	dir := t.TempDir()
	var got *types.MsgCreateArchiveDeal
	wrapped := &recordingChain{fakeChain: chain, seen: func(m *types.MsgCreateArchiveDeal) { got = m }}
	r, err := NewRunner(wrapped, "orama1archiver", "node-1", dir, 10)
	require.NoError(t, err)
	stepOnce(t, r)

	body, err := os.ReadFile(BundlePath(dir, 1, 10))
	require.NoError(t, err)
	commitment, err := piece.Commit(body)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, commitment.Root, got.PieceRoot)
	require.Equal(t, uint64(len(body)), got.PieceBytes)
	require.Equal(t, int64(1), got.StartHeight)
	require.Equal(t, int64(10), got.EndHeight)
}

type recordingChain struct {
	*fakeChain
	seen func(*types.MsgCreateArchiveDeal)
}

func (c *recordingChain) CreateArchiveDeal(ctx context.Context, m *types.MsgCreateArchiveDeal) (uint64, error) {
	c.seen(m)
	return c.fakeChain.CreateArchiveDeal(ctx, m)
}

func TestRunner_aRefusedExtraDealIsNotAnError(t *testing.T) {
	chain := newFakeChain(t, 12)
	first, err := NewRunner(chain, "orama1a", "node-1", t.TempDir(), 10)
	require.NoError(t, err)
	second, err := NewRunner(chain, "orama1b", "node-2", t.TempDir(), 10)
	require.NoError(t, err)
	stepOnce(t, first)
	stepOnce(t, second)
	require.Len(t, chain.deals, types.MaxLiveDealsPerRange, "two archivers of one range open its deals once between them")
}

func TestRunner_replacesADealThatEndedWithoutAProvider(t *testing.T) {
	chain := newFakeChain(t, 12)
	r, err := NewRunner(chain, "orama1archiver", "node-1", t.TempDir(), 10)
	require.NoError(t, err)
	stepOnce(t, r)
	chain.deals[1] = storagetypes.DealStatus_DEAL_STATUS_EXPIRED
	chain.pending[1]--
	stepOnce(t, r)
	require.Len(t, chain.deals, types.MaxLiveDealsPerRange+1, "the ended deal is replaced")
}

func TestRunner_restartFollowsTheSameDeals(t *testing.T) {
	chain := newFakeChain(t, 12)
	dir := t.TempDir()
	r, err := NewRunner(chain, "orama1archiver", "node-1", dir, 10)
	require.NoError(t, err)
	stepOnce(t, r)
	restarted, err := NewRunner(chain, "orama1archiver", "node-1", dir, 10)
	require.NoError(t, err)
	chain.assign()
	stepOnce(t, restarted)
	require.Len(t, chain.deals, types.MaxLiveDealsPerRange)
	require.True(t, chain.ranges[[2]int64{1, 10}].Archived)
}

func TestRunner_monitorReportsProgressAndTheRetainLag(t *testing.T) {
	chain := newFakeChain(t, 25)
	dir := t.TempDir()
	r, err := NewRunner(chain, "orama1archiver", "node-1", dir, 10)
	require.NoError(t, err)
	stepOnce(t, r)

	read := func() Monitor {
		body, err := os.ReadFile(filepath.Join(dir, "monitor.json"))
		require.NoError(t, err)
		var m Monitor
		require.NoError(t, json.Unmarshal(body, &m))
		return m
	}
	m := read()
	require.Equal(t, int64(20), m.AttestedHeight)
	require.Equal(t, int64(25), m.TipHeight)
	require.Zero(t, m.LastArchivedHeight)
	require.Equal(t, int64(25), m.RetainLagBlocks, "nothing is archived, so the whole tip is unpruneable")
	require.Equal(t, 2, m.UnarchivedRanges)
	require.Equal(t, uint64(6), m.DealsOpened)

	chain.assign()
	stepOnce(t, r)
	m = read()
	require.Equal(t, int64(20), m.LastArchivedHeight)
	require.Equal(t, int64(5), m.RetainLagBlocks)
	require.Zero(t, m.UnarchivedRanges)
}
