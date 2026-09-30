//go:build e2e_fleet

package authkeysroles

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// routeCase is one representative route and the roles its policy admits,
// read from the route policy table (core/pkg/gateway/route_policy.go) and
// the role definitions (docs/AUTH.md#roles).
type routeCase struct {
	name   string
	req    gw.Req
	allows map[string]bool
}

var (
	everyone     = map[string]bool{roleOwner: true, roleAdmin: true, roleDev: true, roleRuntime: true, roleReader: true}
	operating    = map[string]bool{roleOwner: true, roleAdmin: true}
	building     = map[string]bool{roleOwner: true, roleAdmin: true, roleDev: true}
	dataPlane    = map[string]bool{roleOwner: true, roleAdmin: true, roleDev: true, roleRuntime: true}
	nobodyHere   = map[string]bool{}
	matrixRoles  = []string{roleOwner, roleAdmin, roleDev, roleRuntime, roleReader}
	jsonHeader   = http.Header{"Content-Type": {"application/json"}}
	matrixRoutes = []routeCase{
		{"whoami (any credential)", gw.Req{Path: gw.PathWhoami}, everyone},
		{"audit (audit:read)", gw.Req{Path: pathAudit}, operating},
		{"members (members:write)", gw.Req{Path: pathMembers}, operating},
		{"keys (members:write)", gw.Req{Path: pathKeys}, operating},
		{"app grants (members:write)", gw.Req{Path: pathGrants}, operating},
		{"namespace list (the wallet's own, any member)", gw.Req{Path: "/v1/namespace/list"}, everyone},
		{"session policy (namespace:write)", gw.Req{Path: "/v1/namespace/session-policy"}, operating},
		{"functions (fn:manage)", gw.Req{Path: "/v1/functions"}, building},
		{"deployments (deploy:read)", gw.Req{Path: "/v1/deployments/list"}, building},
		{"sqlite databases (db:read)", gw.Req{Path: "/v1/db/sqlite/list"}, building},
		{"rqlite (db:write)", gw.Req{Method: http.MethodPost, Path: "/v1/rqlite/query", Header: jsonHeader, Body: []byte(`{"sql":"SELECT 1"}`)}, building},
		{"cache put (cache:write)", gw.Req{Method: http.MethodPost, Path: "/v1/cache/put", Header: jsonHeader, Body: []byte(`{"dmap":"e2e-roles","key":"k","value":"v"}`)}, dataPlane},
		{"cache get (cache:read)", gw.Req{Method: http.MethodPost, Path: "/v1/cache/get", Header: jsonHeader, Body: []byte(`{"dmap":"e2e-roles","key":"k"}`)}, dataPlane},
		{"publish (pubsub:write)", gw.Req{Method: http.MethodPost, Path: "/v1/pubsub/publish", Header: jsonHeader, Body: []byte(`{"topic":"e2e-roles","data_base64":"aGk="}`)}, dataPlane},
		{"topics (pubsub:read)", gw.Req{Path: "/v1/pubsub/topics"}, dataPlane},
		{"operator settings (operator list)", gw.Req{Path: pathOperatorSettings}, nobodyHere},
		{"operator list (operator list)", gw.Req{Path: "/v1/operator/operators"}, nobodyHere},
	}
)

// codeNotAnOperator refuses a wallet missing from the cluster's operator
// list; pathOperatorSettings is one operator route.
const (
	codeNotAnOperator    = "NOT_AN_OPERATOR"
	pathOperatorSettings = "/v1/operator/settings"
)

// deniedCodes are what a refused credential is told: a missing permission,
// no grant here, or not a cluster operator.
var deniedCodes = map[string]bool{"INSUFFICIENT_SCOPE": true, "OWNERSHIP_REQUIRED": true, codeNotAnOperator: true}

// TestRoles_matrix: each role reaches exactly what docs/AUTH.md#roles says —
// owner and admin everything in the namespace, developer the data plane plus
// db, deploy, secrets and fn:manage, runtime the data plane, reader nothing —
// and a refusal says which permission was missing. Nobody's namespace role
// reaches the operator routes.
func TestRoles_matrix(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	tokens := map[string]string{roleOwner: n.Owner.Token()}
	for _, role := range []string{roleAdmin, roleDev, roleRuntime, roleReader} {
		_, tokens[role] = memberToken(t, n, role, "")
	}
	for _, role := range matrixRoles {
		var who struct {
			Role *string `json:"role"`
		}
		if err := c.MustSend(t, gw.Req{Path: gw.PathWhoami, Bearer: tokens[role]}).Expect(t, http.StatusOK).Decode(&who); err != nil {
			t.Fatal(err)
		}
		if who.Role == nil || *who.Role != role {
			t.Errorf("whoami for a %s reports role %v", role, who.Role)
		}
	}
	for _, rc := range matrixRoutes {
		for _, role := range matrixRoles {
			req := rc.req
			req.Bearer = tokens[role]
			resp := c.MustSend(t, req)
			refused := resp.Status == http.StatusUnauthorized || resp.Status == http.StatusForbidden
			switch {
			case rc.allows[role] && !reached(resp):
				t.Errorf("%s: %s was not served (%d %s): %.200s", rc.name, role, resp.Status, resp.ErrorCode(), resp.Body)
			case !rc.allows[role] && !refused:
				t.Errorf("%s: %s reached it (%d)", rc.name, role, resp.Status)
			case !rc.allows[role] && !deniedCodes[resp.ErrorCode()]:
				t.Errorf("%s: %s refused with code %q: %.300s", rc.name, role, resp.ErrorCode(), resp.Body)
			case !rc.allows[role]:
				checkRefusalShape(t, rc.name+" as "+role, resp)
			}
		}
	}
}

