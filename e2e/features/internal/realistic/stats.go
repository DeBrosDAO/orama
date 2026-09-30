//go:build e2e_fleet

package realistic

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// Sample is one observed operation.
type Sample struct {
	At       time.Time
	Duration time.Duration
	Err      error
}

// Samples collects observations from many goroutines.
type Samples struct {
	mu   sync.Mutex
	list []Sample
}

// Add records one operation that started at start.
func (s *Samples) Add(start time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, Sample{At: start, Duration: time.Since(start), Err: err})
}

// Snapshot is a copy of what was recorded so far.
func (s *Samples) Snapshot() []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Sample(nil), s.list...)
}

// Summary is the recorded shape of one metric: what a baseline file stores
// and a later run compares against.
type Summary struct {
	Name       string  `json:"name"`
	Count      int     `json:"count"`
	Errors     int     `json:"errors"`
	ErrorRate  float64 `json:"error_rate"`
	P50MS      float64 `json:"p50_ms"`
	P95MS      float64 `json:"p95_ms"`
	P99MS      float64 `json:"p99_ms"`
	MaxMS      float64 `json:"max_ms"`
	Throughput float64 `json:"throughput_per_sec"`
	FirstError string  `json:"first_error,omitempty"`
}

// Summarize computes a Summary of the samples that satisfy keep (all when nil).
// Latencies are over successful operations only; throughput is successes per
// second over the span from the first start to the last finish.
func Summarize(name string, samples []Sample, keep func(Sample) bool) Summary {
	sum := Summary{Name: name}
	var ok []time.Duration
	var first, last time.Time
	for _, s := range samples {
		if keep != nil && !keep(s) {
			continue
		}
		sum.Count++
		if first.IsZero() || s.At.Before(first) {
			first = s.At
		}
		if end := s.At.Add(s.Duration); end.After(last) {
			last = end
		}
		if s.Err != nil {
			sum.Errors++
			if sum.FirstError == "" {
				sum.FirstError = s.Err.Error()
			}
			continue
		}
		ok = append(ok, s.Duration)
	}
	if sum.Count > 0 {
		sum.ErrorRate = float64(sum.Errors) / float64(sum.Count)
	}
	sort.Slice(ok, func(i, j int) bool { return ok[i] < ok[j] })
	sum.P50MS, sum.P95MS, sum.P99MS = ms(percentile(ok, 50)), ms(percentile(ok, 95)), ms(percentile(ok, 99))
	if len(ok) > 0 {
		sum.MaxMS = ms(ok[len(ok)-1])
	}
	if span := last.Sub(first).Seconds(); span > 0 {
		sum.Throughput = float64(len(ok)) / span
	}
	return sum
}

// percentile is the nearest-rank percentile of sorted durations.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	return sorted[min(max(rank, 0), len(sorted)-1)]
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// String is the one-line form for failure messages.
func (s Summary) String() string {
	return fmt.Sprintf("%s: n=%d errors=%d (%.2f%%) p50=%.1fms p95=%.1fms p99=%.1fms max=%.1fms %.1f/s",
		s.Name, s.Count, s.Errors, 100*s.ErrorRate, s.P50MS, s.P95MS, s.P99MS, s.MaxMS, s.Throughput)
}
