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
		{Name: ServiceIPFSCluster, Scope: ScopeIndex, Order: 99},
		{Name: ServiceIPFSGC, Scope: ScopeIndex, Order: 99},
		{Name: ServiceWireGuard, Scope: ScopeIndex, Order: 99},
		{Name: ServiceVault, Scope: ScopeIndex, Order: 99},
		{Name: ServiceCaddy, Scope: ScopeIndex, Order: 99},
		{Name: ServiceNtfy, Scope: ScopeIndex, Order: 99},
		{Name: ServiceAnyoneClient, Scope: ScopeIndex, Order: 99},
		{Name: ServiceSNIRouter, Scope: ScopeIndex, Order: 99},
		{Name: ServiceCoreDNS, Scope: ScopeNameserver, Order: 99},
		{Name: ServicePubsub, Scope: ScopeIndex, Order: 99},
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

func TestBlueprintIndex_fixedPortsNotTenantRange(t *testing.T) {
	bp := BlueprintIndex()
	if bp.Name != BlueprintNameIndex {
		t.Errorf("Name = %q, want %q", bp.Name, BlueprintNameIndex)
	}
	if bp.Membership != MembersAll {
		t.Errorf("Membership = %s, want %s", bp.Membership, MembersAll)
	}
	if bp.PortNeedCount() != 0 {
		t.Errorf("index PortNeedCount = %d, want 0 (fixed ports, not tenant block)", bp.PortNeedCount())
	}
	if err := bp.Validate(); err != nil {
		t.Fatalf("BlueprintIndex must be valid: %v", err)
	}
	want := []ServiceName{
		ServiceWireGuard, ServiceIPFS, ServiceIPFSCluster, ServiceIPFSGC,
		ServiceRQLite, ServiceOlric, ServicePubsub,
		ServiceVault, ServiceSNIRouter, ServiceCaddy, ServiceNtfy, ServiceAnyoneClient,
	}
	if len(bp.Services) != len(want) {
		t.Fatalf("len(Services) = %d, want %d (%v)", len(bp.Services), len(want), serviceNames(bp))
	}
	for i, name := range want {
		if bp.Services[i].Name != name {
			t.Errorf("Services[%d] = %s, want %s", i, bp.Services[i].Name, name)
		}
	}
	for _, spec := range bp.Services {
		for _, p := range spec.PortNeeds {
			if p.Fixed == 0 {
				t.Errorf("%s: expected Fixed port, got FromBlock %d", spec.Name, p.FromBlock)
			}
			if p.Fixed >= NamespacePortRangeStart && p.Fixed <= NamespacePortRangeEnd {
				t.Errorf("%s Fixed %d collides with tenant range %d-%d", spec.Name, p.Fixed, NamespacePortRangeStart, NamespacePortRangeEnd)
			}
		}
	}

	withGW := BlueprintIndexWithGateway()
	if err := withGW.Validate(); err != nil {
		t.Fatalf("BlueprintIndexWithGateway must be valid: %v", err)
	}
	names := serviceNames(withGW)
	if names[6] != ServicePubsub || names[7] != ServiceGateway || names[8] != ServiceVault {
		t.Fatalf("gateway must sit after pubsub and before vault, got %v", names)
	}
}

func serviceNames(bp Blueprint) []ServiceName {
	out := make([]ServiceName, len(bp.Services))
	for i, s := range bp.Services {
		out[i] = s.Name
	}
	return out
}

func TestBlueprintNameserver_corednsOnly(t *testing.T) {
	bp := BlueprintNameserver()
	if bp.Name != BlueprintNameNameserver {
		t.Errorf("Name = %q, want %q", bp.Name, BlueprintNameNameserver)
	}
	if bp.Membership != MembersLocal {
		t.Errorf("Membership = %s, want %s", bp.Membership, MembersLocal)
	}
	if err := bp.Validate(); err != nil {
		t.Fatalf("BlueprintNameserver must be valid: %v", err)
	}
	if len(bp.Services) != 1 || bp.Services[0].Name != ServiceCoreDNS {
		t.Fatalf("services = %v, want [coredns]", serviceNames(bp))
	}
	if bp.Services[0].Scope != ScopeNameserver {
		t.Errorf("coredns Scope = %s, want %s", bp.Services[0].Scope, ScopeNameserver)
	}
	if n := bp.PortNeedCount(); n != 0 {
		t.Errorf("PortNeedCount = %d, want 0 (fixed :53)", n)
	}
	if got := bp.Services[0].PortNeeds[0].Fixed; got != NameserverDNSPort {
		t.Errorf("coredns Fixed = %d, want %d", got, NameserverDNSPort)
	}
	if NameserverDNSPort != 53 {
		t.Errorf("NameserverDNSPort = %d, want 53", NameserverDNSPort)
	}

	withSNI := bp
	withSNI.Services = append(append([]ServiceSpec(nil), bp.Services...), ServiceSpec{
		Name: ServiceSNIRouter, Scope: ScopeIndex, Order: 2,
	})
	if err := withSNI.Validate(); err == nil {
		t.Fatal("nameserver blueprint must reject sni-router")
	}
	withCaddy := bp
	withCaddy.Services = append(append([]ServiceSpec(nil), bp.Services...), ServiceSpec{
		Name: ServiceCaddy, Scope: ScopeIndex, Order: 2,
	})
	if err := withCaddy.Validate(); err == nil {
		t.Fatal("nameserver blueprint must reject caddy")
	}
}

func TestBlueprintIndex_rejectsCoreDNS(t *testing.T) {
	bp := BlueprintIndex()
	bp.Services = append(bp.Services, ServiceSpec{
		Name: ServiceCoreDNS, Scope: ScopeNameserver, Order: 99,
	})
	if err := bp.Validate(); err == nil {
		t.Fatal("index blueprint must reject coredns")
	}
}

func TestIsReservedNamespace(t *testing.T) {
	if !IsReservedNamespace(BlueprintNameIndex) || !IsReservedNamespace(BlueprintNameNameserver) {
		t.Fatal("index and nameserver must be reserved")
	}
	if IsReservedNamespace("anchat-test") || IsReservedNamespace("rootwallet") {
		t.Fatal("tenant names must not be reserved")
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
