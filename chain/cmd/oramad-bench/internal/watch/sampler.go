package watch

import (
	"context"
	"sync"
	"time"
)

// DefaultSampleEvery is how often the mempool of each node is read.
const DefaultSampleEvery = 500 * time.Millisecond

// MempoolReader reads how many transactions one node's mempool holds.
type MempoolReader interface {
	Unconfirmed(ctx context.Context) (txs int, bytes int64, err error)
}

// MempoolSample is one reading of one node's mempool.
type MempoolSample struct {
	T     time.Time `json:"t"`
	Node  int       `json:"node"`
	Txs   int       `json:"txs"`
	Bytes int64     `json:"bytes"`
}

// Sampler reads every node's mempool on a fixed period.
type Sampler struct {
	readers []MempoolReader
	every   time.Duration
	now     func() time.Time

	mu      sync.Mutex
	samples []MempoolSample
	errs    int
}

// NewSampler returns a sampler of the given nodes' mempools.
func NewSampler(readers []MempoolReader, every time.Duration, now func() time.Time) *Sampler {
	if every <= 0 {
		every = DefaultSampleEvery
	}
	return &Sampler{readers: readers, every: every, now: now}
}

// Run samples until ctx ends.
func (s *Sampler) Run(ctx context.Context) {
	tick := time.NewTicker(s.every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.sample(ctx)
		}
	}
}

func (s *Sampler) sample(ctx context.Context) {
	at := s.now()
	for i, r := range s.readers {
		txs, bytes, err := r.Unconfirmed(ctx)
		s.mu.Lock()
		if err != nil {
			s.errs++
		} else {
			s.samples = append(s.samples, MempoolSample{T: at, Node: i, Txs: txs, Bytes: bytes})
		}
		s.mu.Unlock()
	}
}

// Between returns the samples taken in [from, to].
func (s *Sampler) Between(from, to time.Time) []MempoolSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []MempoolSample
	for _, m := range s.samples {
		if !m.T.Before(from) && !m.T.After(to) {
			out = append(out, m)
		}
	}
	return out
}

// Failed is how many readings could not be taken.
func (s *Sampler) Failed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errs
}

// PeakTxs is the largest mempool any node reported.
func PeakTxs(samples []MempoolSample) int {
	peak := 0
	for _, m := range samples {
		peak = max(peak, m.Txs)
	}
	return peak
}

// FeeSample is the base fee in force after a block.
type FeeSample struct {
	Height int64     `json:"height"`
	At     time.Time `json:"at"`
	// BaseFee is norama per gas unit, as the chain reports it.
	BaseFee string `json:"base_fee"`
}

// FeeSeries collects base fee samples.
type FeeSeries struct {
	mu      sync.Mutex
	samples []FeeSample
}

// Add appends a sample.
func (s *FeeSeries) Add(f FeeSample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, f)
}

// LastBefore returns the latest sample taken before t, and whether there is one. A step shorter
// than a block has no sample of its own; the base fee in force when it began is the last one before.
func (s *FeeSeries) LastBefore(t time.Time) (FeeSample, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.samples) - 1; i >= 0; i-- {
		if s.samples[i].At.Before(t) {
			return s.samples[i], true
		}
	}
	return FeeSample{}, false
}

// Between returns the samples taken in [from, to].
func (s *FeeSeries) Between(from, to time.Time) []FeeSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []FeeSample
	for _, f := range s.samples {
		if !f.At.Before(from) && !f.At.After(to) {
			out = append(out, f)
		}
	}
	return out
}
