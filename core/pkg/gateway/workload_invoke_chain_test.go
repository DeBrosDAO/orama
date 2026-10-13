package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	serverlesshandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// fnRegistry answers Get from a fixed set of functions.
type fnRegistry struct {
	serverless.FunctionRegistry
	fns map[string]*serverless.Function
}

func (r fnRegistry) Get(_ context.Context, namespace, name string, _ int) (*serverless.Function, error) {
	if fn, ok := r.fns[namespace+"/"+name]; ok {
		return fn, nil
	}
	return nil, serverless.ErrFunctionNotFound
}

// invokeThroughTheChain runs a forwarded invoke through the namespace gateway's
// middleware and the real invoke handler. reachedEngine is true when the
// invoker authorized the caller: the handler's engine is absent here, so
// running the function panics, and that panic is the proof it was allowed.
func invokeThroughTheChain(t *testing.T, g *Gateway, fn string, public bool) (status int, reachedEngine bool) {
	t.Helper()
	r := hop(t, g, http.MethodPost, "/v1/functions/"+fn+"/invoke", hopNamespace, workloadSub)
	return invokeRequestThroughTheChain(t, g, fn, public, r)
}

// invokeRequestThroughTheChain is invokeThroughTheChain for a forwarded
// request the caller built.
func invokeRequestThroughTheChain(t *testing.T, g *Gateway, fn string, public bool, r *http.Request) (status int, reachedEngine bool) {
	t.Helper()
	reg := fnRegistry{fns: map[string]*serverless.Function{
		hopNamespace + "/" + fn: {Name: fn, Namespace: hopNamespace, IsPublic: public},
	}}
	h := serverlesshandlers.NewServerlessHandlers(
		serverless.NewInvoker(nil, reg, nil, hopNamespace, zap.NewNop()),
		nil, reg, nil, nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				reachedEngine = true
			}
		}()
		h.InvokeFunction(w, r, hopNamespace+"/"+fn, 0)
	})
	g.internalAuthMiddleware(g.routePolicyMiddleware(g.authMiddleware(
		g.authorizationMiddleware(g.scopeMiddleware(next))))).ServeHTTP(rec, r)
	return rec.Code, reachedEngine
}

var workloadSub = "app:" + hopNamespace + "/web"

// A reader app holds no invoke grant: a private function refuses it (403), and
// a public function is open to it as it is to anybody. The grant the invoke
// route carries for it must not turn into a resource restriction that refuses
// the public one.
func TestForwardedWorkload_aReaderAppInvokesPublicNotPrivate(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "reader")
	registry.principalType = "app"

	if status, reached := invokeThroughTheChain(t, g, "open", true); !reached {
		t.Errorf("a reader app was refused a public function: status %d", status)
	}
	if status, reached := invokeThroughTheChain(t, g, "secret", false); reached || status != http.StatusForbidden {
		t.Errorf("a reader app on a private function: status %d, reached %v, want 403", status, reached)
	}
}

func TestForwardedWorkload_aRuntimeAppInvokesPrivate(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.principalType = "app"

	if status, reached := invokeThroughTheChain(t, g, "secret", false); !reached {
		t.Errorf("a runtime app was refused a private function: status %d", status)
	}
}

// hopWithToken is hop for a workload whose token carries jti.
func hopWithToken(t *testing.T, g *Gateway, path, jti string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, nil)
	r.Header.Set(HeaderInternalAuthValidated, "true")
	r.Header.Set(HeaderInternalAuthNamespace, hopNamespace)
	r.Header.Set(HeaderInternalAuthJWTSub, workloadSub)
	r.Header.Set(HeaderInternalAuthJWTJti, jti)
	if err := signInternalAuthHeaders(g.internalAuthKey, r.Header, http.MethodPost, path, time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return r
}

// An app's grant is cached for ten seconds, and a redeploy mints it a new
// token: the grant the redeploy applied has to be the one the invoke sees, not
// the previous token's cached runtime grant. The stagenet e2e stored a todo as
// a reader app on the one node whose gateway had cached the app's grant seconds
// earlier.
func TestForwardedWorkload_aNewTokenReadsTheLiveGrantNotTheCachedOne(t *testing.T) {
	g, registry := namespaceGatewayForHops(t, "runtime")
	registry.principalType = "app"
	const path = "/v1/functions/secret/invoke"

	if status, reached := invokeRequestThroughTheChain(t, g, "secret", false, hopWithToken(t, g, path, "token-1")); !reached {
		t.Fatalf("a runtime app was refused a private function: status %d", status)
	}

	registry.role = "reader"
	if status, reached := invokeRequestThroughTheChain(t, g, "secret", false, hopWithToken(t, g, path, "token-2")); reached || status != http.StatusForbidden {
		t.Errorf("a reader app on its redeployed token: status %d, reached %v, want 403", status, reached)
	}
}
