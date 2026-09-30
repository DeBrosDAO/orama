package gateway

import (
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	authhandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/sqlguard"
	"go.uber.org/zap"
)

// The raw-database routes serve whatever database the gateway they reach is
// configured against. On a namespace gateway that is the tenant's own; on the
// gateway that fronts the cluster it is the registry — api_keys,
// principals, grants, refresh_tokens, wireguard_peers, node_agent_tokens,
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
	return ownNamespace(g.cfg) == auth.LobbyNamespace
}

// configureORMGateway mounts the ORM gateway at its base path and installs the
// SQL guard for the database this gateway serves.
func (g *Gateway) configureORMGateway() {
	g.ormHTTP.BasePath = ormBasePath
	g.ormHTTP.SQLGuard = g.ormSQLGuard()
}

// ormSQLGuard is the filter the ORM gateway runs tenant SQL through: the same
// one a function's SQL goes through, so raw SQL over /v1/rqlite cannot name the
// platform tables that share a namespace's database (grants, ipfs_content_ownership,
// namespaces, deployments and the rest of sqlguard's list).
//
// Who is exempt is decided by which database the gateway serves, never by who
// is calling. The cluster gateway serves the registry, and requireOperatorForCoreRegistry
// already refuses every /v1/rqlite request on it that does not come from an
// operator, whose job there is precisely to read and write those tables. A
// namespace gateway serves a tenant's database, and everyone reaching it is a
// tenant — the owner and an admin included. An admin who could write api_keys
// directly would bypass the key-minting path and its scope checks, and one who
// could write grants would bypass the owner-only transfer. Nothing is exempt on
// that side, and a gateway whose configuration is missing is treated as one
// (servesCoreRegistry is false without a config), so the failure mode is refusal.
func (g *Gateway) ormSQLGuard() rqlite.SQLGuard {
	if g.servesCoreRegistry() {
		return nil
	}
	return sqlguard.Check
}

// functionDatabaseNamespace is the namespace whose functions may use a
// gateway's database, or "" when no function may.
//
// The cluster registry is never a function's database (bugboard #427). Its
// namespace-placed tables hold every tenant's rows side by side, which a SQL
// denylist cannot tell apart, and the lobby namespace it nominally belongs to
// owns nothing — grants in it were handed to whichever wallet signed in first.
func functionDatabaseNamespace(cfg *Config) string {
	if ns := ownNamespace(cfg); ns != auth.LobbyNamespace {
		return ns
	}
	return ""
}

// ownNamespace is the namespace a gateway's database belongs to: its
// client_namespace, and for the cluster gateway the lobby's name, "default".
//
// The cluster gateway's client namespace was "default" and is now "index".
// Everything keyed off this function — the registry guard above all — read the
// renamed cluster gateway as a tenant's, and stopped keeping tenant keys off
// the cluster registry's raw-database routes.
func ownNamespace(cfg *Config) string {
	if cfg == nil || !servesNamedNamespace(cfg.ClientNamespace) {
		return auth.LobbyNamespace
	}
	return strings.TrimSpace(cfg.ClientNamespace)
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
		forbidden(w, CodeOperatorRequired,
			"this gateway's database is the cluster registry, which only an operator "+
				"may read or write. Your namespace's own database is at "+
				g.namespaceGatewayHint(r)+r.URL.Path, nil)
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

// namespaceGatewayHost is the public host of a namespace gateway,
// ns-<namespace>.<base domain>, or "" for the cluster gateway and for a gateway
// with no base domain.
func namespaceGatewayHost(cfg *Config) string {
	if cfg == nil || !servesNamedNamespace(cfg.ClientNamespace) {
		return ""
	}
	base := strings.TrimSpace(cfg.BaseDomain)
	if base == "" {
		return ""
	}
	return "ns-" + ownNamespace(cfg) + "." + base
}

// bindNamespaceSignIn makes a namespace gateway's sign-in messages name its
// public host. See authhandlers.Handlers.origin.
func bindNamespaceSignIn(h *authhandlers.Handlers, cfg *Config) {
	if host := namespaceGatewayHost(cfg); host != "" {
		h.SetPublicHost(host)
	}
}
