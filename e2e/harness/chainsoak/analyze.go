package chainsoak

import (
	"fmt"
	"sort"
	"time"
)

// Limits are the thresholds of the analysis. DefaultLimits are the ones the soak runs with.
type Limits struct {
	// StallAfter is how long the chain's head may stay at one height. The chain makes a block about
	// every 5 s, so a head that does not move between samples taken minutes apart is a halt.
	StallAfter time.Duration
	// NodeBehindBlocks and NodeBehindSamples: a validator more than that many blocks behind the head
	// (or catching up) at that many samples in a row is not keeping up.
	NodeBehindBlocks  int64
	NodeBehindSamples int

	// MemoryMinSamples is the fewest samples of a series (after the warm-up and between restarts) the
	// memory check judges: a short series cannot tell a leak from a cache filling.
	MemoryMinSamples int
	// MemoryWarmup is the share of a series, from its start, left out: caches fill and the Go heap
	// settles in the first hours.
	MemoryWarmup float64
	// MemoryGrowthPct and MemoryGrowthFloorMB: growth is only a finding when the late memory is that
	// many percent over the early one and that many MiB over it.
	MemoryGrowthPct     int
	MemoryGrowthFloorMB int
	// MonotonicShare is the share of steps that must not fall (by more than MemoryJitterMB) for a
	// series to count as growing steadily. A collector's sawtooth falls often; a leak does not.
	MonotonicShare float64
	MemoryJitterMB int

	// IndexerLagBlocks and IndexerLagSamples: an indexer that far behind at that many samples in a row
	// cannot keep up with the chain.
	IndexerLagBlocks  int64
	IndexerLagSamples int
	// MempoolStuckTxs and MempoolStuckSamples: a mempool that holds at least that many transactions at
	// that many samples in a row is not draining.
	MempoolStuckTxs     int
	MempoolStuckSamples int
}

// DefaultLimits are the thresholds the chain soak runs with; each is named in the documentation.
func DefaultLimits() Limits {
	return Limits{
		StallAfter:          15 * time.Minute,
		NodeBehindBlocks:    60,
		NodeBehindSamples:   3,
		MemoryMinSamples:    12,
		MemoryWarmup:        0.1,
		MemoryGrowthPct:     25,
		MemoryGrowthFloorMB: 64,
		MonotonicShare:      0.9,
		MemoryJitterMB:      2,
		IndexerLagBlocks:    200,
		IndexerLagSamples:   3,
		MempoolStuckTxs:     500,
		MempoolStuckSamples: 6,
	}
}

// Analyze applies the limits to a timeline and returns everything it holds against the run, in the
// order divergence, stall, nodes behind, memory growth, restarts, indexer lag, mempool.
func Analyze(tl Timeline, lim Limits) []Finding {
	if len(tl.Samples) == 0 {
		return []Finding{{Kind: NoSamples, Detail: "the soak took no sample"}}
	}
	var out []Finding
	out = append(out, divergences(tl)...)
	out = append(out, stalls(tl, lim)...)
	out = append(out, nodesBehind(tl, lim)...)
	out = append(out, memoryGrowth(tl, lim)...)
	out = append(out, restarts(tl)...)
	out = append(out, indexerLag(tl, lim)...)
	out = append(out, mempoolStuck(tl, lim)...)
	return out
}

// divergences compares, at every sample, the hashes the nodes store for the agreed height. Two nodes
// that computed another application state for one block commit another app hash in the next header;
// a node that did not answer (an empty hash) is not compared.
func divergences(tl Timeline) []Finding {
	var out []Finding
	for _, s := range tl.Samples {
		ref, refHash := "", ""
		for _, n := range s.Nodes {
			hash := n.BlockHash + "/" + n.AppHash
			if n.BlockHash == "" || n.AppHash == "" {
				continue
			}
			if ref == "" {
				ref, refHash = n.Name, hash
				continue
			}
			if hash != refHash {
				out = append(out, Finding{Kind: Divergence, Node: n.Name, Detail: fmt.Sprintf(
					"at height %d (sample %s into the soak) %s stores block/app hash %s and %s stores %s",
					s.AgreedHeight, s.Elapsed.Round(time.Second), n.Name, hash, ref, refHash)})
				return out
			}
		}
	}
	return out
}

