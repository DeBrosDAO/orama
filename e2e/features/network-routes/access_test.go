//go:build e2e_fleet

package networkroutes

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// TestNetworkRoutes_noCredentialRefused: every network route is the
// control plane, so no credential is 401 AUTH_MISSING, and a token that does
// not verify is 401 too, through the public name of every node.
func TestNetworkRoutes_noCredentialRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, node := range f.State.Nodes {
		c := harness.GW(t).PinTo(node.PublicIP)
		for _, path := range every {
			expectCode(t, node.Name+" "+path+" with no credential", call(t, c, path, tenancy.Cred{}, nil, nil),
				http.StatusUnauthorized, tenancy.CodeMissing)
			if r := call(t, c, path, tenancy.Cred{Bearer: "e2e.garbage.token"}, nil, nil); r.Status != http.StatusUnauthorized {
				t.Errorf("%s %s with a garbage token: HTTP %d, want 401: %.300s", node.Name, path, r.Status, r.Body)
			}
		}
	}
}

// principal is a credential and the refusal the network routes owe it.
type principal struct {
	name   string
	cred   tenancy.Cred
	status int
	code   string
}

// nonOperators is everyone who is not an operator of the cluster: the
// namespace's owner and its admin key hold the operator domain through the
// owner grant and are refused by the operator list (NOT_AN_OPERATOR); a
// runtime member, a storage key and a lobby session never reach the domain
// (INSUFFICIENT_SCOPE, the scope gate in core/pkg/gateway/scope_policy.go).
func nonOperators(t *testing.T, n *ns.Namespace) []principal {
	t.Helper()
	f := harness.Fleet(t)
	lobby := gw.NewUser(t, f, gw.LobbyNamespace)
	return []principal{
		{"namespace owner", tenancy.Owner(n), http.StatusForbidden, tenancy.CodeNotOperator},
		{"admin API key", tenancy.Cred{APIKey: tenancy.APIKey(t, n, scopeAdmin)}, http.StatusForbidden, tenancy.CodeNotOperator},
		{"runtime member", tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleRuntime).Token()}, http.StatusForbidden, tenancy.CodeScope},
		{"storage API key", tenancy.Cred{APIKey: tenancy.APIKey(t, n, scopeStorage)}, http.StatusForbidden, tenancy.CodeScope},
		{"lobby session", tenancy.Cred{Bearer: lobby.Token()}, http.StatusForbidden, tenancy.CodeScope},
	}
}

// TestNetworkRoutes_nonOperatorsRefused: the peer map and topology mutation
// are an operator's (docs/whitepaper/technical-reference/appendices/i-api-surface.md: "an operator's (operator grant and
// the operator list)"; route_policy.go: "The handlers additionally require
// the caller's wallet to be on the operator list"). A fresh wallet owns a
// namespace in open mode, so its owner grant alone must not reach connect or
// disconnect; the body is an empty object, so a missing gate shows as a 400
// and moves nothing.
func TestNetworkRoutes_nonOperatorsRefused(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	c := harness.GW(t)
	for _, p := range nonOperators(t, n) {
		for _, path := range every {
			expectCode(t, p.name+" on "+path, call(t, c, path, p.cred, nil, nil), p.status, p.code)
		}
	}
}

// networkRoutesOtherNamespaceCredentialRefused: an operator's session
// belongs to its namespace; on another namespace's host it is
// NAMESPACE_MISMATCH before any handler runs.
func networkRoutesOtherNamespaceCredentialRefused(t *testing.T, opNS *ns.Namespace) {
	op := opNS
	other := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	for _, path := range every {
		expectCode(t, "an operator's session on another namespace's "+path,
			call(t, other.Client, path, tenancy.Owner(op), nil, nil), http.StatusForbidden, tenancy.CodeMismatch)
	}
}

// forgedStamp is a coordination MAC of the right shape that no key made.
// Set canonicalizes the name, so Get finds it again (a literal map key
// spelled "...-MAC" would not be found by Get's "...-Mac").
func forgedStamp(header string) http.Header {
	h := http.Header{}
	h.Set(header, fmt.Sprintf("%d.%s", time.Now().Unix(), strings.Repeat("0", macHexLen)))
	return h
}

// stampHeaders are the stamp generations a node can carry: v1, which a mixed
// fleet still writes, and v3, the only one written once every node is nonced.
var stampHeaders = []string{coordinationHeader, coordinationV3Header}

// TestNetworkDetail_forgedCoordinationStampIs404: a request carrying a
// coordination stamp (v1 or v3) is judged by the stamp alone, and one that does not
// verify is 404 — from the internet (not the overlay) with or without a
// valid session beside it, and from a node on the overlay (network_detail_auth.go).
func TestNetworkDetail_forgedCoordinationStampIs404(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	c := harness.GW(t)
	for _, header := range stampHeaders {
		for _, path := range detail {
			for what, who := range map[string]tenancy.Cred{"no credential": {}, "owner session": tenancy.Owner(n)} {
				if r := call(t, c, path, who, nil, forgedStamp(header)); r.Status != http.StatusNotFound {
					t.Errorf("%s with a forged %s and %s: HTTP %d, want 404: %.300s", path, header, what, r.Status, r.Body)
				}
			}
		}
	}
	from, to := f.State.Nodes[0], f.State.Nodes[1]
	for _, path := range detail {
		for _, header := range stampHeaders {
			stamp := header + ": " + forgedStamp(header).Get(header)
			forged := edge.NodeCurl{URL: edge.OverlayGateway(to, path), Headers: []string{stamp}}.Run(t, f, from)
			if forged.Status != http.StatusNotFound {
				t.Errorf("%s over the overlay with a forged %s: HTTP %d, want 404: %.300s", path, header, forged.Status, forged.Body)
			}
		}
		bare := edge.NodeCurl{URL: edge.OverlayGateway(to, path)}.Run(t, f, from)
		if bare.Status != http.StatusUnauthorized {
			t.Errorf("%s over the overlay with no stamp and no credential: HTTP %d, want 401: %.300s", path, bare.Status, bare.Body)
		}
	}
}
