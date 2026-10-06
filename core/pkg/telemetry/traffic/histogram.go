package traffic

import (
	"sort"
	"time"
)

// latencyBoundsMs are the inclusive upper bounds, in milliseconds, of the
// latency histogram's buckets. They are roughly log-spaced (x1.5 to x2 per
// step) from 1ms to 30s, so the relative error of an interpolated percentile
// stays about the same at every scale. A sample above the last bound lands
// in the overflow bucket.
var latencyBoundsMs = [...]float64{
	1, 2, 3, 5, 7.5, 10, 15, 25, 40, 60, 100, 150, 250, 400, 600,
	1000, 1500, 2500, 4000, 6000, 10000, 15000, 20000, 30000,
}

// numLatencyBuckets is one bucket per bound plus the overflow bucket.
const numLatencyBuckets = len(latencyBoundsMs) + 1

// histogram counts latency samples per bucket of latencyBoundsMs.
type histogram [numLatencyBuckets]int64

// bucketFor returns the bucket a latency falls in: the first bound it does
// not exceed, or the overflow bucket.
func bucketFor(d time.Duration) int {
	ms := float64(d) / float64(time.Millisecond)
	return sort.SearchFloat64s(latencyBoundsMs[:], ms)
}

func (h *histogram) observe(d time.Duration) {
	h[bucketFor(d)]++
}

func (h *histogram) merge(o *histogram) {
	for i := range h {
		h[i] += o[i]
	}
}

func (h *histogram) count() int64 {
	var n int64
	for _, c := range h {
		n += c
	}
	return n
}

// quantile estimates the q-th quantile (0..1) in milliseconds, interpolating
// linearly inside the bucket the quantile falls in. A quantile that falls in
// the overflow bucket reports the last bound: the histogram knows only that
// those samples took longer than that. An empty histogram reports 0.
func (h *histogram) quantile(q float64) float64 {
	total := h.count()
	if total == 0 {
		return 0
	}
	rank := q * float64(total)
	var cum float64
	for i, c := range h {
		if c == 0 || cum+float64(c) < rank {
			cum += float64(c)
			continue
		}
		if i == len(latencyBoundsMs) {
			return latencyBoundsMs[len(latencyBoundsMs)-1]
		}
		lower := 0.0
		if i > 0 {
			lower = latencyBoundsMs[i-1]
		}
		frac := (rank - cum) / float64(c)
		return lower + frac*(latencyBoundsMs[i]-lower)
	}
	return latencyBoundsMs[len(latencyBoundsMs)-1]
}
