package serverless

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// invokeMockRegistry is a minimal FunctionRegistry that returns a single
// canned function. Anything else panics so accidental drift is loud.
type invokeMockRegistry struct {
	FunctionRegistry // embedded — calling unimplemented methods panics

	fn *Function
}

func (m *invokeMockRegistry) Get(_ context.Context, _, _ string, _ int) (*Function, error) {
	return m.fn, nil
}

// The invocations below use a cancelled ctx so the call short-circuits inside
// executeWithRetry's ctx.Err() check before touching the engine, which is nil
// here. That separates "blocked at authorization" (ErrUnauthorized) from
// "passed authorization, stopped later" (context.Canceled) without standing up
// a WASM engine.
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// A cron row firing has no caller. Gating it on CallerWallet returned false
// every time and silently blocked every fire for 19 hours (bugboard #264).
// What says the gateway started this is SystemOriginated.
func TestInvoke_aGatewayStartedInvocationSkipsTheCallerCheck(t *testing.T) {
	privateFn := &Function{ID: "fn-id", Namespace: "anchat-test", Name: "push-fanout", IsPublic: false}
	inv := &Invoker{registry: &invokeMockRegistry{fn: privateFn}, logger: zap.NewNop()}

	_, err := inv.Invoke(cancelledCtx(), &InvokeRequest{
		Namespace:        "anchat-test",
		FunctionName:     "push-fanout",
		TriggerType:      TriggerTypeCron,
		CallerWallet:     "", // what a cron fire naturally has
		SystemOriginated: true,
	})
	if errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a cron fire was refused for having no caller: %v", err)
	}
}

// The hardening. The trigger type used to be what said "skip authorization",
// so any request carrying one of those type values skipped it — a value that
// travels with the work and gets copied onto derived requests. A request that
// merely says it is a cron does not get the gateway's authority.
func TestInvoke_aSystemTriggerTypeAloneIsNotAuthority(t *testing.T) {
	privateFn := &Function{ID: "fn-id", Namespace: "anchat-test", Name: "push-fanout", IsPublic: false}
	inv := &Invoker{registry: &invokeMockRegistry{fn: privateFn}, logger: zap.NewNop()}

	for _, trigger := range []TriggerType{
		TriggerTypeCron, TriggerTypePubSub, TriggerTypeDatabase,
		TriggerTypeTimer, TriggerTypeJob, TriggerTypeInternal,
	} {
		t.Run(string(trigger), func(t *testing.T) {
			resp, err := inv.Invoke(cancelledCtx(), &InvokeRequest{
				Namespace:    "anchat-test",
				FunctionName: "push-fanout",
				TriggerType:  trigger,
				CallerWallet: "",
				// SystemOriginated deliberately absent.
			})
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("trigger type %q alone got past the caller check: err=%v resp=%+v", trigger, err, resp)
			}
		})
	}
}

func TestInvoke_anExternalCallerIsStillChecked(t *testing.T) {
	privateFn := &Function{ID: "fn-id", Namespace: "anchat-test", Name: "push-fanout", IsPublic: false}
	inv := &Invoker{registry: &invokeMockRegistry{fn: privateFn}, logger: zap.NewNop()}

	for _, trigger := range []TriggerType{TriggerTypeHTTP, TriggerTypeWebSocket} {
		t.Run(string(trigger), func(t *testing.T) {
			resp, err := inv.Invoke(cancelledCtx(), &InvokeRequest{
				Namespace:    "anchat-test",
				FunctionName: "push-fanout",
				TriggerType:  trigger,
				CallerWallet: "",
			})
			if !errors.Is(err, ErrUnauthorized) {
				t.Errorf("an anonymous %s caller reached a private function: %v", trigger, err)
			}
			if resp == nil || resp.Error != "unauthorized" {
				t.Errorf("response = %+v, want an unauthorized response", resp)
			}
		})
	}
}

