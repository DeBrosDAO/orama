package namespace

import (
	"context"
	"reflect"
	"testing"
)

type recordingDriver struct {
	name      ServiceName
	scope     Scope
	portNeeds []PortNeed
	rec       *[]string
}

func (d *recordingDriver) Name() ServiceName { return d.name }
func (d *recordingDriver) Scope() Scope      { return d.scope }
func (d *recordingDriver) PortNeeds() []PortNeed {
	return d.portNeeds
}

func (d *recordingDriver) Spawn(_ context.Context, req SpawnRequest) error {
	// One record per member, in node-list order. Matches today: finish the
	// whole service (all 3 nodes) before the next service starts.
	for range req.Nodes {
		*d.rec = append(*d.rec, string(d.name))
	}
	return nil
}

func (d *recordingDriver) Stop(context.Context, string, string) error { return nil }
func (d *recordingDriver) Ready(context.Context, string, string, []int) error {
	return nil
}

func tenantWalkFixture() (Blueprint, *driverRegistry, SpawnRequest, *[]string) {
	bp := BlueprintTenant()
	var rec []string
	reg := newDriverRegistry()
	for _, spec := range bp.Services {
		s := spec
		reg.Register(&recordingDriver{
			name:      s.Name,
			scope:     s.Scope,
			portNeeds: s.PortNeeds,
			rec:       &rec,
		})
	}
	req := SpawnRequest{
		Cluster: &NamespaceCluster{NamespaceName: "test"},
		Nodes: []NodeCapacity{
			{NodeID: "n1"},
			{NodeID: "n2"},
			{NodeID: "n3"},
		},
		PortBlocks: []*PortBlock{{}, {}, {}},
		State:      &provisionState{},
	}
	return bp, reg, req, &rec
}

func TestProvisionWalk_callsDriversInOrder(t *testing.T) {
	bp, reg, req, rec := tenantWalkFixture()
	if err := walkServices(context.Background(), bp, reg, req); err != nil {
		t.Fatalf("walkServices: %v", err)
	}
	if len(req.Nodes) != 3 {
		t.Fatalf("walk passed %d nodes, want 3", len(req.Nodes))
	}
	want := []string{
		"rqlite", "rqlite", "rqlite",
		"olric", "olric", "olric",
		"gateway", "gateway", "gateway",
	}
	if !reflect.DeepEqual(*rec, want) {
		t.Errorf("spawn order = %v, want %v (rqlite cluster fully, then olric, then gateway)", *rec, want)
	}
}

func TestProvisionWalk_olricBeforeRQLiteChangesOrder(t *testing.T) {
	bp, reg, req, rec := tenantWalkFixture()
	// Swap Order so olric starts before rqlite. This is the test that
	// fails if walkServices ignores blueprint Order.
	bp.Services[0].Order, bp.Services[1].Order = bp.Services[1].Order, bp.Services[0].Order
	if err := walkServices(context.Background(), bp, reg, req); err != nil {
		t.Fatalf("walkServices: %v", err)
	}
	got := *rec
	if len(got) < 6 {
		t.Fatalf("spawn log too short: %v", got)
	}
	if got[0] != "olric" || got[3] != "rqlite" {
		t.Errorf("swapped Order should spawn olric then rqlite, got %v", got)
	}
}

func TestRegister_MustDriver(t *testing.T) {
	prev := defaultRegistry
	defaultRegistry = newDriverRegistry()
	t.Cleanup(func() { defaultRegistry = prev })

	d := &recordingDriver{name: ServiceRQLite, rec: new([]string)}
	Register(d)
	got := MustDriver(ServiceRQLite)
	if got.Name() != ServiceRQLite {
		t.Errorf("MustDriver(rqlite).Name() = %s", got.Name())
	}
	if _, ok := Driver(ServiceOlric); ok {
		t.Error("Driver(olric) should be missing")
	}
}

func TestMustDriver_panicsWhenMissing(t *testing.T) {
	reg := newDriverRegistry()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for missing driver")
		}
	}()
	reg.MustDriver(ServiceRQLite)
}
