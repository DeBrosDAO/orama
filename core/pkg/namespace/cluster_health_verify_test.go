package namespace

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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
//
// A bare TCP listener, which accepts connections and speaks no protocol. It is
// what a crash-looping service looks like to a port probe, and the reason
// readiness asks each service a question it has to answer.
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

// serveRQLite stands up something that answers /status and /db/query the way a
// healthy rqlite does.
func serveRQLite(t *testing.T, raftState string) int {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"store":{"raft":{"state":%q}}}`, raftState)
	})
	mux.HandleFunc("/db/query", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"results":[{"columns":["1"],"values":[[1]]}]}`)
	})
	return servePort(t, mux)
}

// serveOlric answers the stats endpoint an Olric readiness probe reads.
func serveOlric(t *testing.T) int {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stats", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"member":{"name":"test"}}`)
	})
	return servePort(t, mux)
}

// serveGateway answers /v1/health with the given per-service states.
func serveGateway(t *testing.T, services map[string]string) int {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		body, _ := json.Marshal(map[string]any{"status": "ok", "services": services})
		_, _ = w.Write(body)
	})
	return servePort(t, mux)
}

func servePort(t *testing.T, h http.Handler) int {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("split %s: %v", srv.URL, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	return port
}

// healthyNode stands up all three services for one node and returns its block.
func healthyNode(t *testing.T) *PortBlock {
	t.Helper()
	return &PortBlock{
		RQLiteHTTPPort:  serveRQLite(t, "Leader"),
		OlricHTTPPort:   serveOlric(t),
		GatewayHTTPPort: serveGateway(t, map[string]string{"rqlite": "ok", "olric": "ok"}),
	}
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
	cm := &ClusterManager{logger: zap.NewNop(), readyTimeout: 2 * time.Second}
	nodes := []NodeCapacity{{NodeID: "node-1", InternalIP: "127.0.0.1"}}
	blocks := []*PortBlock{healthyNode(t)}

	if err := cm.verifyClusterHealthy(context.Background(), nodes, blocks); err != nil {
		t.Errorf("verifyClusterHealthy failed while everything was answering: %v", err)
	}
}

// The check this replaces was a TCP dial, which a crash-looping service passes
// as long as something is bound. Every service here accepts connections and
// answers nothing.
func TestVerifyClusterHealthy_failsOnAPortThatAnswersNothing(t *testing.T) {
	ip, dumbPort := listener(t)

	cm := &ClusterManager{logger: zap.NewNop(), readyTimeout: time.Second}
	nodes := []NodeCapacity{{NodeID: "node-1", InternalIP: ip}}
	blocks := []*PortBlock{{RQLiteHTTPPort: dumbPort, OlricHTTPPort: dumbPort, GatewayHTTPPort: dumbPort}}

	if err := cm.verifyClusterHealthy(context.Background(), nodes, blocks); err == nil {
		t.Fatal("a listening socket that speaks no protocol passed verification")
	}
}

// rqlite binds its HTTP listener long before it elects anything, so a node
// still holding an election is not ready however healthy the port looks.
func TestVerifyClusterHealthy_failsWhileRQLiteIsStillElecting(t *testing.T) {
	cm := &ClusterManager{logger: zap.NewNop(), readyTimeout: time.Second}
	nodes := []NodeCapacity{{NodeID: "node-1", InternalIP: "127.0.0.1"}}
	blocks := []*PortBlock{{
		RQLiteHTTPPort:  serveRQLite(t, "Candidate"),
		OlricHTTPPort:   serveOlric(t),
		GatewayHTTPPort: serveGateway(t, map[string]string{"rqlite": "ok", "olric": "ok"}),
	}}

	err := cm.verifyClusterHealthy(context.Background(), nodes, blocks)
	if err == nil {
		t.Fatal("a node in raft state Candidate passed verification")
	}
	if !strings.Contains(err.Error(), "Candidate") {
		t.Errorf("the error should say what state it saw: %v", err)
	}
}

// The gateway is the only component that talks to both rqlite and Olric, so a
// gateway that cannot reach one of them is the signal that matters most.
func TestVerifyClusterHealthy_failsWhenTheGatewayCannotReachOlric(t *testing.T) {
	cm := &ClusterManager{logger: zap.NewNop(), readyTimeout: time.Second}
	nodes := []NodeCapacity{{NodeID: "node-1", InternalIP: "127.0.0.1"}}
	blocks := []*PortBlock{{
		RQLiteHTTPPort:  serveRQLite(t, "Leader"),
		OlricHTTPPort:   serveOlric(t),
		GatewayHTTPPort: serveGateway(t, map[string]string{"rqlite": "ok", "olric": "unavailable"}),
	}}

	err := cm.verifyClusterHealthy(context.Background(), nodes, blocks)
	if err == nil {
		t.Fatal("a gateway reporting olric unavailable passed verification")
	}
	if !strings.Contains(err.Error(), "olric") {
		t.Errorf("the error should name the dependency: %v", err)
	}
}

// The reproduction: a node whose gateway never came up must fail verification, so
// the cluster is marked failed instead of ready.
func TestVerifyClusterHealthy_failsWhenAGatewayIsDown(t *testing.T) {
	healthy := healthyNode(t)
	broken := healthyNode(t)
	broken.GatewayHTTPPort = deadPort(t)

	cm := &ClusterManager{logger: zap.NewNop(), readyTimeout: time.Second}
	nodes := []NodeCapacity{
		{NodeID: "node-1", InternalIP: "127.0.0.1"},
		{NodeID: "node-2", InternalIP: "127.0.0.1"},
	}
	blocks := []*PortBlock{healthy, broken}

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
	block := healthyNode(t)
	block.RQLiteHTTPPort = deadPort(t)

	cm := &ClusterManager{logger: zap.NewNop(), readyTimeout: time.Second}
	nodes := []NodeCapacity{{NodeID: "node-1", InternalIP: "127.0.0.1"}}
	blocks := []*PortBlock{block}

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
