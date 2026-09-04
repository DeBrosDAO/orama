package gateway

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

const testClusterSecret = "cluster-secret-for-tests"

func testHopKey(t *testing.T) []byte {
	t.Helper()
	key, err := internalAuthKey(testClusterSecret)
	if err != nil {
		t.Fatalf("derive hop key: %v", err)
	}
	return key
}

// forgedRequest is what anyone on the internet could send: the headers the
// gateway trusts, from the address Caddy proxies from.
func forgedRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set(HeaderInternalAuthValidated, "true")
	r.Header.Set(HeaderInternalAuthNamespace, "someone-elses-namespace")
	r.Header.Set(HeaderInternalAuthJWTSub, "0xvictim")
	r.Header.Set(HeaderInternalAuthScopes, "admin")
	return r
}

func signedRequest(t *testing.T, key []byte, method, path string) *http.Request {
	t.Helper()
	r := forgedRequest(method, path)
	if err := signInternalAuthHeaders(key, r.Header, method, path, time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return r
}

func TestVerifyInternalAuthHeaders_acceptsAGenuineHop(t *testing.T) {
	key := testHopKey(t)
	r := signedRequest(t, key, http.MethodGet, "/v1/db/query")

	if !verifyInternalAuthHeaders(key, r, time.Now()) {
		t.Fatal("a hop this gateway signed was not accepted")
	}
}

// The whole vulnerability: these headers arriving from 127.0.0.1, which is
// where every public request arrives from.
func TestVerifyInternalAuthHeaders_rejectsForgedHeaders(t *testing.T) {
	key := testHopKey(t)

	if verifyInternalAuthHeaders(key, forgedRequest(http.MethodGet, "/v1/db/query"), time.Now()) {
		t.Fatal("headers with no MAC were accepted from loopback")
	}
}

func TestVerifyInternalAuthHeaders_rejectsAMACFromAnotherCluster(t *testing.T) {
	ours := testHopKey(t)
	theirs, err := internalAuthKey("a-different-cluster-secret")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	r := signedRequest(t, theirs, http.MethodGet, "/v1/db/query")
	if verifyInternalAuthHeaders(ours, r, time.Now()) {
		t.Fatal("a MAC from a different cluster secret was accepted")
	}
}

// Every field the receiving gateway trusts is covered, so none can be edited
// after the MAC was stamped.
func TestVerifyInternalAuthHeaders_rejectsEveryEditedField(t *testing.T) {
	key := testHopKey(t)

	for _, edit := range []struct {
		what   string
		header string
		value  string
	}{
		{"the namespace", HeaderInternalAuthNamespace, "another-namespace"},
		{"the JWT subject", HeaderInternalAuthJWTSub, "0xattacker"},
		{"the custom claims", HeaderInternalAuthJWTCustom, "eyJhIjoiYiJ9"},
		{"the grant set", HeaderInternalAuthScopes, "admin,storage"},
	} {
		r := signedRequest(t, key, http.MethodGet, "/v1/db/query")
		r.Header.Set(edit.header, edit.value)
		if verifyInternalAuthHeaders(key, r, time.Now()) {
			t.Errorf("%s was changed after signing and the MAC still verified", edit.what)
		}
	}
}

// A MAC captured on a harmless read must not authorize a write.
func TestVerifyInternalAuthHeaders_rejectsReplayOntoAnotherRequest(t *testing.T) {
	key := testHopKey(t)
	signed := signedRequest(t, key, http.MethodGet, "/v1/db/query")

	replayed := httptest.NewRequest(http.MethodDelete, "/v1/db/query", nil)
	for h, v := range signed.Header {
		replayed.Header[h] = v
	}
	if verifyInternalAuthHeaders(key, replayed, time.Now()) {
		t.Error("a MAC signed for GET authorized a DELETE")
	}

	otherPath := httptest.NewRequest(http.MethodGet, "/v1/namespace/keys", nil)
	for h, v := range signed.Header {
		otherPath.Header[h] = v
	}
	if verifyInternalAuthHeaders(key, otherPath, time.Now()) {
		t.Error("a MAC signed for one path authorized another")
	}
}

func TestVerifyInternalAuthHeaders_rejectsAStaleOrFutureStamp(t *testing.T) {
	key := testHopKey(t)
	now := time.Now()
	r := signedRequest(t, key, http.MethodGet, "/v1/db/query")

	if verifyInternalAuthHeaders(key, r, now.Add(internalAuthMaxSkew+time.Second)) {
		t.Error("a stamp older than the window was accepted")
	}
	if verifyInternalAuthHeaders(key, r, now.Add(-internalAuthMaxSkew-time.Second)) {
		t.Error("a stamp from the future was accepted")
	}
	if !verifyInternalAuthHeaders(key, r, now.Add(internalAuthMaxSkew/2)) {
		t.Error("a stamp inside the window was rejected; nodes have clock drift")
	}
}

func TestVerifyInternalAuthHeaders_rejectsMalformedMACs(t *testing.T) {
	key := testHopKey(t)

	for _, mac := range []string{
		"",
		"not-a-mac",
		"nostop.deadbeef",
		strconv.FormatInt(time.Now().Unix(), 10) + ".",
		strconv.FormatInt(time.Now().Unix(), 10) + ".zzzz",
	} {
		r := forgedRequest(http.MethodGet, "/v1/db/query")
		r.Header.Set(HeaderInternalAuthMAC, mac)
		if verifyInternalAuthHeaders(key, r, time.Now()) {
			t.Errorf("a malformed MAC %q was accepted", mac)
		}
	}
}

// A gateway with no cluster secret has no key, and no key trusts nothing. It
// must not become a gateway that trusts everything.
func TestInternalAuth_withoutAKeyNothingIsTrusted(t *testing.T) {
	r := signedRequest(t, testHopKey(t), http.MethodGet, "/v1/db/query")

	if verifyInternalAuthHeaders(nil, r, time.Now()) {
		t.Error("a gateway with no key accepted a MAC")
	}
	if err := signInternalAuthHeaders(nil, r.Header, http.MethodGet, "/v1/db/query", time.Now()); err == nil {
		t.Error("a gateway with no key signed a hop; it would be dropped at the far end")
	}
}

func testGatewayWithHopKey(t *testing.T) *Gateway {
	t.Helper()
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	return &Gateway{logger: logger, internalAuthKey: testHopKey(t)}
}

// The middleware is what makes the rest of the chain safe: nothing below it
// has to ask whether the headers it sees are authentic, because forged ones
// are already gone.
func TestInternalAuthMiddleware_deletesForgedHeaders(t *testing.T) {
	g := testGatewayWithHopKey(t)

	var seen http.Header
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() })
	g.internalAuthMiddleware(next).ServeHTTP(httptest.NewRecorder(), forgedRequest(http.MethodGet, "/v1/db/query"))

	for _, h := range []string{
		HeaderInternalAuthValidated,
		HeaderInternalAuthNamespace,
		HeaderInternalAuthJWTSub,
		HeaderInternalAuthScopes,
	} {
		if v := seen.Get(h); v != "" {
			t.Errorf("%s survived as %q", h, v)
		}
	}
}

