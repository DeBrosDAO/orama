package routepolicy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The policy of a request is the policy of the route it matches, and matching
// is the mux's own. A path is not a policy: /v1/storage/ as a prefix said
// "storage grant" for /v1/storage/../../health too.

func testTable() *Table {
	t := NewTable()
	t.Add(Policy{Access: Open}, "/health", "/v1/invoke/")
	t.Add(Policy{Scope: "admin", Ownership: true}, "/v1/functions")
	t.Add(Policy{Scope: "storage", Token: WalletToken}, "/v1/storage/get/")
	t.AddDynamic("/v1/storage/unpin/", func(r *http.Request) Policy {
		if r.Method == http.MethodDelete {
			return Policy{Scope: "storage", Token: AnyToken}
		}
		return Policy{Scope: "storage", Token: WalletToken}
	})
	return t
}

func policyFor(method, path string) Policy {
	return testTable().For(httptest.NewRequest(method, path, nil))
}

func TestTable_exactAndPrefixMatch(t *testing.T) {
	if !policyFor(http.MethodGet, "/health").Access.Anonymous() {
		t.Error("/health is not open")
	}
	if !policyFor(http.MethodPost, "/v1/invoke/greet").Access.Anonymous() {
		t.Error("a path under an open prefix is not open")
	}
	if got := policyFor(http.MethodGet, "/v1/storage/get/Qm1").Scope; got != "storage" {
		t.Errorf("scope = %q, want storage", got)
	}
}

func TestTable_dynamicPolicyDependsOnTheRequest(t *testing.T) {
	if got := policyFor(http.MethodDelete, "/v1/storage/unpin/Qm1").Token; got != AnyToken {
		t.Errorf("DELETE token = %v, want AnyToken", got)
	}
	if got := policyFor(http.MethodPost, "/v1/storage/unpin/Qm1").Token; got != WalletToken {
		t.Errorf("POST token = %v, want WalletToken", got)
	}
}

// A path nothing serves is refused, not opened. The prefix lists this replaces
// answered "public" for several paths that matched no route at all.
func TestTable_unmatchedPathGetsTheClosedPolicy(t *testing.T) {
	for _, path := range []string{"/nope", "/HEALTH", "/health/", "/v1/storage", "/v1/functions/"} {
		p := policyFor(http.MethodGet, path)
		if p.Access.Anonymous() || p.Scope != "" || p.Ownership {
			t.Errorf("%q resolved to %+v, want the closed zero policy", path, p)
		}
	}
}

// A path that only reaches a route by being cleaned resolves to that route, so
// //v1/storage/get/x cannot slip past a requirement by spelling.
// A path the mux would redirect to a route carries that route's policy. The
// redirect is answered under the same rules as its destination, which is the
// only answer that cannot be used to reach one route under another's policy.
func TestTable_aRedirectCarriesItsDestinationsPolicy(t *testing.T) {
	if !policyFor(http.MethodGet, "/v1/invoke").Access.Anonymous() {
		t.Error("/v1/invoke redirects to an open route and must be answered as one")
	}
	if got := policyFor(http.MethodGet, "/v1/storage/get").Scope; got != "storage" {
		t.Errorf("/v1/storage/get redirects into storage but resolved to scope %q", got)
	}
}

func TestTable_matchingIsNotStringPrefixing(t *testing.T) {
	if got := policyFor(http.MethodGet, "/v1//storage/get/Qm1").Scope; got != "storage" {
		t.Errorf("a doubled slash resolved to scope %q, want storage", got)
	}
	if got := policyFor(http.MethodGet, "/v1/storage/get/../../health").Scope; got != "" {
		t.Errorf("a traversal resolved to scope %q; it matches no route", got)
	}
}

func TestTable_declaringAPatternTwicePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("declaring a pattern twice did not panic; the second policy would silently win")
		}
	}()
	NewTable().Add(Policy{Access: Open}, "/health").Add(Policy{Scope: "admin"}, "/health")
}

func TestMux_registeringAnUndeclaredRoutePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a route with no policy was registered; it would be reachable by any credential")
		}
	}()
	NewMux(testTable()).HandleFunc("/v1/secrets", func(http.ResponseWriter, *http.Request) {})
}

func TestMux_servesADeclaredRoute(t *testing.T) {
	mux := NewMux(testTable())
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want the handler to have run", rec.Code)
	}
}

func TestMux_registerAllChecksEveryPatternFirst(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a family containing an undeclared pattern was registered")
		}
	}()
	NewMux(testTable()).RegisterAll([]string{"/health", "/v1/secrets"}, func(*http.ServeMux) {})
}

func TestAccess_anonymous(t *testing.T) {
	if !Open.Anonymous() || !HandlerAuth.Anonymous() {
		t.Error("Open and HandlerAuth must both skip the credential check")
	}
	if Credential.Anonymous() {
		t.Error("the zero access must require a credential; a route nobody decided about is not open")
	}
}
