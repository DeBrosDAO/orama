package hostfunctions

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// nestedRecordingInvoker records the request a nested function_invoke builds, which
// is what carries the callee's authorization.
type nestedRecordingInvoker struct {
	mu   sync.Mutex
	reqs []*serverless.InvokeRequest
}

func (c *nestedRecordingInvoker) Invoke(_ context.Context, req *serverless.InvokeRequest) (*serverless.InvokeResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	copied := *req
	c.reqs = append(c.reqs, &copied)
	return &serverless.InvokeResponse{Status: serverless.InvocationStatusSuccess}, nil
}

func (c *nestedRecordingInvoker) last() *serverless.InvokeRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reqs) == 0 {
		return nil
	}
	return c.reqs[len(c.reqs)-1]
}

func hostWithInvoker(inv *nestedRecordingInvoker) *HostFunctions {
	h := &HostFunctions{logger: zap.NewNop(), asyncInvokeSem: make(chan struct{}, 4)}
	h.SetInvoker(inv)
	return h
}

func parentCtx(parent *serverless.InvocationContext) context.Context {
	return serverless.WithInvocationContext(context.Background(), parent)
}

// bugboard #159: a cron-triggered function could not invoke another function.
// The nested call was rebuilt as an ordinary external one and every non-public
// callee was refused, so AnChat's cron reconciler reported success on ~25 runs
// while settling zero payments.
//
// The authority is the parent's own, carried across explicitly.
func TestFunctionInvoke_aGatewayStartedParentPassesOnItsAuthority(t *testing.T) {
	inv := &nestedRecordingInvoker{}
	h := hostWithInvoker(inv)

	ctx := parentCtx(&serverless.InvocationContext{
		Namespace:        "anchat-test",
		TriggerType:      serverless.TriggerTypeCron,
		SystemOriginated: true,
	})
	if _, err := h.FunctionInvoke(ctx, "settle-payments", []byte(`{}`)); err != nil {
		t.Fatalf("FunctionInvoke: %v", err)
	}

	req := inv.last()
	if req == nil {
		t.Fatal("no request was made")
	}
	if !req.SystemOriginated {
		t.Error("a cron-started parent's nested call lost the gateway's authority")
	}
	if req.TriggerType != serverless.TriggerTypeInternal {
		t.Errorf("trigger type = %q, want %q — a function_invoke is an internal call", req.TriggerType, serverless.TriggerTypeInternal)
	}
	if req.Namespace != "anchat-test" {
		t.Errorf("namespace = %q; a nested call stays in its parent's namespace", req.Namespace)
	}
}

// bugboard #152: the boundary that must not move. A caller-started invocation
// never becomes a gateway-started one, however many hops it makes.
func TestFunctionInvoke_aCallerStartedParentNeverGainsAuthority(t *testing.T) {
	for _, parent := range []serverless.TriggerType{
		serverless.TriggerTypeHTTP,
		serverless.TriggerTypeWebSocket,
		serverless.TriggerTypeInternal, // the label alone is not authority
		serverless.TriggerType(""),
		serverless.TriggerType("something-new"),
	} {
		t.Run(string(parent), func(t *testing.T) {
			inv := &nestedRecordingInvoker{}
			h := hostWithInvoker(inv)

			ctx := parentCtx(&serverless.InvocationContext{
				Namespace:    "anchat-test",
				TriggerType:  parent,
				CallerWallet: "0xExternal",
			})
			if _, err := h.FunctionInvoke(ctx, "migrate", []byte(`{}`)); err != nil {
				t.Fatalf("FunctionInvoke: %v", err)
			}
			if inv.last().SystemOriginated {
				t.Errorf("a %q parent's nested call claimed the gateway's authority", parent)
			}
		})
	}
}

// A caller who may run a function directly must not be refused by that same
// function's own nested call. The grant was simply not carried.
func TestFunctionInvoke_carriesTheCallersGrants(t *testing.T) {
	inv := &nestedRecordingInvoker{}
	h := hostWithInvoker(inv)

	ctx := parentCtx(&serverless.InvocationContext{
		Namespace:       "anchat-test",
		TriggerType:     serverless.TriggerTypeHTTP,
		CallerWallet:    "0xUser",
		CallerIsAdmin:   true,
		CallerHasInvoke: true,
	})
	if _, err := h.FunctionInvoke(ctx, "callee", []byte(`{}`)); err != nil {
		t.Fatalf("FunctionInvoke: %v", err)
	}

	req := inv.last()
	if req.CallerWallet != "0xUser" {
		t.Errorf("caller wallet = %q", req.CallerWallet)
	}
	if !req.CallerIsAdmin {
		t.Error("the caller's admin bit was dropped")
	}
	if !req.CallerHasInvoke {
		t.Error("the caller's invoke grant was dropped, so a caller who may run this function directly is refused by its own nested call")
	}
}

func TestFunctionInvokeAsync_carriesTheSameAuthorityAsTheSyncPath(t *testing.T) {
	inv := &nestedRecordingInvoker{}
	h := hostWithInvoker(inv)

	ctx := parentCtx(&serverless.InvocationContext{
		Namespace:        "anchat-test",
		TriggerType:      serverless.TriggerTypeCron,
		SystemOriginated: true,
		CallerHasInvoke:  true,
	})
	if err := h.FunctionInvokeAsync(ctx, "fanout", []byte(`{}`)); err != nil {
		t.Fatalf("FunctionInvokeAsync: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && inv.last() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	req := inv.last()
	if req == nil {
		t.Fatal("the async invoke never reached the invoker")
	}
	if !req.SystemOriginated {
		t.Error("the async path lost the gateway's authority")
	}
	if !req.CallerHasInvoke {
		t.Error("the async path dropped the caller's invoke grant")
	}
	if req.TriggerType != serverless.TriggerTypeInternal {
		t.Errorf("trigger type = %q, want %q", req.TriggerType, serverless.TriggerTypeInternal)
	}
}
