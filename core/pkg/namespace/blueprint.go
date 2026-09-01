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
	ServiceRQLite       ServiceName = "rqlite"
	ServiceOlric        ServiceName = "olric"
	ServiceGateway      ServiceName = "gateway"
	ServiceIPFS         ServiceName = "ipfs"
	ServiceIPFSCluster  ServiceName = "ipfs-cluster"
	ServiceIPFSGC       ServiceName = "ipfs-gc"
	ServiceWireGuard    ServiceName = "wireguard"
	ServiceVault        ServiceName = "vault"
	ServiceCaddy        ServiceName = "caddy"
	ServiceNtfy         ServiceName = "ntfy"
	ServiceAnyoneClient ServiceName = "anyone-client"
	ServiceSNIRouter    ServiceName = "sni-router"
	ServiceCoreDNS      ServiceName = "coredns"
	ServicePubsub       ServiceName = "pubsub"
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
	return BlueprintTenantN(DefaultRQLiteNodeCount)
}

// BlueprintIndex is the host/control plane on this machine. Membership is
// all nodes; ClusterManager must not select or allocate tenant ports.
// Gateway stays out until BlueprintIndexWithGateway.
func BlueprintIndex() Blueprint {
	return Blueprint{
		Name:       BlueprintNameIndex,
		Membership: MembersAll,
		Services: []ServiceSpec{
			{
				Name:  ServiceWireGuard,
				Order: 1,
				Scope: ScopeIndex,
				PortNeeds: []PortNeed{
					{Fixed: IndexWireGuardPort},
				},
			},
			{
				Name:  ServiceIPFS,
				Order: 2,
				Scope: ScopeIndex,
				PortNeeds: []PortNeed{
					{Fixed: IndexIPFSAPIPort},
				},
			},
			{
				Name:  ServiceIPFSCluster,
				Order: 3,
				Scope: ScopeIndex,
				PortNeeds: []PortNeed{
					{Fixed: IndexIPFSClusterAPIPort},
				},
			},
			{
				Name:      ServiceIPFSGC,
				Order:     4,
				Scope:     ScopeIndex,
				PortNeeds: nil,
			},
			{
				Name:  ServiceRQLite,
				Order: 5,
				Scope: ScopeReusable,
				PortNeeds: []PortNeed{
					{Fixed: IndexRQLiteHTTPPort},
					{Fixed: IndexRQLiteRaftPort},
				},
			},
			{
				Name:  ServiceOlric,
				Order: 6,
				Scope: ScopeReusable,
				PortNeeds: []PortNeed{
					{Fixed: IndexOlricHTTPPort},
					{Fixed: IndexOlricMemberlistPort},
				},
			},
			{
				Name:  ServicePubsub,
				Order: 7,
				Scope: ScopeIndex,
				PortNeeds: []PortNeed{
					{Fixed: IndexPubsubPort},
				},
			},
			{
				Name:  ServiceVault,
				Order: 9,
				Scope: ScopeIndex,
				PortNeeds: []PortNeed{
					{Fixed: IndexVaultPort},
				},
			},
			{
				Name:  ServiceSNIRouter,
				Order: 10,
				Scope: ScopeIndex,
				PortNeeds: []PortNeed{
					{Fixed: IndexCaddyHTTPSPort},
				},
			},
			{
				Name:  ServiceCaddy,
				Order: 11,
				Scope: ScopeIndex,
				PortNeeds: []PortNeed{
					{Fixed: IndexCaddyHTTPPort},
					{Fixed: IndexCaddyHTTPSPort},
				},
			},
			{
				Name:  ServiceNtfy,
				Order: 12,
				Scope: ScopeIndex,
				PortNeeds: []PortNeed{
					{Fixed: IndexNtfyPort},
				},
			},
			{
				Name:  ServiceAnyoneClient,
				Order: 13,
				Scope: ScopeIndex,
				PortNeeds: []PortNeed{
					{Fixed: IndexAnyoneSOCKSPort},
				},
			},
		},
	}
}

// BlueprintIndexWithGateway is BlueprintIndex plus the core gateway,
// inserted after pubsub (before vault / edge TLS).
func BlueprintIndexWithGateway() Blueprint {
	bp := BlueprintIndex()
	gw := ServiceSpec{
		Name:  ServiceGateway,
		Order: 8,
		Scope: ScopeReusable,
		PortNeeds: []PortNeed{
			{Fixed: IndexGatewayHTTPPort},
		},
	}
	out := make([]ServiceSpec, 0, len(bp.Services)+1)
	inserted := false
	for _, spec := range bp.Services {
		out = append(out, spec)
		if spec.Name == ServicePubsub {
			out = append(out, gw)
			inserted = true
		}
	}
	if !inserted {
		out = append(out, gw)
	}
	bp.Services = out
	return bp
}

// IsReservedNamespace reports names that must never be tenant-provisioned.
func IsReservedNamespace(name string) bool {
	switch name {
	case BlueprintNameIndex, BlueprintNameNameserver:
		return true
	default:
		return false
	}
}

// BlueprintNameserver is CoreDNS on :53 for a node installed with --nameserver.
// Membership is local (this node only). SNI router and Caddy are index, not here.
func BlueprintNameserver() Blueprint {
	return Blueprint{
		Name:       BlueprintNameNameserver,
		Membership: MembersLocal,
		Services: []ServiceSpec{
			{
				Name:  ServiceCoreDNS,
				Order: 1,
				Scope: ScopeNameserver,
				PortNeeds: []PortNeed{
					{Fixed: NameserverDNSPort},
				},
			},
		},
	}
}

// BlueprintTenantN is a tenant cluster of n members. n=3 is the API-key
// default (BlueprintTenant). n=1 is a single-node cluster (rqlite leader,
// no -join). Port needs stay 2+2+1.
func BlueprintTenantN(n int) Blueprint {
	return Blueprint{
		Name:        BlueprintNameTenant,
		Membership:  MembersSelect,
		SelectCount: n,
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
	if b.Membership == MembersSelect && b.SelectCount < 1 {
		return fmt.Errorf("SelectCount %d must be >= 1", b.SelectCount)
	}
	seenOffset := map[int]ServiceName{}
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
		needCount := b.PortNeedCount()
		for _, p := range spec.PortNeeds {
			if p.Fixed != 0 {
				continue
			}
			if p.FromBlock < 0 || (needCount > 0 && p.FromBlock >= needCount) {
				return fmt.Errorf("service %s port offset %d is outside block size %d", spec.Name, p.FromBlock, needCount)
			}
			if other, ok := seenOffset[p.FromBlock]; ok {
				return fmt.Errorf("port offset %d used by %s and %s", p.FromBlock, other, spec.Name)
			}
			seenOffset[p.FromBlock] = spec.Name
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