// head is the highest height any node reports in a sample.
func head(s Sample) int64 {
	var h int64
	for _, n := range s.Nodes {
		h = max(h, n.Height)
	}
	return h
}

// stalls finds a head that did not move for StallAfter. The first sample starts the clock.
func stalls(tl Timeline, lim Limits) []Finding {
	advanced, last := tl.Samples[0].At, head(tl.Samples[0])
	for _, s := range tl.Samples[1:] {
		if h := head(s); h > last {
			advanced, last = s.At, h
			continue
		}
		if s.At.Sub(advanced) > lim.StallAfter {
			return []Finding{{Kind: Stall, Detail: fmt.Sprintf(
				"the chain head stayed at height %d from %s to %s into the soak (more than %s)",
				last, advanced.Sub(tl.Samples[0].At).Round(time.Second), s.At.Sub(tl.Samples[0].At).Round(time.Second), lim.StallAfter)}}
		}
	}
	return nil
}

// nodesBehind finds a validator that fell behind the head, or kept catching up, for several samples.
func nodesBehind(tl Timeline, lim Limits) []Finding {
	streak := map[string]int{}
	reported := map[string]bool{}
	var out []Finding
	for _, s := range tl.Samples {
		h := head(s)
		for _, n := range s.Nodes {
			if h-n.Height <= lim.NodeBehindBlocks && !n.CatchingUp {
				streak[n.Name] = 0
				continue
			}
			streak[n.Name]++
			if streak[n.Name] >= lim.NodeBehindSamples && !reported[n.Name] {
				reported[n.Name] = true
				out = append(out, Finding{Kind: NodeBehind, Node: n.Name, Detail: fmt.Sprintf(
					"%s was at height %d with the head at %d (catching up: %t) at %d samples in a row, the last %s into the soak",
					n.Name, n.Height, h, n.CatchingUp, streak[n.Name], s.Elapsed.Round(time.Second))})
			}
		}
	}
	return out
}

// indexerLag finds an indexer that stayed far behind its node.
func indexerLag(tl Timeline, lim Limits) []Finding {
	return runs(tl, lim.IndexerLagSamples, func(n Node) bool { return n.IndexerLag > lim.IndexerLagBlocks },
		IndexerLag, func(n Node) string {
			return fmt.Sprintf("%s's indexer was %d blocks behind (limit %d)", n.Name, n.IndexerLag, lim.IndexerLagBlocks)
		})
}

// mempoolStuck finds a mempool that did not drain.
func mempoolStuck(tl Timeline, lim Limits) []Finding {
	return runs(tl, lim.MempoolStuckSamples, func(n Node) bool { return n.MempoolTxs >= lim.MempoolStuckTxs },
		MempoolStuck, func(n Node) string {
			return fmt.Sprintf("%s's mempool held %d transactions (limit %d)", n.Name, n.MempoolTxs, lim.MempoolStuckTxs)
		})
}

// runs reports each node once for bad at need samples in a row.
func runs(tl Timeline, need int, bad func(Node) bool, kind Kind, detail func(Node) string) []Finding {
	streak := map[string]int{}
	reported := map[string]bool{}
	var out []Finding
	for _, s := range tl.Samples {
		for _, n := range s.Nodes {
			if !bad(n) {
				streak[n.Name] = 0
				continue
			}
			streak[n.Name]++
			if streak[n.Name] >= need && !reported[n.Name] {
				reported[n.Name] = true
				out = append(out, Finding{Kind: kind, Node: n.Name, Detail: fmt.Sprintf(
					"%s, at %d samples in a row, the last %s into the soak", detail(n), streak[n.Name], s.Elapsed.Round(time.Second))})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}
