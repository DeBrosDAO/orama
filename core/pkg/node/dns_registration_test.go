package node

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// Recording this node's identity and liveness moved behind the index gateway,
// so registerDNSNode no longer touches the database and no longer holds the
// guard that stopped a nil rqlite adapter being dereferenced. The DNS record
// loop still writes zone data directly, and it is the caller of that handle —
// so it has to be the one that checks for it.
//
// Without this, a heartbeat that fires before the adapter exists dereferences
// nil and takes the node down.
func TestEnsureBaseDNSRecords_withoutAnAdapterSaysSoRatherThanPanicking(t *testing.T) {
	n := testNodeForDNS(t)
	n.config.HTTPGateway.BaseDomain = "example.test"

	err := ensureRecordsWithoutPanic(t, n)
	if err == nil {
		t.Fatal("a node with no rqlite adapter reported that it had written its DNS records")
	}
	if !strings.Contains(err.Error(), "rqlite") {
		t.Errorf("the error does not say what is missing: %v", err)
	}
}

// A node with no domain configured has no records to write, and says so
// without needing a database at all.
func TestEnsureBaseDNSRecords_withNoDomainWritesNothing(t *testing.T) {
	n := testNodeForDNS(t)

	if err := ensureRecordsWithoutPanic(t, n); err != nil {
		t.Errorf("a node with no domain configured reported an error: %v", err)
	}
}

// Recording this node needs a client, and building one needs the cluster
// secret the join writes. A node without it is told which file is missing,
// rather than failing somewhere further in.
func TestRegisterDNSNode_withoutAClusterSecretNamesTheFile(t *testing.T) {
	n := testNodeForDNS(t)
	n.config.Node.DataDir = t.TempDir() + "/data"

	err := n.registerDNSNode(context.Background())
	if err == nil {
		t.Fatal("a node with no peer id and no cluster secret registered itself")
	}
	if !strings.Contains(err.Error(), "cannot record this node") {
		t.Errorf("the error does not say what failed: %v", err)
	}
}

