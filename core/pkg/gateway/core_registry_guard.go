package gateway

import (
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"go.uber.org/zap"
)

// The raw-database routes serve whatever database the gateway they reach is
// configured against. On a namespace gateway that is the tenant's own; on the
// gateway that fronts the cluster it is the registry — api_keys,
// namespace_ownership, refresh_tokens, wireguard_peers, node_agent_tokens,
// invite_tokens.
//
// Reaching them needed the admin grant and ownership of *some* namespace, and
// the cross-namespace check that would have caught the mismatch runs only when
// the gateway serves a named namespace, which the cluster gateway does not. So
// any tenant's admin key could export the registry, or import over it.
//
// The registry is an operator's to read. A tenant's own database is reached
// through their namespace gateway, which is where the same routes do what they
// say.

// coreRegistryPaths are the routes that read or write the raw database.
func isCoreRegistryPath(path string) bool {
	return path == "/rqlite" || path == "/v1/rqlite" || strings.HasPrefix(path, "/v1/rqlite/")
}

// servesCoreRegistry reports whether this gateway's database is the cluster
// registry rather than one tenant's.
//
// The cluster gateway runs with client_namespace "default" (see
// environments/templates/gateway.yaml); a namespace gateway is spawned with
// its own name.
func (g *Gateway) servesCoreRegistry() bool {
	if g.cfg == nil {
		return false
	}
	ns := strings.TrimSpace(g.cfg.ClientNamespace)
	return ns == "" || ns == "default"
}

// requireOperatorForCoreRegistry refuses a non-operator on the raw-database
// routes of the gateway that serves the registry.
//
// It reports whether the request may continue; a false return means the
// response has been written.
func (g *Gateway) requireOperatorForCoreRegistry(w http.ResponseWriter, r *http.Request) bool {
	if !isCoreRegistryPath(r.URL.Path) || !g.servesCoreRegistry() {
		return true
	}

	wallet := operator.WalletFromRequest(r, g.ormClient)
	isOperator, err := operator.IsOperator(r.Context(), g.ormClient, wallet)
	if err != nil {
		// Not knowing whether someone is an operator is not permission to hand
		// them the registry.
		g.logger.ComponentError("gateway", "could not read the operator list", zap.Error(err))
		writeError(w, http.StatusServiceUnavailable,
			"cannot verify operator status right now; the registry did not answer")
		return false
	}
	if !isOperator {
		g.logger.ComponentWarn("gateway", "refused raw database access to the cluster registry",
			zap.String("wallet", wallet), zap.String("path", r.URL.Path))
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "this gateway's database is the cluster registry, which only an operator " +
				"may read or write. Your namespace's own database is at " +
				g.namespaceGatewayHint(r) + r.URL.Path,
			"code": operator.ErrCodeNotAnOperator,
		})
		return false
	}
	return true
}

// namespaceGatewayHint is the base URL of the caller's own namespace gateway,
// so the refusal above names where the request should have gone.
func (g *Gateway) namespaceGatewayHint(r *http.Request) string {
	namespace := ""
	if v := r.Context().Value(CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok {
			namespace = strings.TrimSpace(s)
		}
	}
	if namespace == "" || namespace == "default" {
		return "your namespace gateway, "
	}

	base := ""
	if g.cfg != nil {
		base = strings.TrimSpace(g.cfg.BaseDomain)
	}
	if base == "" {
		return "ns-" + namespace + ".<base domain>"
	}
	return "https://ns-" + namespace + "." + base
}
