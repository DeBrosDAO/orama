package namespace

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// Bugboard #277. Provisioning marked a cluster `ready` — and every node row
// `running` — purely because the spawn RPCs returned. On devnet that produced a
// namespace reported ready while 6 of its 9 processes were crash-looping on a port
// collision, RQLite had no quorum, and the surviving node had joined a foreign
// raft group. `orama auth login` printed "✅ Namespace cluster ready!" and the
// namespace was about to be handed to a tenant with credentials.
//
// Health verification is what turns every other provisioning failure into a
// visible one, so these tests pin it.

// listener occupies an address and returns its host/port split.
func listener(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	a := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", a.Port
}

// deadPort returns a port nothing is listening on.
func deadPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func TestVerifyClusterHealthy_passesWhenAllServicesAnswer(t *testing.T) {
	ip, gwPort := listener(t)
	_, rqPort := listener(t)

	cm := &ClusterManager{logger: zap.NewNop(), readyTimeout: 2 * time.Second}
	nodes := []NodeCapacity{{NodeID: "node-1", InternalIP: ip}}
	blocks := []*PortBlock{{GatewayHTTPPort: gwPort, RQLiteHTTPPort: rqPort}}

	if err := cm.verifyClusterHealthy(context.Background(), nodes, blocks); err != nil {
		t.Errorf("verifyClusterHealthy failed while everything was answering: %v", err)
	}
}

// The reproduction: a node whose gateway never came up must fail verification, so
// the cluster is marked failed instead of ready.
func TestVerifyClusterHealthy_failsWhenAGatewayIsDown(t *testing.T) {
	ip, gwPort := listener(t)
	_, rqPort := listener(t)
	downPort := deadPort(t)

	cm := &ClusterManager{logger: zap.NewNop(), readyTimeout: 2 * time.Second}
	nodes := []NodeCapacity{
		{NodeID: "node-1", InternalIP: ip},
		{NodeID: "node-2", InternalIP: ip},
	}
	blocks := []*PortBlock{
		{GatewayHTTPPort: gwPort, RQLiteHTTPPort: rqPort},
		{GatewayHTTPPort: downPort, RQLiteHTTPPort: rqPort},
	}

	err := cm.verifyClusterHealthy(context.Background(), nodes, blocks)
	if err == nil {
		t.Fatal("verifyClusterHealthy passed with a dead gateway — this is how a broken cluster got reported ready")
	}
	if !strings.Contains(err.Error(), "node-2") {
		t.Errorf("error should name the failing node: %v", err)
	}
	if !strings.Contains(err.Error(), "gateway") {
		t.Errorf("error should name the failing service: %v", err)
	}
}

// A crash-looping RQLite must be caught too — that was the actual devnet failure.
func TestVerifyClusterHealthy_failsWhenRQLiteIsDown(t *testing.T) {
	ip, gwPort := listener(t)
	downPort := deadPort(t)

	cm := &ClusterManager{logger: zap.NewNop(), readyTimeout: 2 * time.Second}
	nodes := []NodeCapacity{{NodeID: "node-1", InternalIP: ip}}
	blocks := []*PortBlock{{GatewayHTTPPort: gwPort, RQLiteHTTPPort: downPort}}

	err := cm.verifyClusterHealthy(context.Background(), nodes, blocks)
	if err == nil {
		t.Fatal("verifyClusterHealthy passed with a dead rqlite")
	}
	if !strings.Contains(err.Error(), "rqlite") {
		t.Errorf("error should name rqlite: %v", err)
	}
}

// It must give up rather than hang provisioning forever.
func TestVerifyClusterHealthy_boundedByTimeout(t *testing.T) {
	cm := &ClusterManager{logger: zap.NewNop(), readyTimeout: 2 * time.Second}
	nodes := []NodeCapacity{{NodeID: "node-1", InternalIP: "127.0.0.1"}}
	blocks := []*PortBlock{{GatewayHTTPPort: deadPort(t), RQLiteHTTPPort: deadPort(t)}}

	start := time.Now()
	if err := cm.verifyClusterHealthy(context.Background(), nodes, blocks); err == nil {
		t.Fatal("expected failure")
	}
	elapsed := time.Since(start)
	if elapsed > 30*time.Second {
		t.Errorf("verification took %v, expected to give up promptly at the configured timeout", elapsed)
	}
}

func TestProbeTCP_reflectsRealState(t *testing.T) {
	ip, port := listener(t)
	if err := probeTCP(net.JoinHostPort(ip, strconv.Itoa(port))); err != nil {
		t.Errorf("probeTCP failed on a live listener: %v", err)
	}
	if err := probeTCP(net.JoinHostPort("127.0.0.1", strconv.Itoa(deadPort(t)))); err == nil {
		t.Error("probeTCP succeeded against a dead port")
	}
}
