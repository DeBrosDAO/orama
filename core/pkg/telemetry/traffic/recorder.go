// Package traffic keeps live, in-memory request metrics for one gateway: a
// rolling window of per-second buckets from which a report.TrafficReport
// (rates, error rate, latency percentiles, busiest namespaces) is computed on
// demand. Nothing is persisted; a restarted gateway starts from zero.
package traffic

import (
	"net/http"
	"sync"
	"time"
)

const (
	// WindowSeconds is the span a snapshot covers, in one-second buckets.
	WindowSeconds = 60
	// MaxNamespacesPerSecond caps the distinct namespaces one second tracks;
	// the rest of that second's traffic is folded into OtherLabel. With the
	// window this bounds memory at WindowSeconds*(MaxNamespacesPerSecond+1)
	// namespace entries however many names clients make up.
	MaxNamespacesPerSecond = 256
	// TopNamespaces is how many namespaces a snapshot lists, busiest first.
	TopNamespaces = 20
)

// nsStats is one namespace's share of a second.
type nsStats struct {
	requests  int64
	errors5xx int64
	latency   histogram
}

// second is one bucket of the ring: everything observed during one second
// since the recorder's epoch.
type second struct {
	// sec is the second since the recorder's epoch this bucket holds, or -1
	// while it has never been used.
	sec        int64
	requests   int64
	errors4xx  int64
	errors5xx  int64
	bytes      int64
	latency    histogram
	namespaces map[string]*nsStats
}

// Recorder is the live request metrics of one gateway.
//
// One mutex guards the whole ring. Observe holds it for a handful of
// increments and one map lookup (under 100ns uncontended and about 220ns
// with 8 cores contending, see BenchmarkObserve*), so one recorder sustains
// millions of requests a second while the request it records costs a
// millisecond or more; contention stays negligible well past 10x today's
// per-gateway traffic. Sharding would
// make every Snapshot merge the shards and buys nothing at these rates.
type Recorder struct {
	now   func() time.Time
	epoch time.Time

	mu        sync.Mutex
	ring      [WindowSeconds]second
	firstSeen time.Time
	seen      bool
	total     int64
}

// New returns an empty Recorder that reads the time from now (time.Now when
// nil). Buckets are indexed by the time elapsed since New, which uses the
// monotonic clock when now is time.Now, so a wall-clock step cannot scramble
// the window.
func New(now func() time.Time) *Recorder {
	if now == nil {
		now = time.Now
	}
	r := &Recorder{now: now, epoch: now()}
	for i := range r.ring {
		r.ring[i].sec = -1
	}
	return r
}

// Observe records one finished request: the namespace it served (validated
// and capped, see sanitizeLabel and MaxNamespacesPerSecond), its status,
// the bytes written and how long it took.
func (r *Recorder) Observe(namespace string, status int, bytes int64, dur time.Duration) {
	r.observe(namespace, status, bytes, dur, true)
}

// ObserveStream records a request that became a long-lived stream, such as a
// WebSocket. It counts toward requests, errors and bytes but adds no latency
// sample: its duration is how long the client stayed connected, and it would
// push every percentile into the overflow bucket.
func (r *Recorder) ObserveStream(namespace string, status int, bytes int64) {
	r.observe(namespace, status, bytes, 0, false)
}

func (r *Recorder) observe(namespace string, status int, bytes int64, dur time.Duration, timed bool) {
	label := sanitizeLabel(namespace)
	now := r.now()

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.seen {
		r.seen, r.firstSeen = true, now
	}
	r.total++

	b := r.bucket(r.secondAt(now))
	b.requests++
	b.bytes += bytes
	is5xx := status >= http.StatusInternalServerError
	switch {
	case is5xx:
		b.errors5xx++
	case status >= http.StatusBadRequest:
		b.errors4xx++
	}

	ns := b.namespace(label)
	ns.requests++
	if is5xx {
		ns.errors5xx++
	}
	if timed {
		b.latency.observe(dur)
		ns.latency.observe(dur)
	}
}

// secondAt is the second since the epoch that t falls in. Times before the
// epoch count as second 0.
func (r *Recorder) secondAt(t time.Time) int64 {
	elapsed := t.Sub(r.epoch)
	if elapsed < 0 {
		return 0
	}
	return int64(elapsed / time.Second)
}

// bucket returns the ring bucket for sec, emptying it first when it still
// holds an older second (the ring wrapped, or it sat idle past the window).
func (r *Recorder) bucket(sec int64) *second {
	b := &r.ring[sec%WindowSeconds]
	if b.sec == sec {
		return b
	}
	namespaces := b.namespaces
	clear(namespaces)
	*b = second{sec: sec, namespaces: namespaces}
	if b.namespaces == nil {
		b.namespaces = make(map[string]*nsStats)
	}
	return b
}

// namespace returns this second's stats for label, folding it into
// OtherLabel once MaxNamespacesPerSecond distinct labels are tracked.
func (b *second) namespace(label string) *nsStats {
	if ns, ok := b.namespaces[label]; ok {
		return ns
	}
	if len(b.namespaces) >= MaxNamespacesPerSecond {
		label = OtherLabel
		if ns, ok := b.namespaces[label]; ok {
			return ns
		}
	}
	ns := &nsStats{}
	b.namespaces[label] = ns
	return ns
}