func TestInternalAuthMiddleware_keepsAGenuineHop(t *testing.T) {
	g := testGatewayWithHopKey(t)

	var seen http.Header
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() })
	g.internalAuthMiddleware(next).ServeHTTP(
		httptest.NewRecorder(), signedRequest(t, g.internalAuthKey, http.MethodGet, "/v1/db/query"))

	if seen.Get(HeaderInternalAuthValidated) != "true" {
		t.Error("a genuine hop lost its validated header")
	}
	if seen.Get(HeaderInternalAuthNamespace) == "" {
		t.Error("a genuine hop lost its namespace")
	}
}

// The MAC authenticates one hop. Passing it on would let whatever is on the
// other side — a deployed app, an outbound proxy target — replay it.
func TestInternalAuthMiddleware_consumesTheMAC(t *testing.T) {
	g := testGatewayWithHopKey(t)

	var seen http.Header
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() })
	g.internalAuthMiddleware(next).ServeHTTP(
		httptest.NewRecorder(), signedRequest(t, g.internalAuthKey, http.MethodGet, "/v1/db/query"))

	if seen.Get(HeaderInternalAuthMAC) != "" {
		t.Error("the MAC was forwarded past the hop it authenticates")
	}
}

// The chain, not the pieces. authMiddleware and authorizationMiddleware both
// used to believe these headers on the strength of the source IP, and neither
// was reached by any test.
func TestMiddlewareChain_forgedInternalAuthDoesNotAuthenticate(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:          logger,
		internalAuthKey: testHopKey(t),
		cfg:             &Config{ClientNamespace: "index"},
	}

	var reached bool
	var gotNamespace string
	var gotScopes auth.ScopeSet
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		reached = true
		if v, ok := r.Context().Value(CtxKeyNamespaceOverride).(string); ok {
			gotNamespace = v
		}
		if v, ok := r.Context().Value(ctxKeyScopes).(auth.ScopeSet); ok {
			gotScopes = v
		}
	})

	chain := g.internalAuthMiddleware(g.authMiddleware(next))
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, forgedRequest(http.MethodGet, "/v1/db/query"))

	if reached && gotNamespace == "someone-elses-namespace" {
		t.Error("forged headers named the namespace the handler ran against")
	}
	if gotScopes.IsAdmin() {
		t.Error("forged headers granted admin")
	}
}