func TestInvoke_aPublicFunctionIsOpenToEveryone(t *testing.T) {
	publicFn := &Function{ID: "fn-id", Namespace: "anchat-test", Name: "ping", IsPublic: true}
	inv := &Invoker{registry: &invokeMockRegistry{fn: publicFn}, logger: zap.NewNop()}

	for _, req := range []*InvokeRequest{
		{Namespace: "anchat-test", FunctionName: "ping", TriggerType: TriggerTypeCron, SystemOriginated: true},
		{Namespace: "anchat-test", FunctionName: "ping", TriggerType: TriggerTypeHTTP},
	} {
		if _, err := inv.Invoke(cancelledCtx(), req); errors.Is(err, ErrUnauthorized) {
			t.Errorf("a public function refused %s: %v", req.TriggerType, err)
		}
	}
}

// bugboard #152: an internal function is for admins and for the gateway. A
// non-admin caller with a real identity — which a merely private function would
// accept — must not reach it.
func TestInvoke_internalFunctionGate(t *testing.T) {
	internalFn := &Function{
		ID: "fn-id", Namespace: "anchat-test", Name: "migrate",
		IsPublic: false, IsInternal: true,
	}
	inv := &Invoker{registry: &invokeMockRegistry{fn: internalFn}, logger: zap.NewNop()}

	for _, tc := range []struct {
		name     string
		req      InvokeRequest
		wantAuth bool
	}{
		{"identified non-admin caller denied",
			InvokeRequest{TriggerType: TriggerTypeHTTP, CallerWallet: "0xAppRuntime", CallerHasInvoke: true}, true},
		{"admin caller allowed",
			InvokeRequest{TriggerType: TriggerTypeHTTP, CallerWallet: "0xAdmin", CallerIsAdmin: true}, false},
		{"gateway-started invocation allowed",
			InvokeRequest{TriggerType: TriggerTypeCron, SystemOriginated: true}, false},
		{"a request that only claims to be a cron is denied",
			InvokeRequest{TriggerType: TriggerTypeCron}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			req.Namespace = "anchat-test"
			req.FunctionName = "migrate"
			resp, err := inv.Invoke(cancelledCtx(), &req)

			if tc.wantAuth {
				if !errors.Is(err, ErrUnauthorized) {
					t.Errorf("err=%v, want ErrUnauthorized", err)
				}
				if resp == nil || resp.Error != "unauthorized" {
					t.Errorf("response = %+v", resp)
				}
			} else if errors.Is(err, ErrUnauthorized) {
				t.Errorf("wrongly refused: %+v", resp)
			}
		})
	}
}

func TestInvoke_anAuthenticatedCallerReachesAPrivateFunction(t *testing.T) {
	privateFn := &Function{ID: "fn-id", Namespace: "anchat-test", Name: "user-create", IsPublic: false}
	inv := &Invoker{registry: &invokeMockRegistry{fn: privateFn}, logger: zap.NewNop()}

	_, err := inv.Invoke(cancelledCtx(), &InvokeRequest{
		Namespace:       "anchat-test",
		FunctionName:    "user-create",
		TriggerType:     TriggerTypeHTTP,
		CallerWallet:    "0xRealUser",
		CallerHasInvoke: true,
	})
	if errors.Is(err, ErrUnauthorized) {
		t.Errorf("an authenticated caller with the invoke grant was refused: %v", err)
	}
}

