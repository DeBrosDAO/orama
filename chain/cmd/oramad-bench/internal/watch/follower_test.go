package watch

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeBlocks struct {
	latest int64
	blocks map[int64]BlockData
	fail   map[int64]error
}

func (f *fakeBlocks) LatestHeight(context.Context) (int64, error) { return f.latest, nil }

func (f *fakeBlocks) Block(_ context.Context, h int64) (BlockData, error) {
	if err := f.fail[h]; err != nil {
		return BlockData{}, err
	}
	b, ok := f.blocks[h]
	if !ok {
		return BlockData{}, errors.New("no such block")
	}
	return b, nil
}

func block(h int64, txs ...TxResult) BlockData {
	return BlockData{Height: h, Time: t0.Add(time.Duration(h) * time.Second), Txs: txs, GasUsed: int64(10 * len(txs)), GasWanted: int64(20 * len(txs))}
}

func newFollower(src BlockSource, clock *time.Time) *Follower {
	return NewFollower(src, time.Millisecond, func() time.Time { return *clock })
}

func TestFollower_startsAfterTheLatestBlockAndMatchesTheLedger(t *testing.T) {
	src := &fakeBlocks{latest: 5, blocks: map[int64]BlockData{
		5: block(5, TxResult{Hash: "OLD"}),
		6: block(6, TxResult{Hash: "A"}, TxResult{Hash: "B", Code: 11, Log: "out of gas"}, TxResult{Hash: "OTHER"}),
	}}
	now := t0.Add(10 * time.Second)
	f := newFollower(src, &now)
	ledger := NewLedger()
	for _, h := range []string{"A", "B", "OLD"} {
		ledger.Register(h)
		ledger.Sent(h, t0.Add(8*time.Second))
	}
	f.SetLedger(ledger)
	start, err := f.Start(context.Background())
	if err != nil || start != 5 {
		t.Fatalf("start %d, %v", start, err)
	}
	src.latest = 7 // block 6 is read once block 7 exists
	if err := f.catchUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap := ledger.Snapshot()
	if snap["A"].Outcome != Included || snap["A"].Latency() != 2*time.Second || snap["A"].Height != 6 {
		t.Fatalf("A: %+v", snap["A"])
	}
	if snap["B"].Outcome != FailedInBlock || snap["B"].Code != 11 {
		t.Fatalf("B: %+v", snap["B"])
	}
	if snap["OLD"].Outcome != Pending {
		t.Fatal("a block from before the follower started was matched")
	}
	blocks := f.Blocks(t0, t0.Add(time.Hour))
	if len(blocks) != 1 || blocks[0].Height != 6 || blocks[0].Txs != 3 || blocks[0].Failed != 1 || blocks[0].GasUsed != 30 {
		t.Fatalf("blocks %+v", blocks)
	}
}

func TestFollower_catchesUpOverAGapInOrder(t *testing.T) {
	src := &fakeBlocks{latest: 0, blocks: map[int64]BlockData{1: block(1), 2: block(2), 3: block(3)}}
	now := t0
	f := newFollower(src, &now)
	if _, err := f.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var seen []int64
	f.OnBlock(func(b BlockStat) { seen = append(seen, b.Height) })
	src.latest = 4
	if err := f.catchUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 || seen[0] != 1 || seen[2] != 3 {
		t.Fatalf("hook saw %v", seen)
	}
	if err := f.catchUp(context.Background()); err != nil || len(seen) != 3 {
		t.Fatalf("a second catch-up re-read blocks: %v, %v", seen, err)
	}
}

func TestFollower_blocksBetweenFiltersBySightingTime(t *testing.T) {
	src := &fakeBlocks{latest: 0, blocks: map[int64]BlockData{1: block(1), 2: block(2), 3: block(3)}}
	now := t0
	f := newFollower(src, &now)
	if _, err := f.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for h := int64(1); h <= 3; h++ {
		src.latest = h + 1
		now = t0.Add(time.Duration(h) * time.Second)
		if err := f.catchUp(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got := f.Blocks(t0.Add(2*time.Second), t0.Add(3*time.Second))
	if len(got) != 2 || got[0].Height != 2 {
		t.Fatalf("got %+v", got)
	}
}

// A node reports block H as its latest before it has saved H's results; the follower reads H only
// once H+1 exists, so it never asks for results that are not there yet.
func TestFollower_readsABlockOnlyOnceTheNextOneExists(t *testing.T) {
	src := &fakeBlocks{latest: 0, blocks: map[int64]BlockData{1: block(1), 2: block(2)}, fail: map[int64]error{2: errors.New("could not find results for height #2")}}
	now := t0
	f := newFollower(src, &now)
	if _, err := f.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	src.latest = 1
	if err := f.catchUp(context.Background()); err != nil {
		t.Fatalf("the latest block was read before the next one existed: %v", err)
	}
	if len(f.Blocks(t0, t0)) != 0 {
		t.Fatal("block 1 was read while it was the node's latest")
	}
	src.latest = 2
	if err := f.catchUp(context.Background()); err != nil {
		t.Fatalf("block 2, the latest, was asked for: %v", err)
	}
	if got := f.Blocks(t0, t0); len(got) != 1 || got[0].Height != 1 {
		t.Fatalf("got %+v, want block 1 once block 2 exists", got)
	}
}

func TestFollower_aBlockItCannotReadStopsIt(t *testing.T) {
	boom := errors.New("pruned")
	src := &fakeBlocks{latest: 0, blocks: map[int64]BlockData{1: block(1)}, fail: map[int64]error{2: boom}}
	now := t0
	f := newFollower(src, &now)
	if _, err := f.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	src.latest = 3
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := f.Run(ctx)
	if !errors.Is(err, boom) || !errors.Is(f.Err(), boom) {
		t.Fatalf("Run = %v, Err = %v", err, f.Err())
	}
	if len(f.Blocks(t0, t0)) != 1 {
		t.Fatal("block 1 was lost")
	}
}

func TestFollower_runStopsWhenTheContextEnds(t *testing.T) {
	src := &fakeBlocks{blocks: map[int64]BlockData{}}
	now := t0
	f := newFollower(src, &now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}
