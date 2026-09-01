package namespace

import (
	"fmt"
)

// Membership is how a blueprint picks machines.
type Membership string

const (
	// MembersAll is every cluster node (index).
	MembersAll Membership = "all"
	// MembersSelect is N nodes chosen by the selector (tenant).
	MembersSelect Membership = "select"
	// MembersLocal is this node only, and only when it has the role (nameserver).
	MembersLocal Membership = "local"
)

// Scope is which blueprints may include a service. Tenants cannot start
// host singletons (ipfs, wireguard, coredns, …).
type Scope string

const (
	ScopeIndex      Scope = "index"
	ScopeNameserver Scope = "nameserver"
	ScopeReusable   Scope = "reusable"
)

// ServiceName is the registry key for a ServiceDriver.
type ServiceName string

const (
	ServiceRQLite    ServiceName = "rqlite"
	ServiceOlric     ServiceName = "olric"
	ServiceGateway   ServiceName = "gateway"
	ServiceIPFS      ServiceName = "ipfs"
	ServiceWireGuard ServiceName = "wireguard"
	ServiceCoreDNS   ServiceName = "coredns"
)

// Named blueprints. Index and nameserver are reserved; they are not
// tenant-provisionable.
const (
	BlueprintNameTenant     = "tenant"
	BlueprintNameIndex      = "index"
	BlueprintNameNameserver = "nameserver"
)

// PortNeed describes one port a service binds.
// FromBlock is the offset inside the namespace port block (tenant 10000–10099).
// Fixed is an absolute host port (index/nameserver edge). Zero means unused.
type PortNeed struct {
	FromBlock int
	Fixed     int
}

// ServiceSpec is one entry in a Blueprint.Services list.
type ServiceSpec struct {
	Name      ServiceName
	Order     int
	Count     int // replica count among members. 0 = all (SelectCount). Example: 10 members, rqlite Count=3.
	Scope     Scope
	PortNeeds []PortNeed
}

// Blueprint is a named recipe: membership, service list, start order, ports.
type Blueprint struct {
	Name        string
	Membership  Membership
	SelectCount int
	Services    []ServiceSpec
}

// BlueprintTenant is today's namespace cluster: 3 nodes, rqlite → olric →
// gateway, 5-port block (2+2+1). WebRTC (sfu/turn) stays a bolt-on.
func BlueprintTenant() Blueprint {
	return Blueprint{
		Name:        BlueprintNameTenant,
		Membership:  MembersSelect,
		SelectCount: DefaultRQLiteNodeCount,
		Services: []ServiceSpec{
			{
				Name:  ServiceRQLite,
				Order: 1,
				Scope: ScopeReusable,
				PortNeeds: []PortNeed{
					{FromBlock: 0}, // RQLite HTTP
					{FromBlock: 1}, // RQLite Raft
				},
			},
			{
				Name:  ServiceOlric,
				Order: 2,
				Scope: ScopeReusable,
				PortNeeds: []PortNeed{
					{FromBlock: 2}, // Olric HTTP
					{FromBlock: 3}, // Olric memberlist
				},
			},
			{
				Name:  ServiceGateway,
				Order: 3,
				Scope: ScopeReusable,
				PortNeeds: []PortNeed{
					{FromBlock: 4}, // Gateway HTTP
				},
			},
		},
	}
}

// MemberCount is how many of the blueprint's members run spec.
// Count == 0 means all members.
func (b Blueprint) MemberCount(spec ServiceSpec) int {
	if spec.Count > 0 {
		return spec.Count
	}
	return b.SelectCount
}

// serviceNodeCounts maps each tenant service to its replica count.
// These feed the three namespace_clusters columns, which are allowed
// to differ (10 members, rqlite on 3, olric/gateway on all 10).
func (b Blueprint) serviceNodeCounts() (rqlite, olric, gateway int) {
	rqlite, olric, gateway = b.SelectCount, b.SelectCount, b.SelectCount
	for _, spec := range b.Services {
		n := b.MemberCount(spec)
		switch spec.Name {
		case ServiceRQLite:
			rqlite = n
		case ServiceOlric:
			olric = n
		case ServiceGateway:
			gateway = n
		}
	}
	return
}

// PortNeedCount is the number of ports the blueprint consumes from the
// namespace block (Fixed edge ports are not counted).
func (b Blueprint) PortNeedCount() int {
	n := 0
	for _, spec := range b.Services {
		for _, p := range spec.PortNeeds {
			if p.Fixed == 0 {
				n++
			}
		}
	}
	return n
}

// Validate rejects services whose Scope is not allowed on this blueprint.
func (b Blueprint) Validate() error {
	allowed := b.allowedScopes()
	if allowed == nil {
		return fmt.Errorf("unknown blueprint %q", b.Name)
	}
	for _, spec := range b.Services {
		if !allowed[spec.Scope] {
			return fmt.Errorf("service %s (scope %s) is not allowed on blueprint %s", spec.Name, spec.Scope, b.Name)
		}
		if spec.Count < 0 {
			return fmt.Errorf("service %s Count %d is negative", spec.Name, spec.Count)
		}
		if b.Membership == MembersSelect && spec.Count > b.SelectCount {
			return fmt.Errorf("service %s Count %d exceeds SelectCount %d", spec.Name, spec.Count, b.SelectCount)
		}
	}
	return nil
}

func (b Blueprint) allowedScopes() map[Scope]bool {
	switch b.Name {
	case BlueprintNameTenant:
		return map[Scope]bool{ScopeReusable: true}
	case BlueprintNameIndex:
		return map[Scope]bool{ScopeIndex: true, ScopeReusable: true}
	case BlueprintNameNameserver:
		return map[Scope]bool{ScopeNameserver: true}
	}
	switch b.Membership {
	case MembersSelect:
		return map[Scope]bool{ScopeReusable: true}
	case MembersAll:
		return map[Scope]bool{ScopeIndex: true, ScopeReusable: true}
	case MembersLocal:
		return map[Scope]bool{ScopeNameserver: true}
	}
	return nil
}