// The authority to skip the caller check must stay something only the gateway's
// own dispatchers set. This walks the source rather than the call graph,
// because a new dispatcher is exactly the place the flag would be added without
// anyone noticing, and because a request built from anything a caller sends
// must never carry it.
func TestSystemOriginated_isOnlySetByGatewayInternalDispatchers(t *testing.T) {
	allowed := map[string]bool{
		// The cron scheduler firing a registered row.
		"../serverless/triggers/cron_scheduler.go": true,
		// The pubsub dispatcher matching a registered trigger.
		"../serverless/triggers/dispatcher.go": true,
		// The gateway asking a fixed function for extra JWT claims.
		"../gateway/claims_provider.go": true,
		// A nested call inheriting its parent's, never inventing one.
		"../serverless/hostfunctions/context.go": true,
		// This package: the field's definition and its copy into the
		// invocation context.
		"invoke.go": true,
		"types.go":  true,
	}

	found := map[string]int{}
	roots := []string{".", "../serverless/triggers", "../serverless/hostfunctions", "../gateway"}
	for _, root := range roots {
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, root, func(fi fs.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", root, err)
		}
		for _, pkg := range pkgs {
			for path, file := range pkg.Files {
				ast.Inspect(file, func(n ast.Node) bool {
					kv, ok := n.(*ast.KeyValueExpr)
					if !ok {
						return true
					}
					ident, ok := kv.Key.(*ast.Ident)
					if !ok || ident.Name != "SystemOriginated" {
						return true
					}
					found[path]++
					return true
				})
			}
		}
	}

	if len(found) == 0 {
		t.Fatal("no assignment of SystemOriginated was found at all; this test is not looking where it thinks it is")
	}
	for path := range found {
		normalized := strings.TrimPrefix(path, "./")
		if !allowed[normalized] {
			t.Errorf("%s sets SystemOriginated. Only a gateway-internal dispatcher may: it is the "+
				"authority to skip the caller check. If this is a new dispatcher, add it to the list "+
				"above and say why it has that authority.", normalized)
		}
	}
}

// What the running function carries is what its own nested calls will be
// authorized on. A field dropped here is a grant lost one hop later, or an
// authority handed over that was never given.
func TestNewInvocationContext_carriesTheCallersIdentityAndGrants(t *testing.T) {
	fn := &Function{ID: "fn-1", Name: "settle", Namespace: "anchat-test"}
	req := &InvokeRequest{
		CallerWallet:     "0xUser",
		CallerIP:         "203.0.113.4",
		CallerIsAdmin:    true,
		CallerHasInvoke:  true,
		SystemOriginated: true,
		TriggerType:      TriggerTypeCron,
		WSClientID:       "client-9",
		CallerClaims:     map[string]string{"tier": "pro"},
		CallerJWTSubject: "0xUser",
		TriggerDepth:     2,
	}
	got := newInvocationContext(req, fn, "req-1", map[string]string{"K": "v"})

	if got.CallerWallet != "0xUser" {
		t.Errorf("caller wallet = %q", got.CallerWallet)
	}
	if !got.CallerIsAdmin {
		t.Error("the admin bit was dropped")
	}
	if !got.CallerHasInvoke {
		t.Error("the invoke grant was dropped, so a nested call would be refused for a caller who may run this function")
	}
	if !got.SystemOriginated {
		t.Error("the gateway's authority was dropped, so a nested call from a cron would be refused")
	}
	if got.TriggerType != TriggerTypeCron {
		t.Errorf("trigger type = %q", got.TriggerType)
	}
	if got.CallerIP != "203.0.113.4" || got.WSClientID != "client-9" ||
		got.CallerJWTSubject != "0xUser" || got.TriggerDepth != 2 {
		t.Errorf("context = %+v", got)
	}
	if got.CallerClaims["tier"] != "pro" {
		t.Errorf("caller claims = %#v", got.CallerClaims)
	}
	if got.EnvVars["K"] != "v" {
		t.Errorf("env vars = %#v", got.EnvVars)
	}
	if got.RequestID != "req-1" || got.FunctionID != "fn-1" ||
		got.FunctionName != "settle" || got.Namespace != "anchat-test" {
		t.Errorf("context = %+v", got)
	}
}

// A caller-started request must not produce a context that says otherwise.
func TestNewInvocationContext_doesNotInventAuthority(t *testing.T) {
	fn := &Function{ID: "fn-1", Name: "settle", Namespace: "anchat-test"}
	got := newInvocationContext(&InvokeRequest{TriggerType: TriggerTypeHTTP}, fn, "req-1", nil)

	if got.SystemOriginated {
		t.Error("a caller-started request produced a gateway-started context")
	}
	if got.CallerIsAdmin || got.CallerHasInvoke {
		t.Error("a request with no grants produced a context with grants")
	}
}
