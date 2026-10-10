package gateway

import (
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/sqlguard"
)

// deploymentRegistryScope names the deployment family in the errors a refused
// statement carries.
const deploymentRegistryScope = "deployment registry"

// deploymentRegistryTables are the registry tables the deployment family
// (the port allocator, the home-node and replica managers, the deployment
// service and its handlers, and the workload-token minter) reads and writes.
//
// The set is what those packages' statements name, and
// TestDeploymentRegistryTables_coverEveryStatementTheFamilyIssues holds the two
// together: a statement added against another table fails that test rather than
// failing on a namespace gateway in production.
var deploymentRegistryTables = []string{
	"deployments",
	"deployment_replicas",
	"deployment_domains",
	"deployment_events",
	"deployment_history",
	"deployment_health_checks",
	"home_node_assignments",
	"port_allocations",
	"dns_nodes",
	"dns_records",
	"global_deployment_subdomains",
}

// deploymentRegistryGuard is the SQLGuard of the deployment family's registry
// handle: it admits a statement only if every table it names is one of
// deploymentRegistryTables.
func deploymentRegistryGuard() rqlite.SQLGuard {
	allowed := make(map[string]bool, len(deploymentRegistryTables))
	for _, table := range deploymentRegistryTables {
		allowed[table] = true
	}
	known := rqlite.KnownTables()
	return func(query string) error {
		return sqlguard.CheckScope(query, allowed, known)
	}
}

// scopedDeploymentRegistry is the registry handle the deployment family gets.
//
// On a namespace gateway it is the cluster registry reached through a guard
// that admits only the deployment tables. The credential behind it is still the
// cluster's one RQLite user, and RQLite cannot restrict a user to tables, so the
// guard is a check in this process on the statements the family issues, not a
// database permission: it keeps a bug in the family from reaching identity,
// secrets or topology tables through this handle. docs/whitepaper/technical-reference/vol1/05-privilege-and-filesystem-trust.md says what it
// does and does not cover.
//
// On the gateway that fronts the cluster the family keeps the registry itself:
// that gateway is the registry's own operator, with the whole schema in hand.
func scopedDeploymentRegistry(cfg *Config, deps *Dependencies) rqlite.Client {
	registry := deploymentRegistry(deps)
	if registry == nil || !isNamespaceGateway(cfg) {
		return registry
	}
	return rqlite.NewGuardedClient(registry, deploymentRegistryScope, deploymentRegistryGuard())
}
