package checks

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// termConsistency judges the raft terms the nodes report, keyed by term. A
// term of 0 is a status that carried none (an unparsed or pre-term report) and
// is left out. One term apart is a warning, not a failure: the inspector reads
// the nodes one after another, and an election between two reads, or a
// follower that has not yet heard from the new leader, is one term behind on
// a healthy cluster. More than one apart is divergence.
func termConsistency(terms map[uint64][]string) inspector.CheckResult {
	const id, name = "rqlite.term_consistent", "All nodes same Raft term"
	var known []uint64
	for t := range terms {
		if t != 0 {
			known = append(known, t)
		}
	}
	if len(known) == 0 {
		return inspector.Skip(id, name, rqliteSub, "", "no node reported a raft term", inspector.Critical)
	}
	sort.Slice(known, func(i, j int) bool { return known[i] < known[j] })
	lo, hi := known[0], known[len(known)-1]
	if lo == hi {
		return inspector.Pass(id, name, rqliteSub, "", fmt.Sprintf("term=%d across %d nodes", lo, len(terms[lo])), inspector.Critical)
	}
	var parts []string
	for _, t := range known {
		parts = append(parts, fmt.Sprintf("term=%d: %s", t, strings.Join(terms[t], ",")))
	}
	if hi-lo == 1 {
		return inspector.Warn(id, name, rqliteSub, "", "one term apart (an election in progress?): "+strings.Join(parts, "; "), inspector.Critical)
	}
	return inspector.Fail(id, name, rqliteSub, "", "term divergence: "+strings.Join(parts, "; "), inspector.Critical)
}