// checkRefusalShape: {error, code, hint}, and a missing permission names it.
// NOT_AN_OPERATOR is held to {error, code} here: the product sends it without
// a hint (core/pkg/gateway/handlers/operator/authorize.go requireOperator),
// and TestOperatorRefusals_carryAHint alone holds it to the documented hint,
// so that one bug fails one test instead of every matrix cell.
func checkRefusalShape(t testing.TB, what string, resp *gw.Response) {
	t.Helper()
	var body map[string]any
	if err := resp.Decode(&body); err != nil {
		t.Errorf("%s: %v", what, err)
		return
	}
	fields := []string{"error", "code", "hint"}
	if body["code"] == codeNotAnOperator {
		fields = fields[:2]
	}
	for _, k := range fields {
		if s, _ := body[k].(string); s == "" {
			t.Errorf("%s: refusal lacks %q", what, k)
		}
	}
	if body["code"] == "INSUFFICIENT_SCOPE" && (body["required_scope"] == nil || body["required_permission"] == nil) {
		t.Errorf("%s: INSUFFICIENT_SCOPE does not name the missing grant: %s", what, resp.Body)
	}
}

// TestOperatorRefusals_carryAHint: NOT_AN_OPERATOR, like every 401 and 403,
// carries {error, code, hint} (docs/AUTH.md#when-a-request-is-refused). A
// namespace owner holds the admin grant, so the operator list is what refuses
// it. Expected red until the product adds the hint.
func TestOperatorRefusals_carryAHint(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	resp := harness.GW(t).MustSend(t, gw.Req{Path: pathOperatorSettings, Bearer: n.Owner.Token()})
	var body map[string]any
	if err := resp.Decode(&body); err != nil || resp.Status != http.StatusForbidden || body["code"] != codeNotAnOperator {
		t.Fatalf("a namespace owner on %s: want 403 %s, got %d: %.300s", pathOperatorSettings, codeNotAnOperator, resp.Status, resp.Body)
	}
	if s, _ := body["hint"].(string); strings.TrimSpace(s) == "" {
		t.Errorf("PRODUCT BUG: 403 %s carries no hint (core/pkg/gateway/handlers/operator/authorize.go requireOperator "+
			"writes only error and code), although docs/AUTH.md#when-a-request-is-refused promises {error, code, hint}: %s",
			codeNotAnOperator, resp.Body)
	}
}

// TestRoles_keyScopes: a key reaches exactly its grants. app-runtime holds
// neither pubsub nor cache; a cache key reaches the cache and nothing else;
// an admin key reaches the control plane.
func TestRoles_keyScopes(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	keys := map[string]string{
		"app-runtime": mintKey(t, n, map[string]any{"scope": "app-runtime"}).APIKey,
		"cache":       mintKey(t, n, map[string]any{"scope": "cache"}).APIKey,
		"admin":       mintKey(t, n, map[string]any{"scope": "admin"}).APIKey,
	}
	allowed := map[string]map[string]bool{
		"cache put (cache:write)": {"cache": true, "admin": true},
		"publish (pubsub:write)":  {"admin": true},
		"audit (audit:read)":      {"admin": true},
		"members (members:write)": {"admin": true},
		"whoami (any credential)": {"app-runtime": true, "cache": true, "admin": true},
	}
	for _, rc := range matrixRoutes {
		want, ok := allowed[rc.name]
		if !ok {
			continue
		}
		for scope, key := range keys {
			req := rc.req
			req.Bearer = key
			resp := c.MustSend(t, req)
			refused := resp.Status == http.StatusUnauthorized || resp.Status == http.StatusForbidden
			if want[scope] != reached(resp) || (!want[scope] && !refused) {
				t.Errorf("%s with a %s key: HTTP %d %s (want served=%v)", rc.name, scope, resp.Status, resp.ErrorCode(), want[scope])
			}
		}
	}
}

// TestRoutePolicy_oddPaths: an unrouted path asks for a credential and then
// is not found; a doubled slash is judged as the route it cleans to; a dot
// segment does not smuggle a request past its policy.
func TestRoutePolicy_oddPaths(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	_, runtimeTok := memberToken(t, n, roleRuntime, "")
	refusal(t, c.MustSend(t, gw.Req{Path: "/v1/no-such-route"}), http.StatusUnauthorized, "AUTH_MISSING")
	if r := c.MustSend(t, gw.Req{Path: "/v1/no-such-route", Bearer: runtimeTok}); r.Status != http.StatusNotFound {
		t.Errorf("an unrouted path with a credential: want 404, got %d", r.Status)
	}
	if r := c.MustSend(t, gw.Req{Path: "/v1//storage/get/QmE2E"}); r.Status != http.StatusUnauthorized {
		t.Errorf("/v1//storage/get anonymously: want 401 like /v1/storage/get, got %d", r.Status)
	}
	for _, p := range []string{"/v1/storage/get/../../v1/audit", "/v1/cache/get/../../audit", "/v1/pubsub/../audit"} {
		r := c.MustSend(t, gw.Req{Path: p, Bearer: runtimeTok})
		if r.Status == http.StatusOK && containsFold(string(r.Body), `"events"`) {
			t.Errorf("%s let a runtime member read the audit trail", p)
		}
	}
}
