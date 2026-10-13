//go:build e2e_fleet

package tenancy

import (
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Where a node keeps a namespace (core/pkg/namespace systemd_spawner.go,
// core/pkg/systemd/manager.go envFilePath).
const (
	// NamespacesDir holds <ns>/configs, <ns>/gateway (state), rqlite data.
	NamespacesDir = "/opt/orama/.orama/data/namespaces"
	// UnitEnvDir holds <ns>/<service>.env, root-owned.
	UnitEnvDir = "/var/lib/orama-unit-env"
	// Tenant port range and block (core/pkg/namespace/types.go).
	PortRangeStart = 10000
	PortRangeEnd   = 10099
	PortsPerBlock  = 5
	// MaxPerNode is how many tenant blocks fit in the range.
	MaxPerNode = (PortRangeEnd - PortRangeStart + 1) / PortsPerBlock
)

// Tenant units of a namespace (core/systemd).
func UnitRQLite(name string) string  { return "orama-namespace-rqlite@" + name + ".service" }
func UnitOlric(name string) string   { return "orama-namespace-olric@" + name + ".service" }
func UnitGateway(name string) string { return "orama-namespace-gateway@" + name + ".service" }

// TenantUnits are the three a namespace runs on every member.
func TenantUnits(name string) []string {
	return []string{UnitRQLite(name), UnitOlric(name), UnitGateway(name)}
}

// portLine matches "...:<port>" at the end of an address line and
// "bindPort: <port>" in the Olric YAML.
var portLine = regexp.MustCompile(`(?m)(?::|bindPort:\s*)(\d+)\s*$`)

// PortBlock reads the ports a namespace's services were given on node: rqlite
// HTTP and raft from its env file, Olric's two bind ports from its YAML and
// the gateway's listen address from its YAML. Only these keys are read: the
// gateway YAML also holds credentials, which must not reach evidence.
func PortBlock(t testing.TB, f *fleet.Fleet, node fleet.Node, name string) []int {
	t.Helper()
	cfg := NamespacesDir + "/" + name + "/configs"
	cmd := "grep -hE '^(HTTP_ADDR|RAFT_ADDR)=' " + UnitEnvDir + "/" + name + "/rqlite.env; " +
		"grep -hE '^\\s*bindPort:' " + cfg + "/olric-*.yaml; " +
		"grep -hE '^listen_addr:' " + cfg + "/gateway-*.yaml"
	out := f.MustExec(t, node, cmd).Stdout
	var ports []int
	for _, m := range portLine.FindAllStringSubmatch(out, -1) {
		p, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: port %q of %s: %v", node.Name, m[1], name, err)
		}
		ports = append(ports, p)
	}
	sort.Ints(ports)
	if len(ports) != PortsPerBlock {
		t.Fatalf("%s: found %d ports for %s, want %d:\n%s", node.Name, len(ports), name, PortsPerBlock, out)
	}
	return ports
}
