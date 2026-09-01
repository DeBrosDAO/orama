package namespace

import (
	"testing"
)

func TestBlueprintTenant_orderAndPorts(t *testing.T) {
	bp := BlueprintTenant()

	if bp.Name != BlueprintNameTenant {
		t.Errorf("Name = %q, want %q", bp.Name, BlueprintNameTenant)
	}
	if bp.Membership != MembersSelect {
		t.Errorf("Membership = %v, want %s", bp.Membership, MembersSelect)
	}
	if bp.SelectCount != 3 {
		t.Errorf("SelectCount = %d, want 3", bp.SelectCount)
	}
	if bp.SelectCount != DefaultRQLiteNodeCount {
		t.Errorf("SelectCount = %d, want DefaultRQLiteNodeCount %d", bp.SelectCount, DefaultRQLiteNodeCount)
	}

	want := []ServiceName{ServiceRQLite, ServiceOlric, ServiceGateway}
	if len(bp.Services) != len(want) {
		t.Fatalf("len(Services) = %d, want %d", len(bp.Services), len(want))
	}
	for i, name := range want {
		got := bp.Services[i]
		if got.Name != name {
			t.Errorf("Services[%d] = %s, want %s; tenant must start rqlite before olric before gateway", i, got.Name, name)
		}
		if got.Scope != ScopeReusable {
			t.Errorf("%s Scope = %q, want %q", name, got.Scope, ScopeReusable)
		}
		if got.Count != 0 {
			t.Errorf("%s Count = %d, want 0 (all members; membership is SelectCount)", name, got.Count)
		}
		if n := bp.MemberCount(got); n != bp.SelectCount {
			t.Errorf("%s MemberCount = %d, want SelectCount %d", name, n, bp.SelectCount)
		}
	}
	if bp.Services[0].Order >= bp.Services[1].Order || bp.Services[1].Order >= bp.Services[2].Order {
		t.Errorf("Order must be rqlite < olric < gateway, got %d, %d, %d",
			bp.Services[0].Order, bp.Services[1].Order, bp.Services[2].Order)
	}

	if n := bp.PortNeedCount(); n != PortsPerNamespace {
		t.Errorf("sum PortNeeds = %d, want PortsPerNamespace %d", n, PortsPerNamespace)
	}
	if got := len(bp.Services[0].PortNeeds); got != 2 {
		t.Errorf("rqlite PortNeeds = %d, want 2", got)
	}
	if got := len(bp.Services[1].PortNeeds); got != 2 {
		t.Errorf("olric PortNeeds = %d, want 2", got)
	}
	if got := len(bp.Services[2].PortNeeds); got != 1 {
		t.Errorf("gateway PortNeeds = %d, want 1", got)
	}
	if err := bp.Validate(); err != nil {
		t.Fatalf("BlueprintTenant must be valid: %v", err)
	}
}

func TestBlueprintTenant_rejectsIndexSingletons(t *testing.T) {
	base := BlueprintTenant()
	singletons := []ServiceSpec{
		{Name: ServiceIPFS, Scope: ScopeIndex, Order: 99},
		{Name: ServiceWireGuard, Scope: ScopeIndex, Order: 99},
		{Name: ServiceCoreDNS, Scope: ScopeNameserver, Order: 99},
	}
	for _, spec := range singletons {
		bp := base
		bp.Services = append(append([]ServiceSpec(nil), base.Services...), spec)
		if err := bp.Validate(); err == nil {
			t.Errorf("expected error including %s (scope %s) on tenant blueprint", spec.Name, spec.Scope)
		}
	}
}

func TestBlueprint_MemberCount(t *testing.T) {
	bp := BlueprintTenant()

	allMembers := ServiceSpec{Name: ServiceRQLite, Scope: ScopeReusable}
	if got := bp.MemberCount(allMembers); got != bp.SelectCount {
		t.Errorf("Count 0 MemberCount = %d, want SelectCount %d", got, bp.SelectCount)
	}

	turnLike := ServiceSpec{Name: "turn", Scope: ScopeReusable, Count: 2}
	if got := bp.MemberCount(turnLike); got != 2 {
		t.Errorf("Count 2 MemberCount = %d, want 2", got)
	}

	tooMany := bp
	tooMany.Services = append(append([]ServiceSpec(nil), bp.Services...), ServiceSpec{
		Name:  "turn",
		Scope: ScopeReusable,
		Count: bp.SelectCount + 1,
	})
	if err := tooMany.Validate(); err == nil {
		t.Fatal("expected error when Count exceeds SelectCount")
	}
}

func TestBlueprint_serviceNodeCounts_subsetRQLite(t *testing.T) {
	bp := BlueprintTenant()
	bp.SelectCount = 10
	bp.Services[0].Count = 3 // rqlite on 3 of 10; olric/gateway Count 0 = all 10
	rqlite, olric, gateway := bp.serviceNodeCounts()
	if rqlite != 3 || olric != 10 || gateway != 10 {
		t.Errorf("serviceNodeCounts = rqlite %d olric %d gateway %d, want 3, 10, 10", rqlite, olric, gateway)
	}
}
