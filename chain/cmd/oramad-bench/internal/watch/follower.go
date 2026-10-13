package watch

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultPoll is how often the follower asks for a new block. A latency is measured to the moment
// the follower saw the block, so this is the resolution of every inclusion latency.
const DefaultPoll = 50 * time.Millisecond

// TxResult is the execution result of one transaction of a block.
type TxResult struct {
	Hash string
	Code uint32
	Log  string
}

// BlockData is what the follower needs of a committed block.
type BlockData struct {
	Height int64
	// Time is the block header's time.
	Time      time.Time
	Txs       []TxResult
	GasUsed   int64
	GasWanted int64
}

// BlockSource reads committed blocks from one node.
type BlockSource interface {
	LatestHeight(ctx context.Context) (int64, error)
	Block(ctx context.Context, height int64) (BlockData, error)
}

// BlockStat is a block as the report shows it.
type BlockStat struct {
	Height int64     `json:"height"`
	Time   time.Time `json:"time"`
	// SeenAt is when the follower saw the block.
	SeenAt    time.Time `json:"seen_at"`
	Txs       int       `json:"txs"`
	Failed    int       `json:"failed"`
	GasUsed   int64     `json:"gas_used"`
	GasWanted int64     `json:"gas_wanted"`
}

// Follower reads every new block of one node and tells the current ledger which of its transactions
// the block included.
type Follower struct {
	src    BlockSource
	poll   time.Duration
	now    func() time.Time
	ledger atomic.Pointer[Ledger]

	mu     sync.Mutex
	blocks []BlockStat
	hooks  []func(BlockStat)
	last   int64
	err    error
}

// NewFollower returns a follower of src. now is the clock block sightings are stamped with.
func NewFollower(src BlockSource, poll time.Duration, now func() time.Time) *Follower {
	if poll <= 0 {
		poll = DefaultPoll
	}
	return &Follower{src: src, poll: poll, now: now}
}

// SetLedger makes l the ledger blocks are matched against; nil stops matching.
func (f *Follower) SetLedger(l *Ledger) { f.ledger.Store(l) }

// OnBlock registers fn to be called, in order, for each block after it was matched. fn runs on the
// follower's goroutine, so it must be quick.
func (f *Follower) OnBlock(fn func(BlockStat)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hooks = append(f.hooks, fn)
}

// Err is the error that stopped the follower, if one did.
func (f *Follower) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// Blocks returns the blocks seen so far whose SeenAt is in [from, to].
func (f *Follower) Blocks(from, to time.Time) []BlockStat {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []BlockStat
	for _, b := range f.blocks {
		if !b.SeenAt.Before(from) && !b.SeenAt.After(to) {
			out = append(out, b)
		}
	}
	return out
}

// Start reads the node's latest height and follows from the block after it. It returns that height.
func (f *Follower) Start(ctx context.Context) (int64, error) {
	h, err := f.src.LatestHeight(ctx)
	if err != nil {
		return 0, fmt.Errorf("read the latest height: %w", err)
	}
	f.mu.Lock()
	f.last = h
	f.mu.Unlock()
	return h, nil
}

// Run follows the chain until ctx ends or a block cannot be read; it returns the error that stopped
// it (nil for ctx ending).
func (f *Follower) Run(ctx context.Context) error {
	tick := time.NewTicker(f.poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := f.catchUp(ctx); err != nil {
				f.mu.Lock()
				f.err = err
				f.mu.Unlock()
				return err
			}
		}
	}
}

// catchUp reads every block after the last one seen up to the one before the node's latest. A node
// reports block H as its latest once it has stored it, before it has saved H's execution results
// and committed H's state; both are there once block H+1 exists, so a block is read only then and
// every read of it (its results, the base fee after it) answers the first time.
func (f *Follower) catchUp(ctx context.Context) error {
	latest, err := f.src.LatestHeight(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("read the latest height: %w", err)
	}
	f.mu.Lock()
	next := f.last + 1
	f.mu.Unlock()
	for h := next; h < latest; h++ {
		b, err := f.src.Block(ctx, h)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read block %d: %w", h, err)
		}
		f.record(b)
	}
	return nil
}

func (f *Follower) record(b BlockData) {
	seen := f.now()
	stat := BlockStat{Height: b.Height, Time: b.Time, SeenAt: seen, Txs: len(b.Txs), GasUsed: b.GasUsed, GasWanted: b.GasWanted}
	ledger := f.ledger.Load()
	for _, tx := range b.Txs {
		if tx.Code != 0 {
			stat.Failed++
		}
		if ledger != nil {
			ledger.Included(tx.Hash, seen, b.Height, tx.Code, tx.Log)
		}
	}
	f.mu.Lock()
	f.blocks = append(f.blocks, stat)
	f.last = b.Height
	hooks := append([]func(BlockStat){}, f.hooks...)
	f.mu.Unlock()
	for _, fn := range hooks {
		fn(stat)
	}
}
