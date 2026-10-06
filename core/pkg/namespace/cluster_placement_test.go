package namespace

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

// fakeFleet is a registry of free blocks per node: selection offers the nodes
// with room, most free first, as the selector does; allocation takes one.
type fakeFleet struct {
	free     map[string]int
	order    []string
	taken    map[string]int
	released []string
	selects  int
	// beforeAllocate runs before each allocation, to let a "concurrent"
	// provisioning take a block between this one's selection and its write.
	beforeAllocate func(nodeID string)
	allocErr       error
	releaseErr     error
}

func newFakeFleet(free map[string]int, order ...string) *fakeFleet {
	return &fakeFleet{free: free, order: order, taken: map[string]int{}}
}

func (f *fakeFleet) placement() placement {
	return placement{
		selectNodes: func(_ context.Context, count int) ([]NodeCapacity, error) {
			f.selects++
			var out []NodeCapacity
			for _, id := range f.order {
				if f.free[id] > 0 && len(out) < count {
					out = append(out, NodeCapacity{NodeID: id})
				}
			}
			if len(out) < count {
				return nil, ErrInsufficientNodes
			}
			return out, nil
		},
		allocate: func(_ context.Context, nodeID string) (*PortBlock, error) {
			if f.beforeAllocate != nil {
				f.beforeAllocate(nodeID)
			}
			if f.allocErr != nil {
				return nil, f.allocErr
			}
			if f.free[nodeID] == 0 {
				return nil, ErrNoPortsAvailable
			}
			f.free[nodeID]--
			f.taken[nodeID]++
			return &PortBlock{NodeID: nodeID, PortStart: NamespacePortRangeStart}, nil
		},
		release: func(_ context.Context, nodeID string) error {
			f.released = append(f.released, nodeID)
			if f.releaseErr != nil {
				return f.releaseErr
			}
			f.free[nodeID]++
			f.taken[nodeID]--
			return nil
		},
		logger: zap.NewNop(),
	}
}

// Two clusters provisioning at once chose the same node for its last block;
// the one that wrote second failed "no ports available on node" although
// another node had room (stagenet e2e, 2026-10-03). It selects again instead.
func TestPlaceCluster_aNodeFilledByAConcurrentClusterIsReplaced(t *testing.T) {
	f := newFakeFleet(map[string]int{"a": 1, "b": 1, "c": 1, "d": 1}, "a", "b", "c", "d")
	f.beforeAllocate = func(nodeID string) {
		if nodeID == "b" && f.selects == 1 {
			f.free["b"] = 0 // the concurrent cluster took b's last block
		}
	}
	nodes, blocks, err := placeCluster(context.Background(), 3, f.placement())
	if err != nil {
		t.Fatalf("placement failed although a, c and d have room: %v", err)
	}
	got := []string{nodes[0].NodeID, nodes[1].NodeID, nodes[2].NodeID}
	if got[0] != "a" || got[1] != "c" || got[2] != "d" || len(blocks) != 3 {
		t.Fatalf("placed on %v with %d blocks, want a, c, d", got, len(blocks))
	}
	if f.taken["a"] != 1 || f.taken["b"] != 0 {
		t.Fatalf("blocks taken %v: the first attempt's block on a was not given back before reselecting", f.taken)
	}
}

// When the fleet really is full, the fresh selection says so.
func TestPlaceCluster_aFullFleetFailsOnSelection(t *testing.T) {
	f := newFakeFleet(map[string]int{"a": 1, "b": 1, "c": 1}, "a", "b", "c")
	f.beforeAllocate = func(nodeID string) {
		if nodeID == "c" {
			f.free["c"] = 0
		}
	}
	if _, _, err := placeCluster(context.Background(), 3, f.placement()); !errors.Is(err, ErrInsufficientNodes) {
		t.Fatalf("err = %v, want ErrInsufficientNodes once no three nodes have room", err)
	}
	if f.free["a"] != 1 || f.free["b"] != 1 {
		t.Fatalf("free blocks %v: the blocks taken before the conflict were not given back", f.free)
	}
}

// Any other allocation failure ends the placement at once.
func TestPlaceCluster_anotherAllocationErrorIsNotRetried(t *testing.T) {
	f := newFakeFleet(map[string]int{"a": 1, "b": 1, "c": 1, "d": 1}, "a", "b", "c", "d")
	want := errors.New("registry refused the write")
	f.allocErr = want
	if _, _, err := placeCluster(context.Background(), 3, f.placement()); !errors.Is(err, want) {
		t.Fatalf("err = %v, want the allocation error", err)
	}
	if f.selects != 1 {
		t.Fatalf("selected %d times for a non-conflict error, want 1", f.selects)
	}
}

// A node that keeps filling under the placement stops it at the bound.
func TestPlaceCluster_conflictsStopAtTheBound(t *testing.T) {
	f := newFakeFleet(map[string]int{"a": 9, "b": 9, "c": 9}, "a", "b", "c")
	// c reports full on every allocation but is offered again on every selection.
	p := f.placement()
	alloc := p.allocate
	p.allocate = func(ctx context.Context, nodeID string) (*PortBlock, error) {
		if nodeID == "c" {
			return nil, ErrNoPortsAvailable
		}
		return alloc(ctx, nodeID)
	}
	if _, _, err := placeCluster(context.Background(), 3, p); !errors.Is(err, ErrNoPortsAvailable) {
		t.Fatalf("err = %v, want ErrNoPortsAvailable after the bound", err)
	}
	if f.selects != placementAttempts {
		t.Fatalf("selected %d times, want the bound %d", f.selects, placementAttempts)
	}
}

// A block that cannot be given back is reported with the failure, not dropped.
func TestPlaceCluster_aFailedReleaseIsReported(t *testing.T) {
	f := newFakeFleet(map[string]int{"a": 1, "b": 1}, "a", "b")
	want := errors.New("registry down")
	f.releaseErr = want
	p := f.placement()
	p.allocate = func(_ context.Context, nodeID string) (*PortBlock, error) {
		if nodeID == "b" {
			return nil, errors.New("disk full")
		}
		return &PortBlock{NodeID: nodeID}, nil
	}
	_, _, err := placeCluster(context.Background(), 2, p)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want it to carry the failed release", err)
	}
}

// A conflict whose blocks could not all be given back is not followed by a new
// selection: it could leave the unreleased block held by this cluster on a node
// that is not one of its members.
func TestPlaceCluster_aFailedReleaseEndsThePlacement(t *testing.T) {
	f := newFakeFleet(map[string]int{"a": 1, "b": 1, "c": 1, "d": 1}, "a", "b", "c", "d")
	f.releaseErr = errors.New("registry down")
	f.beforeAllocate = func(nodeID string) {
		if nodeID == "b" {
			f.free["b"] = 0
		}
	}
	_, _, err := placeCluster(context.Background(), 3, f.placement())
	if !errors.Is(err, errReleaseFailed) || !errors.Is(err, ErrNoPortsAvailable) {
		t.Fatalf("err = %v, want the conflict and the failed release", err)
	}
	if f.selects != 1 {
		t.Fatalf("selected %d times after a failed release, want 1", f.selects)
	}
}

// A selection that fails is returned as it is.
func TestPlaceCluster_selectionErrorIsReturned(t *testing.T) {
	f := newFakeFleet(map[string]int{}, "")
	if _, _, err := placeCluster(context.Background(), 3, f.placement()); !errors.Is(err, ErrInsufficientNodes) {
		t.Fatalf("err = %v, want ErrInsufficientNodes", err)
	}
}
