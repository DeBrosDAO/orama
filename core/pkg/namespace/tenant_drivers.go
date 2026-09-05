package namespace

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// Thin ServiceDriver wrappers around the existing start* helpers.
// Sequencing (RQLite leader-then-join, Olric concurrent + 5s settle)
// stays inside those helpers.

func (cm *ClusterManager) initTenantDrivers() {
	if cm.drivers == nil {
		cm.drivers = newDriverRegistry()
	}
	cm.drivers.Register(&rqliteClusterDriver{cm: cm})
	cm.drivers.Register(&olricClusterDriver{cm: cm})
	cm.drivers.Register(&gatewayClusterDriver{cm: cm})
}

// probeTarget builds the overlay host:port a readiness probe dials.
//
// Probes go over the WireGuard address, not the public one: the tenant service
// ports are only reachable there, and a probe that dialled a public IP would be
// testing the firewall rather than the service.
func probeTarget(cm *ClusterManager, nodeID string, ports []int) (string, error) {
	if len(ports) == 0 || ports[0] == 0 {
		return "", fmt.Errorf("no port allocated")
	}
	internalIP, err := cm.nodeInternalIP(nodeID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d", internalIP, ports[0]), nil
}

func tenantSpec(name ServiceName) ServiceSpec {
	for _, spec := range BlueprintTenant().Services {
		if spec.Name == name {
			return spec
		}
	}
	panic("namespace: tenant blueprint missing " + string(name))
}

type rqliteClusterDriver struct{ cm *ClusterManager }

func (d *rqliteClusterDriver) Name() ServiceName     { return ServiceRQLite }
func (d *rqliteClusterDriver) Scope() Scope          { return tenantSpec(ServiceRQLite).Scope }
func (d *rqliteClusterDriver) PortNeeds() []PortNeed { return tenantSpec(ServiceRQLite).PortNeeds }
func (d *rqliteClusterDriver) Stop(context.Context, string, string) error {
	return nil
}

// Ready waits for this node's tenant rqlite to be participating in raft and
// able to serve a read. ports carries the node's block; the first entry is the
// HTTP port, per the driver's PortNeeds.
func (d *rqliteClusterDriver) Ready(ctx context.Context, ns, node string, ports []int) error {
	hostPort, err := probeTarget(d.cm, node, ports)
	if err != nil {
		return fmt.Errorf("rqlite readiness for %s on %s: %w", ns, node, err)
	}
	return awaitReady(ctx, readyTimeout, fmt.Sprintf("rqlite for %s on %s", ns, node), func(ctx context.Context) error {
		return rqliteReady(ctx, hostPort)
	})
}
func (d *rqliteClusterDriver) Spawn(ctx context.Context, req SpawnRequest) error {
	inst, err := d.cm.startRQLiteCluster(ctx, req.Cluster, req.Nodes, req.PortBlocks)
	if err != nil {
		return fmt.Errorf("failed to start RQLite cluster: %w", err)
	}
	if req.State != nil {
		req.State.rqlite = inst
	}
	return nil
}

type olricClusterDriver struct{ cm *ClusterManager }

func (d *olricClusterDriver) Name() ServiceName     { return ServiceOlric }
func (d *olricClusterDriver) Scope() Scope          { return tenantSpec(ServiceOlric).Scope }
func (d *olricClusterDriver) PortNeeds() []PortNeed { return tenantSpec(ServiceOlric).PortNeeds }
func (d *olricClusterDriver) Stop(context.Context, string, string) error {
	return nil
}

// Ready waits for this node's tenant Olric to answer.
func (d *olricClusterDriver) Ready(ctx context.Context, ns, node string, ports []int) error {
	hostPort, err := probeTarget(d.cm, node, ports)
	if err != nil {
		return fmt.Errorf("olric readiness for %s on %s: %w", ns, node, err)
	}
	return awaitReady(ctx, readyTimeout, fmt.Sprintf("olric for %s on %s", ns, node), func(ctx context.Context) error {
		return olricReady(ctx, hostPort)
	})
}
func (d *olricClusterDriver) Spawn(ctx context.Context, req SpawnRequest) error {
	inst, err := d.cm.startOlricCluster(ctx, req.Cluster, req.Nodes, req.PortBlocks)
	if err != nil {
		return fmt.Errorf("failed to start Olric cluster: %w", err)
	}
	if req.State != nil {
		req.State.olric = inst
	}
	return nil
}

type gatewayClusterDriver struct{ cm *ClusterManager }

func (d *gatewayClusterDriver) Name() ServiceName     { return ServiceGateway }
func (d *gatewayClusterDriver) Scope() Scope          { return tenantSpec(ServiceGateway).Scope }
func (d *gatewayClusterDriver) PortNeeds() []PortNeed { return tenantSpec(ServiceGateway).PortNeeds }
func (d *gatewayClusterDriver) Stop(context.Context, string, string) error {
	return nil
}

// Ready waits for this node's tenant gateway to report that it can reach both
// rqlite and Olric. This is the probe that makes `ready` mean anything: the
// gateway is the only component that talks to both.
func (d *gatewayClusterDriver) Ready(ctx context.Context, ns, node string, ports []int) error {
	hostPort, err := probeTarget(d.cm, node, ports)
	if err != nil {
		return fmt.Errorf("gateway readiness for %s on %s: %w", ns, node, err)
	}
	return awaitReady(ctx, readyTimeout, fmt.Sprintf("gateway for %s on %s", ns, node), func(ctx context.Context) error {
		return gatewayReady(ctx, hostPort)
	})
}
func (d *gatewayClusterDriver) Spawn(ctx context.Context, req SpawnRequest) error {
	state := req.State
	if state == nil {
		state = &provisionState{}
	}
	inst, err := d.cm.startGatewayCluster(ctx, req.Cluster, req.Nodes, req.PortBlocks, state.rqlite, state.olric)
	if err != nil {
		if strings.Contains(err.Error(), "gateway binary not found") {
			d.cm.logger.Warn("Skipping namespace gateway spawning (binary not available)",
				zap.String("namespace", req.Cluster.NamespaceName),
				zap.Error(err),
			)
			d.cm.logEvent(ctx, req.Cluster.ID, "gateway_skipped", "", "Gateway binary not available, cluster will use main gateway", nil)
			return nil
		}
		return fmt.Errorf("failed to start Gateway cluster: %w", err)
	}
	if req.State != nil {
		req.State.gateway = inst
	}
	return nil
}
