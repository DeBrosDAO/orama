package namespace

import (
	"context"
	"net"
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

// The reproduction's fix: gateway serving, so the record is asserted.
func TestEnsureNamespaceHostRecordIfServing_advertisesWhenGatewayAnswers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

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
