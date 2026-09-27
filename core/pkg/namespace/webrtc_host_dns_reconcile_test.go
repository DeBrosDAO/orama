package namespace

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// Bugboard #286. The boot-time namespace-host DNS ensure needs this node's public
// IP from the MAIN rqlite, which is not up that early in boot, so it routinely
// loses that race and gives up. A node then serves 200 while absent from the
// `ns-<namespace>` round-robin for minutes after every restart, with nothing
// reporting it. During the 0.122.105 rolling upgrade the lag compounded far enough
// that both devnet namespaces briefly resolved to a single node while two healthy
// nodes sat idle.
//
// The periodic sweep re-asserts it — but only while the local gateway is actually
// answering, because advertising a node whose gateway is down just moves the
// outage rather than fixing it.

// dnsProbeCM builds a ClusterManager whose public-IP lookup returns ip.
func dnsProbeCM(t *testing.T, ip string) (*ClusterManager, *recoveryMockDB) {
	t.Helper()
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if strings.Contains(query, "dns_nodes") {
			appendToSlice(dest, map[string]any{"IP": ip})
		}
		return nil
	}
	logger := zap.NewNop()
	return &ClusterManager{
		db:         db,
		logger:     logger,
		dnsManager: NewDNSRecordManager(db, "orama-devnet.network", logger),
	}, db
}

// dnsUpserts counts writes that touch dns_records.
func dnsUpserts(db *recoveryMockDB) int {
	n := 0
	for _, ec := range db.getExecCalls() {
		if strings.Contains(ec.Query, "dns_records") {
			n++
		}
	}
	return n
}

func servingGatewayPort(t *testing.T, status int, body string) int {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
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

func TestNamespaceGatewayProbe_usesTheWireGuardAddress(t *testing.T) {
	got, ok := namespaceGatewayProbe(&ClusterLocalState{
		LocalIP:    "10.0.0.1",
		LocalPorts: ClusterLocalStatePorts{GatewayHTTPPort: 10004},
	})
	if !ok || got != "10.0.0.1:10004" {
		t.Fatalf("probe = %q ok=%v, want 10.0.0.1:10004", got, ok)
	}
}

func TestNamespaceGatewayProbe_loopbackWhenTheStateHasNoAddress(t *testing.T) {
	got, ok := namespaceGatewayProbe(&ClusterLocalState{
		LocalPorts: ClusterLocalStatePorts{GatewayHTTPPort: 10004},
	})
	if !ok || got != "127.0.0.1:10004" {
		t.Fatalf("probe = %q ok=%v, want 127.0.0.1:10004", got, ok)
	}
}

// The reproduction's fix: gateway serving, so the record is asserted.
func TestEnsureNamespaceHostRecordIfServing_advertisesWhenGatewayAnswers(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"status": "ok", "services": map[string]string{}})
	port := servingGatewayPort(t, http.StatusOK, string(body))

	cm, db := dnsProbeCM(t, "203.0.113.10")
	state := &ClusterLocalState{
		NamespaceName: "anchat-v2",
		LocalPorts:    ClusterLocalStatePorts{GatewayHTTPPort: port},
	}

	cm.ensureNamespaceHostRecordIfServing(context.Background(), state)

	if dnsUpserts(db) == 0 {
		t.Error("no dns_records write — a healthy, serving node stays out of the ns-<ns> round-robin")
	}
}

// The guard: a node whose gateway is down must NOT be advertised, or clients
// round-robin onto a dead endpoint.
func TestEnsureNamespaceHostRecordIfServing_skipsWhenGatewayIsDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close() // nothing listening now

	cm, db := dnsProbeCM(t, "203.0.113.10")
	state := &ClusterLocalState{
		NamespaceName: "anchat-v2",
		LocalPorts:    ClusterLocalStatePorts{GatewayHTTPPort: port},
	}

	cm.ensureNamespaceHostRecordIfServing(context.Background(), state)

	if dnsUpserts(db) != 0 {
		t.Error("advertised a node whose gateway is not answering — clients would round-robin onto a dead endpoint")
	}
}

// TCP-open is not "serving". A gateway that accepts connections but returns
// 404 / starting would be withdrawn by the HTTP health loop and put back by
// a TCP probe — the two must not fight.
func TestEnsureNamespaceHostRecordIfServing_skipsWhenGatewayAcceptsTCPButIsNotReady(t *testing.T) {
	port := servingGatewayPort(t, http.StatusNotFound, "not this namespace")

	cm, db := dnsProbeCM(t, "203.0.113.10")
	state := &ClusterLocalState{
		NamespaceName: "anchat-v2",
		LocalPorts:    ClusterLocalStatePorts{GatewayHTTPPort: port},
	}

	cm.ensureNamespaceHostRecordIfServing(context.Background(), state)

	if dnsUpserts(db) != 0 {
		t.Error("advertised a node whose gateway accepts TCP but is not ready")
	}
}

// A state file with no gateway port recorded must be a no-op rather than a panic
// or a bogus probe against port 0.
func TestEnsureNamespaceHostRecordIfServing_noPortIsNoop(t *testing.T) {
	cm, db := dnsProbeCM(t, "203.0.113.10")
	state := &ClusterLocalState{NamespaceName: "ns"}

	cm.ensureNamespaceHostRecordIfServing(context.Background(), state)

	if dnsUpserts(db) != 0 {
		t.Error("wrote a DNS record for a state file with no gateway port")
	}
}

// No public IP recorded: skip rather than write a record with an empty value.
func TestEnsureNamespaceHostRecordIfServing_skipsWithoutPublicIP(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"status": "ok", "services": map[string]string{}})
	port := servingGatewayPort(t, http.StatusOK, string(body))

	cm, db := dnsProbeCM(t, "") // lookup yields nothing
	state := &ClusterLocalState{
		NamespaceName: "anchat-v2",
		LocalPorts:    ClusterLocalStatePorts{GatewayHTTPPort: port},
	}

	cm.ensureNamespaceHostRecordIfServing(context.Background(), state)

	if dnsUpserts(db) != 0 {
		t.Error("wrote a DNS record without a public IP")
	}
}
