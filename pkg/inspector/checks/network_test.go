package checks

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestCheckNetwork_HealthyNode(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Network = &inspector.NetworkData{
		InternetReachable: true,
		DefaultRoute:      true,
		WGRouteExists:     true,
		TCPEstablished:    100,
		TCPTimeWait:       50,
		TCPRetransRate:    0.1,
		PingResults:       map[string]bool{"10.0.0.2": true, "10.0.0.3": true},
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckNetwork(data)

	expectStatus(t, results, "network.internet", inspector.StatusPass)
	expectStatus(t, results, "network.default_route", inspector.StatusPass)
	expectStatus(t, results, "network.wg_route", inspector.StatusPass)
	expectStatus(t, results, "network.tcp_established", inspector.StatusPass)
	expectStatus(t, results, "network.tcp_timewait", inspector.StatusPass)
	expectStatus(t, results, "network.tcp_retrans", inspector.StatusPass)
	expectStatus(t, results, "network.wg_mesh_ping", inspector.StatusPass)
}

func TestCheckNetwork_InternetUnreachable(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Network = &inspector.NetworkData{InternetReachable: false}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckNetwork(data)
	expectStatus(t, results, "network.internet", inspector.StatusFail)
}

func TestCheckNetwork_MissingRoutes(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Network = &inspector.NetworkData{DefaultRoute: false, WGRouteExists: false}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckNetwork(data)
	expectStatus(t, results, "network.default_route", inspector.StatusFail)
	expectStatus(t, results, "network.wg_route", inspector.StatusFail)
}

func TestCheckNetwork_TCPConnections(t *testing.T) {
	tests := []struct {
		name   string
		estab  int
		status inspector.Status
	}{
		{"normal", 100, inspector.StatusPass},
		{"high", 6000, inspector.StatusWarn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.Network = &inspector.NetworkData{TCPEstablished: tt.estab}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckNetwork(data)
			expectStatus(t, results, "network.tcp_established", tt.status)
		})
	}
}

func TestCheckNetwork_TCPTimeWait(t *testing.T) {
	tests := []struct {
		name   string
		tw     int
		status inspector.Status
	}{
		{"normal", 50, inspector.StatusPass},
		{"high", 15000, inspector.StatusWarn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.Network = &inspector.NetworkData{TCPTimeWait: tt.tw}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckNetwork(data)
			expectStatus(t, results, "network.tcp_timewait", tt.status)
		})
	}
}

func TestCheckNetwork_TCPRetransmission(t *testing.T) {
	tests := []struct {
		name   string
		rate   float64
		status inspector.Status
	}{
		{"low", 0.5, inspector.StatusPass},
		{"elevated", 6.0, inspector.StatusWarn},
		{"high", 12.0, inspector.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nd := makeNodeData("1.1.1.1", "node")
			nd.Network = &inspector.NetworkData{TCPRetransRate: tt.rate}
			data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
			results := CheckNetwork(data)
			expectStatus(t, results, "network.tcp_retrans", tt.status)
		})
	}
}

func TestCheckNetwork_WGMeshPing(t *testing.T) {
	t.Run("all ok", func(t *testing.T) {
		nd := makeNodeData("1.1.1.1", "node")
		nd.Network = &inspector.NetworkData{
			PingResults: map[string]bool{"10.0.0.2": true, "10.0.0.3": true},
		}
		data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
		results := CheckNetwork(data)
		expectStatus(t, results, "network.wg_mesh_ping", inspector.StatusPass)
	})

	t.Run("some fail", func(t *testing.T) {
		nd := makeNodeData("1.1.1.1", "node")
		nd.Network = &inspector.NetworkData{
			PingResults: map[string]bool{"10.0.0.2": true, "10.0.0.3": false},
		}
		data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
		results := CheckNetwork(data)
		expectStatus(t, results, "network.wg_mesh_ping", inspector.StatusFail)
	})

	t.Run("no pings", func(t *testing.T) {
		nd := makeNodeData("1.1.1.1", "node")
		nd.Network = &inspector.NetworkData{PingResults: map[string]bool{}}
		data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
		results := CheckNetwork(data)
		// No ping results → no wg_mesh_ping check
		if findCheck(results, "network.wg_mesh_ping") != nil {
			t.Error("should not emit wg_mesh_ping when no ping results")
		}
	})
}

func TestCheckNetwork_NilData(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckNetwork(data)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil Network data, got %d", len(results))
	}
}
