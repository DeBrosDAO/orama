package chainsoak

import (
	"fmt"
	"sort"
	"time"
)

// point is one reading of a service.
type point struct {
	at  time.Time
	svc Service
}

// series collects the readings of every node and service, in sample order.
func series(tl Timeline) map[string][]point {
	out := map[string][]point{}
	for _, s := range tl.Samples {
		for _, n := range s.Nodes {
			for unit, svc := range n.Services {
				key := n.Name + "/" + unit
				out[key] = append(out[key], point{at: s.At, svc: svc})
			}
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// segments cuts a series where the service restarted: its memory starts over, and a rise after a
// restart is the daemon filling, not a leak across the restart.
func segments(pts []point) [][]int {
	var out [][]int
	var cur []int
	for i, p := range pts {
		if i > 0 && p.svc.Restarts != pts[i-1].svc.Restarts && len(cur) > 0 {
			out, cur = append(out, cur), nil
		}
		cur = append(cur, p.svc.AnonMB)
	}
	return append(out, cur)
}

// memoryGrowth finds a service whose memory only rises, after the warm-up, by more than the limits.
func memoryGrowth(tl Timeline, lim Limits) []Finding {
	all := series(tl)
	var out []Finding
	for _, key := range sortedKeys(all) {
		for _, seg := range segments(all[key]) {
			seg = seg[int(float64(len(seg))*lim.MemoryWarmup):]
			if len(seg) < lim.MemoryMinSamples {
				continue
			}
			if early, late, share, ok := growing(seg, lim); ok {
				out = append(out, Finding{Kind: MemoryGrowth, Node: key, Detail: fmt.Sprintf(
					"%s: anonymous memory rose from about %d to about %d MiB over %d samples, %.0f%% of the steps not falling (limit: +%d%% and +%d MiB with %.0f%% of steps not falling)",
					key, early, late, len(seg), share*100, lim.MemoryGrowthPct, lim.MemoryGrowthFloorMB, lim.MonotonicShare*100)})
				break
			}
		}
	}
	return out
}

// growing reports whether seg rises steadily by more than the limits, with the medians of its first
// and last thirds and the share of steps that did not fall.
func growing(seg []int, lim Limits) (early, late int, share float64, ok bool) {
	// One sample has no step to judge, whatever MemoryMinSamples a caller set.
	if len(seg) < 2 {
		return 0, 0, 0, false
	}
	third := max(len(seg)/3, 1)
	early, late = median(seg[:third]), median(seg[len(seg)-third:])
	steps, kept := len(seg)-1, 0
	for i := 1; i < len(seg); i++ {
		if seg[i] >= seg[i-1]-lim.MemoryJitterMB {
			kept++
		}
	}
	share = float64(kept) / float64(steps)
	rose := late-early >= lim.MemoryGrowthFloorMB && late*100 >= early*(100+lim.MemoryGrowthPct)
	return early, late, share, rose && share >= lim.MonotonicShare
}

func median(v []int) int {
	c := append([]int(nil), v...)
	sort.Ints(c)
	return c[len(c)/2]
}

// restarts finds a service whose restart counter rose during the soak: nothing in it restarts a
// daemon, so a restart is a crash (or an OOM kill) the run must not hide.
func restarts(tl Timeline) []Finding {
	all := series(tl)
	var out []Finding
	for _, key := range sortedKeys(all) {
		pts := all[key]
		if rose := pts[len(pts)-1].svc.Restarts - pts[0].svc.Restarts; rose > 0 {
			out = append(out, Finding{Kind: Restart, Node: key, Detail: fmt.Sprintf("%s restarted %d time(s) during the soak", key, rose)})
		}
	}
	return out
}
