package monitor

// MaxApplied is the highest rqlite applied index any node reported, and
// whether any did.
func (r *Report) MaxApplied() (uint64, bool) {
	var hi uint64
	seen := false
	for _, n := range r.Nodes {
		if n.Report == nil || n.Report.RQLite == nil || !n.Report.RQLite.Responsive {
			continue
		}
		hi, seen = max(hi, n.Report.RQLite.Applied), true
	}
	return hi, seen
}

// AppliedBehind is how far the furthest-behind node is from where the cluster
// had applied by the previous observation, prevMax: 0 when every node has
// caught up to it.
//
// The spread of applied indexes within one report is not a lag: each node is
// read at a slightly different moment, so under steady writes it counts the
// writes made between the reads (101 over a bound of 100 on a healthy
// stagenet, 2026-10-03). A node that keeps up passes the index the cluster had
// one observation earlier; one that stalls falls further behind it at every
// observation. It needs writes to see a stall: in an idle cluster nothing moves
// the previous maximum away from a stalled node.
func (r *Report) AppliedBehind(prevMax uint64) uint64 {
	var behind uint64
	for _, n := range r.Nodes {
		if n.Report == nil || n.Report.RQLite == nil || !n.Report.RQLite.Responsive {
			continue
		}
		if a := n.Report.RQLite.Applied; a < prevMax {
			behind = max(behind, prevMax-a)
		}
	}
	return behind
}
