//go:build e2e_fleet

package realistic

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// Op is one unit of simulated work: worker is the goroutine, i counts its calls.
type Op func(ctx context.Context, worker, i int) error

// Burst runs total calls of op from workers goroutines as fast as they go
// and returns every observation. It is the throughput measurement.
func Burst(ctx context.Context, workers, total int, op Op) []Sample {
	var s Samples
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				if n := next.Add(1); n > int64(total) || ctx.Err() != nil {
					return
				}
				start := time.Now()
				s.Add(start, op(ctx, w, i))
			}
		}()
	}
	wg.Wait()
	return s.Snapshot()
}

// Paced runs total calls of op from workers goroutines, each worker starting
// its next call interval after the previous one started (never sooner), and
// returns every observation. It is the latency measurement under a request
// rate the caller chose, workers/interval, instead of the fastest rate the
// workers can reach. A call slower than interval starts the next at once.
func Paced(ctx context.Context, workers, total int, interval time.Duration, op Op) []Sample {
	var s Samples
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// eventually.Poll's ticker paces the worker: a call slower than
			// interval delays the next instead of piling calls up.
			i := 0
			_ = eventually.Poll(ctx, interval, maxLoad, "paced calls", func() (bool, error) {
				if n := next.Add(1); n > int64(total) {
					return true, nil
				}
				s.Add(time.Now(), op(ctx, w, i))
				i++
				return false, nil
			})
		}()
	}
	wg.Wait()
	return s.Snapshot()
}

// maxLoad bounds a paced load that nobody stops: longer than any stage.
const maxLoad = 24 * time.Hour

// Load is steady background traffic: every worker calls its op once per
// interval until Stop. The pacing is eventually.Poll's ticker, so a slow call
// delays that worker's next one instead of piling calls up.
type Load struct {
	Samples Samples
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// StartLoad starts workers goroutines calling op every interval.
func StartLoad(parent context.Context, workers int, interval time.Duration, op Op) *Load {
	ctx, cancel := context.WithCancel(parent)
	l := &Load{cancel: cancel}
	for w := range workers {
		l.wg.Add(1)
		go func() {
			defer l.wg.Done()
			i := 0
			_ = eventually.Poll(ctx, interval, maxLoad, "steady load", func() (bool, error) {
				start := time.Now()
				err := op(ctx, w, i)
				if ctx.Err() == nil {
					l.Samples.Add(start, err)
				}
				i++
				return false, nil
			})
		}()
	}
	return l
}

// Stop ends the load, waits for every worker, and returns what it observed.
func (l *Load) Stop() []Sample {
	l.cancel()
	l.wg.Wait()
	return l.Samples.Snapshot()
}
