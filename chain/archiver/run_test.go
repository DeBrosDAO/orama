package archiver

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	cmttypes "github.com/cometbft/cometbft/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

type fakeChain struct {
	blocks  map[int64]Block
	tip     int64
	ranges  map[[2]int64]types.RangeRecord
	submits int
	fail    error
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
	c := &fakeChain{blocks: map[int64]Block{}, ranges: map[[2]int64]types.RangeRecord{}}
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
