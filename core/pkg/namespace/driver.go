package namespace

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// ServiceDriver starts, stops, and health-checks one service kind.
// Spawn is cluster-scoped: the driver owns member sequencing (RQLite
// leader-then-followers, Olric concurrent). walkServices must not loop nodes.
type ServiceDriver interface {
	Name() ServiceName
	Scope() Scope
	PortNeeds() []PortNeed
	Spawn(ctx context.Context, req SpawnRequest) error
	Stop(ctx context.Context, ns, node string) error
	Ready(ctx context.Context, ns, node string, ports []int) error
}

// SpawnRequest is the cluster-level input to ServiceDriver.Spawn.
type SpawnRequest struct {
	Cluster    *NamespaceCluster
	Nodes      []NodeCapacity
	PortBlocks []*PortBlock
	State      *provisionState
}

// provisionState holds instances started so far in one provision walk.
// Gateway needs RQLite/Olric DSNs; rollback needs whatever succeeded.
type provisionState struct {
	rqlite  []*rqlite.Instance
	olric   []*olric.OlricInstance
	gateway []*gateway.GatewayInstance
}

type driverRegistry struct {
	mu sync.RWMutex
	m  map[ServiceName]ServiceDriver
}

func newDriverRegistry() *driverRegistry {
	return &driverRegistry{m: make(map[ServiceName]ServiceDriver)}
}

func (r *driverRegistry) Register(d ServiceDriver) {
	if d == nil {
		panic("namespace: Register nil ServiceDriver")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = make(map[ServiceName]ServiceDriver)
	}
	r.m[d.Name()] = d
}

func (r *driverRegistry) lookup(name ServiceName) (ServiceDriver, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.m[name]
	return d, ok
}

func (r *driverRegistry) MustDriver(name ServiceName) ServiceDriver {
	d, ok := r.lookup(name)
	if !ok {
		panic("namespace: no driver registered for " + string(name))
	}
	return d
}

var defaultRegistry = newDriverRegistry()

// Register adds d to the package-level driver registry.
func Register(d ServiceDriver) { defaultRegistry.Register(d) }

// MustDriver returns the package-level driver for name, or panics.
func MustDriver(name ServiceName) ServiceDriver { return defaultRegistry.MustDriver(name) }

// Driver looks up a package-level driver.
func Driver(name ServiceName) (ServiceDriver, bool) { return defaultRegistry.lookup(name) }

// walkServices invokes each blueprint driver once, in Order.
// Sequencing inside a service (leader-first, concurrent members) belongs
// in the driver, not here.
func walkServices(ctx context.Context, bp Blueprint, reg *driverRegistry, req SpawnRequest) error {
	if err := bp.Validate(); err != nil {
		return err
	}
	if reg == nil {
		return fmt.Errorf("walkServices: nil driver registry")
	}
	specs := append([]ServiceSpec(nil), bp.Services...)
	sort.SliceStable(specs, func(i, j int) bool { return specs[i].Order < specs[j].Order })
	for _, spec := range specs {
		driver := reg.MustDriver(spec.Name)
		if err := driver.Spawn(ctx, req); err != nil {
			return err
		}

		// Wait for the service to actually work before starting the next one.
		//
		// The blueprint's Order encodes a real dependency — the gateway cannot
		// initialise its Olric client if Olric is not up, and does not retry —
		// but "spawned" only ever meant the systemd unit reported active, which
		// for a Type=simple unit means the binary was exec'd. The gap was
		// covered by a fixed sleep, which is a guess.
		if err := awaitServiceReady(ctx, driver, req); err != nil {
			return err
		}
	}
	return nil
}

// awaitServiceReady probes one service on every node it was spawned on.
//
// It stops at the first node that does not come up, and names it: a namespace
// that is broken on one of five nodes is a different problem from one that is
// broken everywhere, and the error is the only place that distinction survives.
func awaitServiceReady(ctx context.Context, driver ServiceDriver, req SpawnRequest) error {
	if req.Cluster == nil {
		return nil
	}
	for i, node := range req.Nodes {
		if i >= len(req.PortBlocks) || req.PortBlocks[i] == nil {
			return fmt.Errorf("%s on %s: no port block allocated, so it cannot be probed",
				driver.Name(), node.NodeID)
		}
		ports := servicePorts(driver.Name(), req.PortBlocks[i])
		if len(ports) == 0 {
			continue
		}
		if err := driver.Ready(ctx, req.Cluster.NamespaceName, node.NodeID, ports); err != nil {
			return err
		}
	}
	return nil
}

// servicePorts picks the ports a service's readiness probe needs from a node's
// block. The first entry is the one the probe dials.
func servicePorts(name ServiceName, block *PortBlock) []int {
	switch name {
	case ServiceRQLite:
		return []int{block.RQLiteHTTPPort, block.RQLiteRaftPort}
	case ServiceOlric:
		return []int{block.OlricHTTPPort, block.OlricMemberlistPort}
	case ServiceGateway:
		return []int{block.GatewayHTTPPort}
	default:
		return nil
	}
}
