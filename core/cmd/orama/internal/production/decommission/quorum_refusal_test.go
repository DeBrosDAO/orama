package decommission

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/clusterops"
)

func TestQuorumRefusal_unsafeRemovalIsConflict(t *testing.T) {
	impacts := []clusterops.Impact{
		{Cluster: clusterops.PlatformCluster, VotersBefore: 3, VotersAfter: 2, QuorumAfter: 2, ReachableAfter: 2},
		{Cluster: "tenant-a", VotersBefore: 3, VotersAfter: 2, QuorumAfter: 2, ReachableAfter: 1, Refusal: "1 of 2 reachable"},
	}
	err := quorumRefusal("10.0.0.2", impacts)
	if err == nil {
		t.Fatal("a removal that costs tenant-a its quorum was not refused")
	}
	if got := clierr.CodeOf(err); got != clierr.CodeConflict {
		t.Errorf("exit code = %d, want %d (CodeConflict)", got, clierr.CodeConflict)
	}
	if !strings.Contains(err.Error(), "would cost a cluster its quorum") || !strings.Contains(err.Error(), "10.0.0.2") {
		t.Errorf("refusal %q does not name the host and the quorum", err)
	}
}

func TestQuorumRefusal_safeRemovalPasses(t *testing.T) {
	impacts := []clusterops.Impact{{Cluster: clusterops.PlatformCluster, VotersBefore: 5, VotersAfter: 4, QuorumAfter: 3, ReachableAfter: 4}}
	if err := quorumRefusal("10.0.0.2", impacts); err != nil {
		t.Fatalf("a safe removal was refused: %v", err)
	}
}

func TestQuorumRefusal_noClustersPasses(t *testing.T) {
	if err := quorumRefusal("10.0.0.2", nil); err != nil {
		t.Fatalf("a node in no cluster was refused: %v", err)
	}
}
