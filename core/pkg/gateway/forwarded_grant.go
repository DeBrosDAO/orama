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
// are its authority on a route that does not itself resolve a grant, so a key
// never needs one here. A wallet's authority is its grant: its role decides
// what it reaches, on the data plane as much as on the control plane. A reader
// holds none of the data plane, and a grant narrowed to part of it reaches only
// that part, so the grant is resolved for every wallet session on a route that
// asks for a permission, and only the grant says what the wallet holds.
//
// A wallet with no grant in the namespace holds the data plane, as every
// signed-in user always has. The same question applies to a wallet that called
// this gateway directly.
//
// Cost: the data plane is the hot path, so its grant is read through a short
// cache (narrowed_grant.go) — registry round trips once per wallet per
// namespace per narrowedGrantTTL, and a map hit on every other request. A
// control route, where a stale answer would be a stale refusal, reads it live.
func (g *Gateway) forwardedCallerNeedsGrant(r *http.Request, policy routepolicy.Policy) bool {
	if policy.Domain == "" {
		return false
	}
	claims, _ := r.Context().Value(ctxKeyJWT).(*auth.JWTClaims)
	if claims == nil || strings.TrimSpace(claims.Sub) == "" {
		return false
	}
	isKey := subjectOwnerType(strings.TrimSpace(claims.Sub)) == "api_key"
	// A key's scopes already answer a route that does not resolve a grant.
	// A workload's do not: its token carries its role's scopes, and the
	// selector its grant narrows them with lives only on the grant.
	// An owned route still looks the grant up: the selector lives on it.
	if isKey && !policy.Ownership {
		return false
	}
	if !g.callerPermissions(r).PermitsDomain(auth.Domain(policy.Domain), auth.Action(policy.Action)) {
		return true
	}
	// The data plane reaches the route, but only a wallet's grant says whether
	// its role holds any of it, and a key's scopes were read from its row.
	return !isKey
}