func TestMiddlewareChain_aGenuineHopStillAuthenticates(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:          logger,
		internalAuthKey: testHopKey(t),
		cfg:             &Config{ClientNamespace: "index"},
	}

	var gotNamespace string
	var gotScopes auth.ScopeSet
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if v, ok := r.Context().Value(CtxKeyNamespaceOverride).(string); ok {
			gotNamespace = v
		}
		if v, ok := r.Context().Value(ctxKeyScopes).(auth.ScopeSet); ok {
			gotScopes = v
		}
	})

	chain := g.internalAuthMiddleware(g.authMiddleware(next))
	chain.ServeHTTP(httptest.NewRecorder(), signedRequest(t, g.internalAuthKey, http.MethodGet, "/v1/db/query"))

	if gotNamespace != "someone-elses-namespace" {
		t.Errorf("a genuine hop resolved namespace %q; the main gateway's decision was lost", gotNamespace)
	}
	if !gotScopes.IsAdmin() {
		t.Error("a genuine hop lost the grant set the main gateway forwarded")
	}
}

// The ownership gate skips its checks for a pre-authenticated request, which is
// the shortest path to any namespace's data. It used to skip them on the
// strength of the source IP.
func TestAuthorizationMiddleware_forgedInternalAuthDoesNotSkipOwnership(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:          logger,
		internalAuthKey: testHopKey(t),
		cfg:             &Config{ClientNamespace: "index"},
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	chain := g.internalAuthMiddleware(g.authorizationMiddleware(next))

	// A path the gate actually guards. /v1/rqlite is raw access to the
	// namespace's own database.
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, forgedRequest(http.MethodPost, "/v1/rqlite/query"))

	if rec.Code == http.StatusOK {
		t.Fatalf("forged headers skipped the ownership gate on %s", "/v1/rqlite/query")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("the request was refused with %d, want 403: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// The same gate must still let a genuine hop past, or every proxied request
// pays for an ownership lookup the main gateway already did.
func TestAuthorizationMiddleware_aGenuineHopSkipsOwnership(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:          logger,
		internalAuthKey: testHopKey(t),
		cfg:             &Config{ClientNamespace: "index"},
	}

	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	chain := g.internalAuthMiddleware(g.authorizationMiddleware(next))
	chain.ServeHTTP(httptest.NewRecorder(),
		signedRequest(t, g.internalAuthKey, http.MethodPost, "/v1/rqlite/query"))

	if !reached {
		t.Error("a genuine hop was made to re-prove ownership the main gateway already checked")
	}
}

// withMiddleware is the chain the server actually runs. The gate being first in
// it is the whole point, and a test that assembles its own chain would not
// notice if it were dropped from the real one.
func TestWithMiddleware_stripsForgedInternalAuthHeaders(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{
		logger:          logger,
		internalAuthKey: testHopKey(t),
		cfg:             &Config{ClientNamespace: "index"},
		ready:           newReadiness(),
		startedAt:       time.Now(),
	}
	g.ready.set(ReadinessReady, "ready")

	var seen http.Header
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	})

	// A public path, so the chain runs end to end without a credential.
	r := forgedRequest(http.MethodGet, "/v1/health")
	g.withMiddleware(next).ServeHTTP(httptest.NewRecorder(), r)

	if seen == nil {
		t.Fatal("the request never reached the handler; the chain refused a public path")
	}
	for _, h := range []string{
		HeaderInternalAuthValidated,
		HeaderInternalAuthNamespace,
		HeaderInternalAuthJWTSub,
		HeaderInternalAuthScopes,
		HeaderInternalAuthMAC,
	} {
		if v := seen.Get(h); v != "" {
			t.Errorf("%s reached the handler as %q — the gate is not in the chain", h, v)
		}
	}
}

// Setting the internal-auth headers without stamping a MAC over them produces
// a request the far end will strip, which looks to a caller like auth silently
// stopping working. There are two proxy hops; a third must not be written
// without a MAC.
func TestEveryProxyHopSignsWhatItAsserts(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse the gateway package: %v", err)
	}

	hops := 0
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				block, ok := n.(*ast.BlockStmt)
				if !ok {
					return true
				}
				if !assertsInternalAuth(block) {
					return true
				}
				hops++
				if !identUsed(block, "signInternalAuthHeaders") {
					t.Errorf("%s: internal auth is asserted without stamping a MAC over it, "+
						"so the far end will strip every header set here",
						fset.Position(block.Pos()))
				}
				return true
			})
		}
	}

	if hops != 2 {
		t.Errorf("found %d places that assert internal auth, expected 2 (the WebSocket hop "+
			"and the HTTP hop) — if a hop was added or removed, say so here", hops)
	}
}

// assertsInternalAuth reports whether a block directly contains
// `<header>.Set(HeaderInternalAuthValidated, ...)`, which is what makes a
// request claim it was pre-authenticated.
func assertsInternalAuth(block *ast.BlockStmt) bool {
	for _, stmt := range block.List {
		expr, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := expr.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Set" || len(call.Args) == 0 {
			continue
		}
		if ident, ok := call.Args[0].(*ast.Ident); ok && ident.Name == "HeaderInternalAuthValidated" {
			return true
		}
	}
	return false
}

// identUsed reports whether an identifier appears anywhere under a node.
func identUsed(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && ident.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}
