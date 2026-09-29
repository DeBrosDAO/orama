//go:build e2e_fleet

package namespaces

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// TestNamespacePorts_blockInRangeAndDisjoint: every member gives a namespace
// one block of five ports inside 10000-10099 (core/pkg/namespace/types.go;
// docs/ARCHITECTURE.md), and two namespaces never share a port on a node.
func TestNamespacePorts_blockInRangeAndDisjoint(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	pair := tenancy.Namespaces(t, f, 2, ns.Options{})
	for _, node := range f.State.Nodes {
		a := tenancy.PortBlock(t, f, node, pair[0].Name)
		b := tenancy.PortBlock(t, f, node, pair[1].Name)
		for _, block := range [][]int{a, b} {
			if block[0] < tenancy.PortRangeStart || block[len(block)-1] > tenancy.PortRangeEnd {
				t.Errorf("%s: block %v is outside %d-%d", node.Name, block, tenancy.PortRangeStart, tenancy.PortRangeEnd)
			}
		}
		for _, p := range a {
			if slices.Contains(b, p) {
				t.Errorf("%s: port %d is in both %s's and %s's blocks", node.Name, p, pair[0].Name, pair[1].Name)
			}
		}
	}
}

// TestNamespaceIsolation_nothingListensPublicly: a tenant's rqlite, Olric and
// gateway listen on the node's WireGuard address, never on every interface and
// never on loopback, where Caddy delivers public traffic and a deployment can
// connect (docs/SECURITY.md "Tenant deployments"; docs/ARCHITECTURE.md), and
// the firewall does not open them.
func TestNamespaceIsolation_nothingListensPublicly(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	for _, node := range f.State.Nodes {
		block := tenancy.PortBlock(t, f, node, n.Name)
		listeners := f.Listeners(t, node)
		fw := f.Firewall(t, node)
		for _, port := range block {
			found := false
			for _, l := range listeners {
				if l.Port != port {
					continue
				}
				found = true
				if l.Addr != node.WGIP {
					t.Errorf("%s: %s port %d listens on %s, want only %s", node.Name, n.Name, port, l.Addr, node.WGIP)
				}
			}
			if !found {
				t.Errorf("%s: nothing listens on %s's port %d", node.Name, n.Name, port)
			}
			if fw.Allows(strconv.Itoa(port) + "/tcp") {
				t.Errorf("%s: the firewall opens %s's port %d", node.Name, n.Name, port)
			}
		}
	}
}

// TestNamespaceIsolation_unitsAreConfined: a tenant gateway writes only its
// own namespace's directory (plus the shared deployment and SQLite trees),
// sees of secrets/ only what is bound into it, and runs as orama with hidden
// processes; Olric cannot see secrets/ at all (core/systemd; docs/SECURITY.md
// "Per-service accounts").
func TestNamespaceIsolation_unitsAreConfined(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	pair := tenancy.Namespaces(t, f, 2, ns.Options{})
	a, b := pair[0], pair[1]
	node := f.State.Nodes[0]
	gwProps := unitProps(t, f, node, tenancy.UnitGateway(a.Name), "User", "ReadWritePaths", "ProtectProc", "NoNewPrivileges", "TemporaryFileSystem")
	if gwProps["User"] != "orama" || gwProps["ProtectProc"] != "invisible" || gwProps["NoNewPrivileges"] != "yes" {
		t.Errorf("%s runs with %v", tenancy.UnitGateway(a.Name), gwProps)
	}
	rw := gwProps["ReadWritePaths"]
	if !strings.Contains(rw, tenancy.NamespacesDir+"/"+a.Name) || strings.Contains(rw, tenancy.NamespacesDir+"/"+b.Name) {
		t.Errorf("%s may write %q; want its own directory and not %s's", tenancy.UnitGateway(a.Name), rw, b.Name)
	}
	if !strings.Contains(gwProps["TemporaryFileSystem"], "/opt/orama/.orama/secrets") {
		t.Errorf("%s sees secrets/ unmasked: %q", tenancy.UnitGateway(a.Name), gwProps["TemporaryFileSystem"])
	}
	olric := unitProps(t, f, node, tenancy.UnitOlric(a.Name), "InaccessiblePaths", "User")
	if !strings.Contains(olric["InaccessiblePaths"], "/opt/orama/.orama/secrets") || olric["User"] != "orama" {
		t.Errorf("%s runs with %v", tenancy.UnitOlric(a.Name), olric)
	}
}

// TestNamespaceIsolation_configFilesArePrivate: the gateway YAML holds the
// namespace's credentials and is 0600 orama; the unit env tree is root's
// (docs/SECURITY.md "Per-service accounts": "A gateway reads its 0600 YAML").
func TestNamespaceIsolation_configFilesArePrivate(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	for _, node := range f.State.Nodes {
		out := f.MustExec(t, node, "stat -c '%a %U %n' "+tenancy.NamespacesDir+"/"+n.Name+"/configs/gateway-*.yaml "+tenancy.UnitEnvDir+"/"+n.Name).Stdout
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 3 {
				t.Fatalf("%s: unexpected stat line %q", node.Name, line)
			}
			if strings.HasSuffix(fields[2], ".yaml") && (fields[0] != "600" || fields[1] != "orama") {
				t.Errorf("%s: %s is %s %s, want 600 orama", node.Name, fields[2], fields[0], fields[1])
			}
			if !strings.HasSuffix(fields[2], ".yaml") && fields[1] != "root" {
				t.Errorf("%s: %s is owned by %s, want root", node.Name, fields[2], fields[1])
			}
		}
		if f.Exec(t, node, "runuser -u nobody -- ls "+tenancy.NamespacesDir+"/"+n.Name+"/configs").Exit == 0 {
			t.Errorf("%s: another user can list %s's configs", node.Name, n.Name)
		}
	}
}

// TestNamespaceIsolation_crossNamespaceCredentialRefused: at A's gateway, B's
// API key is NAMESPACE_MISMATCH naming both namespaces, B's session is not
// served, and neither can read A's members or keys (docs/SECURITY.md).
func TestNamespaceIsolation_crossNamespaceCredentialRefused(t *testing.T) {
	t.Parallel()
	pair := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{})
	a, b := pair[0], pair[1]
	bKey := tenancy.APIKey(t, b, "admin")
	for _, path := range []string{tenancy.PathMembers, tenancy.PathKeys, "/v1/cache/health", "/v1/pubsub/topics"} {
		r := tenancy.Get(t, a.Client, path, tenancy.Cred{APIKey: bKey})
		tenancy.ExpectRefused(t, r, http.StatusForbidden, tenancy.CodeMismatch)
		var body map[string]any
		if err := r.Decode(&body); err != nil || body["credential_namespace"] != b.Name {
			t.Errorf("%s: mismatch does not name %s as the credential's namespace: %s", path, b.Name, r.Body)
		}
		tenancy.ExpectDenied(t, tenancy.Get(t, a.Client, path, tenancy.Owner(b)), "B's session at A's "+path)
	}
}

// unitProps reads systemd properties of unit on node.
func unitProps(t testing.TB, f *fleet.Fleet, node fleet.Node, unit string, props ...string) map[string]string {
	t.Helper()
	out := f.MustExec(t, node, "systemctl show "+unit+" -p "+strings.Join(props, ",")).Stdout
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			m[k] = v
		}
	}
	return m
}
