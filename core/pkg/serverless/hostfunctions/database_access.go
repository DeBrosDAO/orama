package hostfunctions

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// A gateway's database handle belongs to one namespace: the gateway's own
// client_namespace. On a namespace gateway that is the tenant's rqlite. On the
// cluster gateway (client_namespace "default") it is the cluster registry, and
// the registry's namespace-placed tables — namespaces, deployments,
// deployment_domains, functions, triggers — hold every tenant's rows side by
// side. The SQL guard is a denylist of platform tables; it cannot tell one
// tenant's row from another's.
//
// Bugboard #427: the cluster gateway ran functions for any namespace against
// that registry, so a tenant's function could rename another tenant's
// namespace or repoint its deployments. The cluster gateway now proxies tenant
// function traffic to the tenant's own gateway and runs no tenant function.
// Independently of both, a database host call is served only for a function
// of the namespace the database is configured for (dbNamespace) — and on the
// cluster gateway that is none: the registry is no function's database.

// checkDatabaseAccess is the first thing every database host function asks:
// is there a database, and does it belong to the namespace of the function
// making this call?
//
// A call outside any invocation — a warm-pool module's _initialize — has no
// namespace, so it is refused: the function it came from cannot be known.
func (h *HostFunctions) checkDatabaseAccess(ctx context.Context, fn string) error {
	if h.db == nil {
		return &serverless.HostFunctionError{Function: fn, Cause: serverless.ErrDatabaseUnavailable}
	}
	ns := ""
	if cur := h.currentInvocationContext(ctx); cur != nil {
		ns = cur.Namespace
	}
	if ns == "" {
		return &serverless.HostFunctionError{Function: fn, Cause: fmt.Errorf(
			"%w: the database is available only inside an invocation, from handle()",
			serverless.ErrDatabaseOfAnotherNamespace)}
	}
	if h.dbNamespace == "" {
		return &serverless.HostFunctionError{Function: fn, Cause: fmt.Errorf(
			"%w: this gateway's database is the cluster registry, which no function may use; "+
				"a function's database is on its namespace gateway, https://ns-%s.<base domain>",
			serverless.ErrDatabaseOfAnotherNamespace, ns)}
	}
	if ns != h.dbNamespace {
		return &serverless.HostFunctionError{Function: fn, Cause: fmt.Errorf(
			"%w: the function runs in namespace %q and this gateway's database is %q's; "+
				"deploy and invoke it through its own namespace gateway, https://ns-%s.<base domain>",
			serverless.ErrDatabaseOfAnotherNamespace, ns, h.dbNamespace, ns)}
	}
	return nil
}
