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

func TestPortsPerNamespace_matchesBlueprintTenant(t *testing.T) {
	if n := BlueprintTenant().PortNeedCount(); n != PortsPerNamespace {
		t.Errorf("BlueprintTenant PortNeedCount = %d, want PortsPerNamespace %d", n, PortsPerNamespace)
	}
}

func TestBlueprintTenantN_1_and_5(t *testing.T) {
	one := BlueprintTenantN(1)
	if one.SelectCount != 1 {
		t.Errorf("N=1 SelectCount = %d, want 1", one.SelectCount)
	}
	if one.PortNeedCount() != PortsPerNamespace {
		t.Errorf("N=1 PortNeedCount = %d, want %d (same services)", one.PortNeedCount(), PortsPerNamespace)
	}
	if err := one.Validate(); err != nil {
		t.Fatalf("N=1 must be valid: %v", err)
	}
	rqliteN, olricN, gatewayN := one.serviceNodeCounts()
	if rqliteN != 1 || olricN != 1 || gatewayN != 1 {
		t.Errorf("N=1 service counts = %d, %d, %d, want 1,1,1", rqliteN, olricN, gatewayN)
	}

	five := BlueprintTenantN(5)
	if five.SelectCount != 5 {
		t.Errorf("N=5 SelectCount = %d, want 5", five.SelectCount)
	}
	if err := five.Validate(); err != nil {
		t.Fatalf("N=5 must be valid: %v", err)
	}

	zero := BlueprintTenantN(0)
	if err := zero.Validate(); err == nil {
		t.Fatal("N=0 must be rejected")
	}
}

func TestBlueprint_gatewayOnlyOnePort(t *testing.T) {
	bp := Blueprint{
		Name:        BlueprintNameTenant,
		Membership:  MembersSelect,
		SelectCount: 1,
		Services: []ServiceSpec{{
			Name:      ServiceGateway,
			Order:     1,
			Scope:     ScopeReusable,
			PortNeeds: []PortNeed{{FromBlock: 0}},
		}},
	}
	if n := bp.PortNeedCount(); n != 1 {
		t.Errorf("gateway-only PortNeedCount = %d, want 1", n)
	}
	if err := bp.Validate(); err != nil {
		t.Fatalf("gateway-only blueprint must be valid: %v", err)
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
