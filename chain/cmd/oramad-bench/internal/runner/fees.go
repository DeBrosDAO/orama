package runner

import (
	"context"
	"sync/atomic"

	"github.com/DeBrosOfficial/network/chain/client/node"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/watch"
	feestypes "github.com/DeBrosOfficial/network/chain/x/fees/types"
)

const (
	// feeQueue is how many blocks may wait for their base fee to be read.
	feeQueue = 1024
)

// feeRecorder reads x/fees' base fee after every block the follower sees. It does so off the
// follower's goroutine: a slow query must not delay the follower's next sighting of a block, which
// is the moment every inclusion latency is measured to.
type feeRecorder struct {
	client  *node.Client
	series  *watch.FeeSeries
	blocks  chan watch.BlockStat
	failed  atomic.Int64
	dropped atomic.Int64
}

func newFeeRecorder(client *node.Client, series *watch.FeeSeries) *feeRecorder {
	return &feeRecorder{client: client, series: series, blocks: make(chan watch.BlockStat, feeQueue)}
}

// OnBlock is the follower's hook. It never blocks: a full queue drops the block and counts it.
func (f *feeRecorder) OnBlock(b watch.BlockStat) {
	select {
	case f.blocks <- b:
	default:
		f.dropped.Add(1)
	}
}

// Run reads the fee of each queued block until ctx ends.
func (f *feeRecorder) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-f.blocks:
			f.record(ctx, b)
		}
	}
}

// record reads the base fee after block b. The follower hands a block on once the next one exists,
// so the state at its height is committed and the read answers; a failed read is a missed block.
func (f *feeRecorder) record(ctx context.Context, b watch.BlockStat) {
	var resp feestypes.QueryBaseFeeResponse
	if err := f.client.QueryAt(ctx, b.Height, "/orama.fees.v1.Query/BaseFee", &feestypes.QueryBaseFeeRequest{}, &resp); err != nil {
		f.failed.Add(1)
		return
	}
	f.series.Add(watch.FeeSample{Height: b.Height, At: b.SeenAt, BaseFee: resp.BaseFee.String()})
}

// Missed is how many blocks have no base fee sample.
func (f *feeRecorder) Missed() int64 { return f.failed.Load() + f.dropped.Load() }