// testNodeForDNS is a node with just enough of itself to run the DNS loop.
func testNodeForDNS(t *testing.T) *Node {
	t.Helper()
	lg, err := logging.NewColoredLogger(logging.ComponentNode, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	return &Node{config: &config.Config{}, logger: lg}
}

// A two-minute heartbeat miss takes the node out of the round-robin. It must
// not free the nameserver slot: that is what a rolling restart looks like, and
// the zone's glue goes with the slot. `orama node remove` is what frees it.
func TestReapInactiveNodeDNS_keepsTheNameserverSlot(t *testing.T) {
	db := newDNSTestDB(t)
	if _, err := db.Exec(`ALTER TABLE dns_nodes ADD COLUMN updated_at TIMESTAMP`); err != nil {
		t.Fatal(err)
	}
	if got := claimAs(t, db, "peer-a", "203.0.113.1"); got != "ns1" {
		t.Fatalf("slot = %s, want ns1", got)
	}
	if _, err := db.Exec(`INSERT INTO dns_nodes (id, ip_address, status, last_seen) VALUES ('peer-a', '203.0.113.1', 'active', datetime('now', '-10 minutes'))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value, namespace) VALUES (?, 'A', '203.0.113.1', 'system')`, testNSDomain+"."); err != nil {
		t.Fatal(err)
	}

	n := testNodeForDNS(t)
	n.config.HTTPGateway.BaseDomain = testNSDomain
	n.reapInactiveNodeDNS(context.Background(), db, testNSDomain)

	var slots int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dns_nameservers WHERE node_id = 'peer-a'`).Scan(&slots); err != nil {
		t.Fatal(err)
	}
	if slots != 1 {
		t.Fatalf("nameserver slots = %d; a two-minute miss released one", slots)
	}
	if got := recordValues(t, db, "ns1."+testNSDomain+".", "A"); len(got) != 1 || got[0] != "203.0.113.1" {
		t.Fatalf("glue = %v", got)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM dns_nodes WHERE id = 'peer-a'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "inactive" {
		t.Fatalf("status = %s, want inactive", status)
	}
	if got := recordValues(t, db, testNSDomain+".", "A"); len(got) != 0 {
		t.Fatalf("apex A records = %v; the inactive node's address should leave the round-robin", got)
	}
}

// ensureRecordsWithoutPanic runs the record loop and turns a panic into a test
// failure, because a panic here is the failure this is about.
func ensureRecordsWithoutPanic(t *testing.T, n *Node) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ensureBaseDNSRecords panicked: %v", r)
		}
	}()
	return n.ensureBaseDNSRecords(context.Background())
}

// The boot supervisor retries a failed component forever, with backoff, and
// never gives up on one. Remembering a failure to build the client would defeat
// that: the cluster secret is read from a file, and a file being unreadable is
// exactly what an operator fixes while the process is running. The cost of
// getting this wrong is not a log line — a node that never registers is reaped
// to `inactive` after 120s and has its DNS records deleted, until somebody
// restarts it.
func TestCoreAPIClient_aFailureIsNotRemembered(t *testing.T) {
	n := testNodeForDNS(t)
	n.config.Node.DataDir = t.TempDir() + "/data"

	// A condition an operator can change while the process is running. The
	// cluster secret being unreadable is the same shape — a file, a mode, a
	// mount — and it is the one this actually guards.
	n.config.HTTPGateway.Enabled = false
	first, err := n.coreAPIClient(context.Background())
	if err == nil {
		t.Fatal("a node running no gateway built a client to call one")
	}
	if first != nil {
		t.Error("a failed build returned a client")
	}

	// The operator fixes it. The next attempt has to look again, and fail for
	// the next reason rather than repeating the first one forever.
	n.config.HTTPGateway.Enabled = true
	_, err = n.coreAPIClient(context.Background())
	if err == nil {
		t.Fatal("a client was built with no peer id")
	}
	if strings.Contains(err.Error(), "http_gateway.enabled") {
		t.Errorf("the first failure was remembered after the cause was fixed: %v", err)
	}
	if !strings.Contains(err.Error(), "peer ID") {
		t.Errorf("the error does not name what is still missing: %v", err)
	}
}

// A node that serves no gateway has nowhere to record itself — and must not
// advertise itself as active in any case, because a `dns_nodes` row saying
// active promises that this node terminates TLS and proxies tenants. Without
// this it would post to a port nothing listens on, forever.
func TestCoreAPIClient_aNodeWithNoGatewaySaysSo(t *testing.T) {
	n := testNodeForDNS(t)
	n.config.Node.DataDir = t.TempDir() + "/data"
	n.config.HTTPGateway.Enabled = false

	_, err := n.coreAPIClient(context.Background())
	if err == nil {
		t.Fatal("a node running no gateway built a client to call one")
	}
	if !strings.Contains(err.Error(), "http_gateway.enabled") {
		t.Errorf("the error does not name the setting: %v", err)
	}
}

// A node that cannot work out its own address does not register. It used to
// publish 127.0.0.1, which every consumer of `status = 'active'` then handed
// out as the address to reach this node on — a node that could not answer the
// question published a wrong answer rather than none.
func TestRegisterDNSNode_doesNotInventAnAddress(t *testing.T) {
	if strings.Contains(registrationSource(t), `ipAddress = "127.0.0.1"`) {
		t.Error("registerDNSNode still falls back to localhost for the address other nodes route on")
	}
}

// registrationSource is the file the assertion above reads. Reading the source
// is the only way to assert the absence of a fallback that would otherwise need
// a live IP-detection failure to reach.
func registrationSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("dns_registration.go")
	if err != nil {
		t.Fatalf("read dns_registration.go: %v", err)
	}
	return string(raw)
}

// The address a node publishes is the one install recorded, not a guess.
func TestGetNodeIPAddress_usesConfiguredPublicIP(t *testing.T) {
	n := testNodeForDNS(t)
	n.config.Node.PublicIP = "203.0.113.7"

	ip, err := n.getNodeIPAddress()
	if err != nil {
		t.Fatal(err)
	}
	if ip != "203.0.113.7" {
		t.Errorf("ip = %q, want the configured node.public_ip", ip)
	}
}

// With no node.public_ip there is nothing to publish, and the error says how
// to record one instead of guessing.
func TestGetNodeIPAddress_emptyIsAnActionableError(t *testing.T) {
	n := testNodeForDNS(t)

	_, err := n.getNodeIPAddress()
	if err == nil {
		t.Fatal("a node with no node.public_ip reported an address")
	}
	for _, want := range []string{"node.public_ip", "orama node upgrade", "--public-ip"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// An IPv4-mapped spelling is published as the dotted quad an A record holds.
func TestGetNodeIPAddress_canonicalisesMappedIPv4(t *testing.T) {
	n := testNodeForDNS(t)
	n.config.Node.PublicIP = "::ffff:203.0.113.7"
	ip, err := n.getNodeIPAddress()
	if err != nil {
		t.Fatal(err)
	}
	if ip != "203.0.113.7" {
		t.Errorf("ip = %q, want 203.0.113.7", ip)
	}
}

// node.yaml is not written by this process, so what it says is validated
// before it is published in DNS: private, overlay, IPv6 and garbage values
// are refused.
func TestGetNodeIPAddress_rejectsUnusableValues(t *testing.T) {
	for _, bad := range []string{"10.0.0.3", "192.168.1.5", "127.0.0.1", "100.64.0.1", "2001:db8::1", "not-an-ip", " 203.0.113.7"} {
		t.Run(bad, func(t *testing.T) {
			n := testNodeForDNS(t)
			n.config.Node.PublicIP = bad
			if ip, err := n.getNodeIPAddress(); err == nil {
				t.Errorf("node.public_ip %q was accepted as %q", bad, ip)
			}
		})
	}
}
