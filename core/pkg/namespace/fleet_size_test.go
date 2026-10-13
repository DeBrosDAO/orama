package namespace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// The stagenet case: five members, four full. Sizing by the nodes with room
// made this an eval namespace on the one node left; it is a refusal.
func TestChooseTenantBlueprint_fullLargeFleetRefusesInsteadOfEval(t *testing.T) {
	_, err := chooseTenantBlueprint(5, 1)
	if !errors.Is(err, ErrInsufficientNodes) {
		t.Fatalf("5 members, 1 with room: err = %v, want ErrInsufficientNodes (the create handler's capacity refusal)", err)
	}
	if !strings.Contains(err.Error(), "needs 3 nodes") || !strings.Contains(err.Error(), "1 have one") {
		t.Errorf("refusal %q does not say how far short the fleet is", err)
	}
}

func TestChooseTenantBlueprint_sizedByTheFleet(t *testing.T) {
	for _, tt := range []struct{ members, withRoom, wantN int }{
		{1, 1, 1},
		{3, 3, 3},
		{5, 4, 3},
		{10, 10, 3},
	} {
		bp, err := chooseTenantBlueprint(tt.members, tt.withRoom)
		if err != nil || bp.SelectCount != tt.wantN {
			t.Errorf("members=%d withRoom=%d: N=%d err=%v, want N=%d", tt.members, tt.withRoom, bp.SelectCount, err, tt.wantN)
		}
	}
}

func TestChooseTenantBlueprint_refusals(t *testing.T) {
	for _, tt := range []struct {
		members, withRoom int
		want              error
	}{
		{0, 0, ErrInsufficientNodes},
		{1, 0, ErrInsufficientNodes}, // the one eval node is full
		{2, 2, ErrTwoNodeFleet},
		{3, 2, ErrInsufficientNodes},
	} {
		if _, err := chooseTenantBlueprint(tt.members, tt.withRoom); !errors.Is(err, tt.want) {
			t.Errorf("members=%d withRoom=%d: err = %v, want %v", tt.members, tt.withRoom, err, tt.want)
		}
	}
}

// Membership is registration, not liveness: retired nodes are left out by the
// date `orama node remove` gives them, and nothing filters on last_seen being
// recent.
func TestFleetMemberCount_countsRegisteredNodesNotRetiredOnes(t *testing.T) {
	db := &recoveryMockDB{queryFunc: func(dest any, query string, args ...any) error {
		*dest.(*[]fleetMember) = []fleetMember{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}
		return nil
	}}
	selector := NewClusterNodeSelector(db, NewNamespacePortAllocator(db, zap.NewNop()), zap.NewNop())
	n, err := selector.FleetMemberCount(context.Background())
	if err != nil || n != 5 {
		t.Fatalf("FleetMemberCount = %d, %v; want 5", n, err)
	}
	call := db.queryCalls[len(db.queryCalls)-1]
	if !strings.Contains(call.Query, "last_seen != ?") || len(call.Args) != 1 || call.Args[0] != RetiredNodeLastSeen {
		t.Fatalf("query %q args %v: want retired nodes excluded by RetiredNodeLastSeen", call.Query, call.Args)
	}
	if strings.Contains(call.Query, "status") || strings.Contains(call.Query, "last_seen >") {
		t.Fatalf("query %q filters on liveness: a fleet whose nodes stopped heartbeating would look like eval", call.Query)
	}
}

func TestFleetMemberCount_queryFailureIsAnError(t *testing.T) {
	db := &recoveryMockDB{queryFunc: func(any, string, ...any) error { return errors.New("no leader") }}
	selector := NewClusterNodeSelector(db, NewNamespacePortAllocator(db, zap.NewNop()), zap.NewNop())
	if _, err := selector.FleetMemberCount(context.Background()); err == nil {
		t.Fatal("a failed count read as a fleet size")
	}
}

func TestSelectNodesForCluster_shortfallIsTheCapacitySentinel(t *testing.T) {
	if err := capacityShortfall(3, 1); !errors.Is(err, ErrInsufficientNodes) {
		t.Fatalf("capacityShortfall does not wrap ErrInsufficientNodes: %v", err)
	}
}
