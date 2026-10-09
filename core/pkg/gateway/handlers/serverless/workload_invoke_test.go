package serverless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

func workloadInvokeRequest(custom map[string]string, grant *auth.Grant) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/functions/store/invoke", nil)
	ctx := context.WithValue(r.Context(), ctxkeys.JWT, &auth.JWTClaims{
		Sub:       auth.WorkloadSubject("acme", "api"),
		Namespace: "acme",
		Custom:    custom,
	})
	if grant != nil {
		ctx = context.WithValue(ctx, ctxkeys.Grant, grant)
	}
	return r.WithContext(ctx)
}

func appGrant(role auth.Role, resource string) *auth.Grant {
	return &auth.Grant{PrincipalType: auth.PrincipalApp, Identifier: auth.WorkloadSubject("acme", "api"), Role: role, Resource: resource}
}

// An app is started before its owner can grant it anything, so its token holds
// the grant of that moment. The invoke decision reads the grant it holds now,
// in either direction.
func TestCallerHasInvoke_aWorkloadHoldsWhatItsGrantSaysNow(t *testing.T) {
	h := &ServerlessHandlers{}
	for name, tc := range map[string]struct {
		custom map[string]string
		grant  *auth.Grant
		want   bool
	}{
		"granted runtime after its token was minted with nothing": {nil, appGrant(auth.RoleRuntime, ""), true},
		"granted runtime, token minted empty":                     {map[string]string{"scopes": ""}, appGrant(auth.RoleRuntime, ""), true},
		"narrowed to a function it may still invoke":              {nil, appGrant(auth.RoleRuntime, "fn:name=checkout"), true},
		"reduced to reader after its token carried invoke":        {map[string]string{"scopes": "invoke"}, appGrant(auth.RoleReader, ""), false},
		"no grant at all, token carrying invoke":                  {map[string]string{"scopes": "invoke"}, nil, false},
		"narrowed to a resource that is not a function":           {map[string]string{"scopes": "invoke"}, appGrant(auth.RoleRuntime, "cache:key=sessions/*"), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := h.getCallerHasInvokeFromRequest(workloadInvokeRequest(tc.custom, tc.grant)); got != tc.want {
				t.Errorf("getCallerHasInvokeFromRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

// A key's exchanged token is still its own answer: only a workload's grant is
// read live here.
func TestCallerHasInvoke_anExchangedKeyStillReadsItsScopes(t *testing.T) {
	h := &ServerlessHandlers{}
	r := httptest.NewRequest(http.MethodPost, "/v1/functions/store/invoke", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.JWT, &auth.JWTClaims{
		Sub: "ak_exchanged_key", Namespace: "acme", Custom: map[string]string{"scopes": "invoke"},
	}))
	if !h.getCallerHasInvokeFromRequest(r) {
		t.Error("an exchanged key holding invoke was refused")
	}
}

func refusedInvoke(t *testing.T, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	reg := newMockRegistry()
	reg.functions["acme/store"] = &serverless.Function{Name: "store", Namespace: "acme"}
	h := newTestHandlers(reg)
	h.invoker = serverless.NewInvoker(nil, reg, nil, "acme", zap.NewNop())
	rec := httptest.NewRecorder()
	h.InvokeFunction(rec, r, "acme/store", 0)
	return rec
}

// 401 is for a caller with no identity. A deployed app holding no invoke grant
// is known and refused: asking it to sign in again changes nothing, so it is 403.
func TestInvokeFunction_aKnownCallerWithoutTheGrantIsForbiddenNotUnauthorized(t *testing.T) {
	rec := refusedInvoke(t, asCredentialOf(workloadInvokeRequest(nil, appGrant(auth.RoleReader, "")), "acme"))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), string(httputil.ErrCodeForbidden)) {
		t.Errorf("a reader app: %d %s, want 403 FORBIDDEN", rec.Code, rec.Body.String())
	}
}

func TestInvokeFunction_anAnonymousCallerIsStillUnauthorized(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/functions/store/invoke", nil)
	rec := refusedInvoke(t, r)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), string(httputil.ErrCodeUnauthorized)) {
		t.Errorf("an anonymous caller: %d %s, want 401 UNAUTHORIZED", rec.Code, rec.Body.String())
	}
}

// The index gateway's circuit breaker counts a 502, 503 or 504 against the
// namespace gateway that sent it, unless the response is a function's own. The
// marker is what tells them apart, and it is on a function's every outcome,
// the failure to load it included (503 FUNCTION_UNAVAILABLE).
func TestInvokeFunction_aFunctionsOutcomeIsMarkedAsTheFunctions(t *testing.T) {
	rec := refusedInvoke(t, httptest.NewRequest(http.MethodPost, "/v1/functions/store/invoke", nil))
	if rec.Header().Get(httputil.HeaderFunctionOrigin) == "" {
		t.Errorf("a refused invocation (%d) carries no %s", rec.Code, httputil.HeaderFunctionOrigin)
	}
}

// A refusal made before any function is involved is the gateway's own, and must
// count against it when it is a 503.
func TestInvokeFunction_aRefusalBeforeTheFunctionIsNotMarked(t *testing.T) {
	h := &ServerlessHandlers{}
	rec := httptest.NewRecorder()
	h.InvokeFunction(rec, httptest.NewRequest(http.MethodGet, "/v1/functions/store/invoke", nil), "acme/store", 0)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get(httputil.HeaderFunctionOrigin) != "" {
		t.Errorf("a wrong-method request: %d with %s=%q, want 405 unmarked",
			rec.Code, httputil.HeaderFunctionOrigin, rec.Header().Get(httputil.HeaderFunctionOrigin))
	}
}
