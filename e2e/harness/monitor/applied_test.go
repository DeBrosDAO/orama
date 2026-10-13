package monitor

import "testing"

func appliedReport(applied ...uint64) *Report {
	r := &Report{}
	for i, a := range applied {
		host := testHosts[i%len(testHosts)]
		n := healthyNode(host, i == 0)
		n.RQLite.Applied = a
		r.Nodes = append(r.Nodes, Node{Host: host, Report: n})
	}
	return r
}

// Under steady writes the nodes are read moments apart, so their applied
// indexes spread by the writes in between; every one is still past where the
// cluster was an observation ago.
func TestAppliedBehind_aSpreadUnderWritesIsNoLag(t *testing.T) {
	prev, _ := appliedReport(1000, 990, 995).MaxApplied()
	now := appliedReport(1210, 1100, 1150)
	if behind := now.AppliedBehind(prev); behind != 0 {
		t.Fatalf("AppliedBehind = %d, want 0: every node passed the previous max %d", behind, prev)
	}
}

// A node that stalls is behind the previous max by what the cluster applied
// since it stopped, and further at each observation.
func TestAppliedBehind_aStalledNodeFallsBehind(t *testing.T) {
	prev, _ := appliedReport(1000, 1000, 700).MaxApplied()
	if behind := appliedReport(1200, 1190, 700).AppliedBehind(prev); behind != 300 {
		t.Fatalf("AppliedBehind = %d, want 300", behind)
	}
}

// An unresponsive node has no applied index to compare, and no report has no
// maximum.
func TestMaxApplied_skipsUnresponsiveAndEmpty(t *testing.T) {
	r := appliedReport(500, 900)
	r.Nodes[1].Report.RQLite.Responsive = false
	if hi, ok := r.MaxApplied(); !ok || hi != 500 {
		t.Fatalf("MaxApplied = %d, %v; want 500 from the responsive node", hi, ok)
	}
	if _, ok := (&Report{}).MaxApplied(); ok {
		t.Fatal("an empty report has a maximum")
	}
	if behind := r.AppliedBehind(800); behind != 300 {
		t.Fatalf("AppliedBehind = %d, want 300 from the responsive node only", behind)
	}
}
