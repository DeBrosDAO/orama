package gateway

import (
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/routepolicy"
)

// grantDB is the database grants are read from: the cluster registry. `grants`
// and `principals` are cluster-placed tables, so a namespace gateway's own
// rqlite — which g.client is there (bugboard #162) — does not hold them, and a
// lookup against it finds nobody.
func (g *Gateway) grantDB() client.DatabaseClient {
	if g.authClient != nil {
		return g.authClient.Database()
	}
	return g.client.Database()
}

// forwardedCallerNeedsGrant reports whether this caller's grant has to be
// resolved before the scope gate runs.
//
// The proxy hop carries who the caller is — the namespace, a JWT subject, an
// API key's scopes — and not the grant the caller holds. An API key's scopes
// are its authority on a route that does not itself resolve a grant. A wallet's
// authority is its grant, and a wallet with none holds only the data plane, so
// the grant is read when the route asks for something the data plane does not
// have. Cache and publish do not: the lookup is registry round trips on every
// one of those calls, unless the wallet holds a narrowed grant: that is read
// through a short cache (narrowed_grant.go). The same question applies to a wallet that called this
// gateway directly. A control route that does not set Ownership — namespace
// list, deployments, the database — used to skip the read, and the scope gate
// then refused the owner with the data plane's permissions.
func (g *Gateway) forwardedCallerNeedsGrant(r *http.Request, policy routepolicy.Policy) bool {
	if policy.Domain == "" {
		return false
	}
	claims, _ := r.Context().Value(ctxKeyJWT).(*auth.JWTClaims)
	if claims == nil || strings.TrimSpace(claims.Sub) == "" {
		return false
	}
	// A key's scopes already answer a route that does not resolve a grant.
	// An owned route still looks the grant up: the selector lives on it.
	if auth.IsAPIKeySubject(claims.Sub) && !policy.Ownership {
		return false
	}
	if !g.callerPermissions(r).PermitsDomain(auth.Domain(policy.Domain), auth.Action(policy.Action)) {
		return true
	}
	// The data plane reaches the route, but a wallet narrowed to part of it
	// reaches only that part, and only the grant says which.
	return g.callerHoldsNarrowedGrant(r, policy)
}
