package chainsoak

import (
	"time"
)

// SeriesSummary is how one service moved over the run.
type SeriesSummary struct {
	FirstMB int `json:"first_mb"`
	LastMB  int `json:"last_mb"`
	MinMB   int `json:"min_mb"`
	MaxMB   int `json:"max_mb"`
	// SlopeMBPerHour is the least-squares slope of the memory over the run's time.
	SlopeMBPerHour float64 `json:"slope_mb_per_hour"`
	// DataGrowthBytesPerDay is the growth of the daemon's state directory, scaled to a day.
	DataGrowthBytesPerDay int64 `json:"data_growth_bytes_per_day"`
}

// Summary is the run in numbers: what the timeline file leads with.
type Summary struct {
	Samples         int                      `json:"samples"`
	Elapsed         time.Duration            `json:"elapsed"`
	FirstHeight     int64                    `json:"first_height"`
	LastHeight      int64                    `json:"last_height"`
	Blocks          int64                    `json:"blocks"`
	BlockIntervalS  float64                  `json:"mean_block_interval_seconds"`
	TransfersSent   int                      `json:"transfers_sent"`
	MaxMempoolTxs   int                      `json:"max_mempool_txs"`
	MaxIndexerLag   int64                    `json:"max_indexer_lag_blocks"`
	Services        map[string]SeriesSummary `json:"services"`
	FindingsPerKind map[Kind]int             `json:"findings_per_kind,omitempty"`
}

// Summarize reduces a timeline.
func Summarize(tl Timeline, findings []Finding) Summary {
	s := Summary{Samples: len(tl.Samples), Services: map[string]SeriesSummary{}, FindingsPerKind: map[Kind]int{}}
	for _, f := range findings {
		s.FindingsPerKind[f.Kind]++
	}
	if len(tl.Samples) == 0 {
		return s
	}
	first, last := tl.Samples[0], tl.Samples[len(tl.Samples)-1]
	s.Elapsed = last.At.Sub(first.At)
	s.FirstHeight, s.LastHeight = head(first), head(last)
	s.Blocks = s.LastHeight - s.FirstHeight
	if s.Blocks > 0 {
		s.BlockIntervalS = s.Elapsed.Seconds() / float64(s.Blocks)
	}
	s.TransfersSent = last.Sent
	for _, smp := range tl.Samples {
		for _, n := range smp.Nodes {
			s.MaxMempoolTxs, s.MaxIndexerLag = max(s.MaxMempoolTxs, n.MempoolTxs), max(s.MaxIndexerLag, n.IndexerLag)
		}
	}
	for key, pts := range series(tl) {
		s.Services[key] = summarizeService(pts)
	}
	return s
}

// summarizeService reduces one node/unit series.
func summarizeService(pts []point) SeriesSummary {
	first, last := pts[0], pts[len(pts)-1]
	out := SeriesSummary{FirstMB: first.svc.AnonMB, LastMB: last.svc.AnonMB, MinMB: first.svc.AnonMB, MaxMB: first.svc.AnonMB}
	xs, ys := make([]float64, 0, len(pts)), make([]float64, 0, len(pts))
	for _, p := range pts {
		out.MinMB, out.MaxMB = min(out.MinMB, p.svc.AnonMB), max(out.MaxMB, p.svc.AnonMB)
		xs, ys = append(xs, p.at.Sub(first.at).Hours()), append(ys, float64(p.svc.AnonMB))
	}
	out.SlopeMBPerHour = slope(xs, ys)
	if span := last.at.Sub(first.at); span > 0 {
		out.DataGrowthBytesPerDay = int64(float64(last.svc.DataBytes-first.svc.DataBytes) * (24 * time.Hour).Seconds() / span.Seconds())
	}
	return out
}

// slope is the least-squares slope of ys over xs; zero for fewer than two points or no spread in xs.
func slope(xs, ys []float64) float64 {
	n := float64(len(xs))
	if n < 2 {
		return 0
	}
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx, sy, sxx, sxy = sx+xs[i], sy+ys[i], sxx+xs[i]*xs[i], sxy+xs[i]*ys[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / den
}
