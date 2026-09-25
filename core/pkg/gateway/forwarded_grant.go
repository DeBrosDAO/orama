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

// forwardedCallerNeedsGrant reports whether a request the main gateway
// forwarded has to have its grant resolved here.
//
// The proxy hop carries who the caller is — the namespace, a JWT subject, an
// API key's scopes — and not the grant the caller holds. An API key's scopes
// are its authority, so that caller's answer has already arrived. A JWT
// caller's is its grant, so it is looked up here, and only when the route
// needs more than the identity alone reaches: a wallet reaches the data plane
// without one, and the lookup is registry round trips on every publish.
func (g *Gateway) forwardedCallerNeedsGrant(r *http.Request, policy routepolicy.Policy) bool {
	if !policy.Ownership || policy.Domain == "" {
		return false
	}
	claims, _ := r.Context().Value(ctxKeyJWT).(*auth.JWTClaims)
	if claims == nil || strings.TrimSpace(claims.Sub) == "" {
		return false
	}
	return !g.callerPermissions(r).PermitsDomain(auth.Domain(policy.Domain), auth.Action(policy.Action))
}
