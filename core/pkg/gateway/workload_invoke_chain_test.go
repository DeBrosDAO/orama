package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
	reg := fnRegistry{fns: map[string]*serverless.Function{
		hopNamespace + "/" + fn: {Name: fn, Namespace: hopNamespace, IsPublic: public},
	}}
	h := serverlesshandlers.NewServerlessHandlers(
		serverless.NewInvoker(nil, reg, nil, hopNamespace, zap.NewNop()),
		nil, reg, nil, nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
	r := hop(t, g, http.MethodPost, "/v1/functions/"+fn+"/invoke", hopNamespace, workloadSub)
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
