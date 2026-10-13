// Package metrics holds the number-crunching of a load run: percentiles of inclusion latency and
// the summary a report prints. It has no dependency on the chain.
package metrics

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

const (
	// P50, P90 and P99 are the percentiles every latency summary reports.
	P50 = 50.0
	P90 = 90.0
	P99 = 99.0

	maxPercentile = 100.0
)

// ErrNoSamples is returned for a percentile of an empty sample set.
var ErrNoSamples = errors.New("no samples")

// Percentile returns the p-th percentile of samples, which must be sorted ascending, by the
// nearest-rank method: the smallest sample such that at least p percent of the samples are at or
// below it. p is in (0, 100].
func Percentile(sorted []float64, p float64) (float64, error) {
	if len(sorted) == 0 {
		return 0, ErrNoSamples
	}
	if math.IsNaN(p) || p <= 0 || p > maxPercentile {
		return 0, fmt.Errorf("percentile %v is outside (0, %v]", p, maxPercentile)
	}
	rank := int(math.Ceil(p * float64(len(sorted)) / maxPercentile))
	if rank < 1 {
		rank = 1
	}
	return sorted[rank-1], nil
}

// Summary is the distribution of one set of samples. Every field is zero when Count is zero.
type Summary struct {
	Count int     `json:"count"`
	Min   float64 `json:"min"`
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P99   float64 `json:"p99"`
	Max   float64 `json:"max"`
}

// Summarize sorts a copy of samples and reports their distribution. NaN samples are refused so a
// bad measurement cannot silently poison a percentile.
func Summarize(samples []float64) (Summary, error) {
	if len(samples) == 0 {
		return Summary{}, nil
	}
	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sort.Float64s(sorted)
	if math.IsNaN(sorted[0]) || math.IsNaN(sorted[len(sorted)-1]) {
		return Summary{}, errors.New("a sample is NaN")
	}
	sum := 0.0
	for _, v := range sorted {
		sum += v
	}
	out := Summary{Count: len(sorted), Min: sorted[0], Max: sorted[len(sorted)-1], Mean: sum / float64(len(sorted))}
	for p, dst := range map[float64]*float64{P50: &out.P50, P90: &out.P90, P99: &out.P99} {
		v, err := Percentile(sorted, p)
		if err != nil {
			return Summary{}, fmt.Errorf("percentile %v: %w", p, err)
		}
		*dst = v
	}
	return out, nil
}
