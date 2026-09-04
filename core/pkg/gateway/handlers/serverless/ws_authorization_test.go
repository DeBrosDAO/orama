package serverless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// The persistent-WebSocket path builds its own invocation context and never
// reaches Invoke, so the authorization the stateless path gets per-frame has to
// happen at the upgrade. It used to check only that an internal function had an
// admin caller, which left the rest of the same decision out: a merely private
// function was reachable over a persistent socket by a caller with no wallet
// and no invoke grant, while the identical function over HTTP refused them.

func persistentFn(name string, public, internal bool) *serverless.Function {
	return &serverless.Function{
		ID: "fn-" + name, Name: name, Namespace: "anchat-test",
		WSPersistent: true, IsPublic: public, IsInternal: internal,
	}
}

func handlersWith(fn *serverless.Function) *ServerlessHandlers {
	reg := newMockRegistry()
	reg.functions[fn.Namespace+"/"+fn.Name] = fn
	return newTestHandlers(reg)
}

func upgradeRequest(ctxValues map[any]any) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/?namespace=anchat-test", nil)
	ctx := req.Context()
	for k, v := range ctxValues {
		ctx = context.WithValue(ctx, k, v)
	}
	return req.WithContext(ctx)
}

func TestHandleWebSocket_persistentPrivateFunctionRefusesAnAnonymousCaller(t *testing.T) {
	h := handlersWith(persistentFn("rpc-router", false, false))

	rec := httptest.NewRecorder()
	h.HandleWebSocket(rec, upgradeRequest(nil), "rpc-router", 0)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — a private function was reachable over a persistent socket with no identity", rec.Code)
	}
}

// An identified caller who does not hold the invoke grant is the case
// bugboard #259 closed on every other path.
func TestHandleWebSocket_persistentPrivateFunctionRefusesACallerWithoutTheGrant(t *testing.T) {
	h := handlersWith(persistentFn("rpc-router", false, false))

	req := upgradeRequest(map[any]any{
		ctxkeys.JWT: &auth.JWTClaims{Sub: "ak_key:anchat-test", Custom: map[string]string{"scopes": "storage"}},
	})
	rec := httptest.NewRecorder()
	h.HandleWebSocket(rec, req, "rpc-router", 0)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — a storage-only key opened a persistent socket to a private function", rec.Code)
	}
}

func TestHandleWebSocket_persistentInternalFunctionRefusesANonAdmin(t *testing.T) {
	h := handlersWith(persistentFn("migrate", false, true))

	req := upgradeRequest(map[any]any{
		ctxkeys.Scopes: auth.ScopeSet{auth.ScopeInvoke: {}},
	})
	rec := httptest.NewRecorder()
	h.HandleWebSocket(rec, req, "migrate", 0)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// The upgrade must still be refused before anything is upgraded, not after —
// a rejected caller should never reach a WebSocket at all.
func TestHandleWebSocket_persistentRefusalHappensBeforeTheUpgrade(t *testing.T) {
	h := handlersWith(persistentFn("rpc-router", false, false))

	rec := httptest.NewRecorder()
	h.HandleWebSocket(rec, upgradeRequest(nil), "rpc-router", 0)

	if got := rec.Header().Get("Upgrade"); got != "" {
		t.Errorf("the connection was upgraded before the caller was refused: Upgrade: %s", got)
	}
}

// A caller who is allowed gets past the gate. The upgrade itself then fails,
// because httptest's recorder cannot hijack the connection — which is exactly
// how we know the authorization was not what stopped it.
func TestHandleWebSocket_persistentAllowsAnAuthorizedCaller(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   *serverless.Function
		ctx  map[any]any
	}{
		{"public function, anonymous caller", persistentFn("ping", true, false), nil},
		{"private function, caller with the invoke grant", persistentFn("rpc-router", false, false),
			map[any]any{ctxkeys.Scopes: auth.ScopeSet{auth.ScopeInvoke: {}}, ctxkeys.JWT: &auth.JWTClaims{Sub: "0xUser"}}},
		{"internal function, admin caller", persistentFn("migrate", false, true),
			map[any]any{ctxkeys.Scopes: auth.ScopeSet{auth.ScopeAdmin: {}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := handlersWith(tc.fn)
			rec := httptest.NewRecorder()
			h.HandleWebSocket(rec, upgradeRequest(tc.ctx), tc.fn.Name, 0)

			if rec.Code == http.StatusForbidden {
				t.Fatalf("an authorized caller was refused: %s", rec.Body.String())
			}
		})
	}
}
