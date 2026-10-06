package serverless

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

// countingRegistry counts the lookups that reach it; anything else panics.
type countingRegistry struct {
	FunctionRegistry
	fn   *Function
	gets int
}

func (c *countingRegistry) Get(_ context.Context, _, _ string, _ int) (*Function, error) {
	c.gets++
	return c.fn, nil
}

// bugboard #427: the cluster gateway ran every namespace's functions. The
// cron scheduler, the pubsub dispatcher and the claims provider reach the
// invoker directly, so a tenant row left in the registry kept running there.
// None of those paths may run another namespace's function.
func TestInvoke_refusesAnotherNamespacesFunction(t *testing.T) {
	reg := &countingRegistry{fn: &Function{ID: "fn-1", Namespace: "tenant-a", Name: "nightly", IsPublic: true}}
	inv := NewInvoker(nil, reg, nil, "default", zap.NewNop())

	for _, req := range []*InvokeRequest{
		{Namespace: "tenant-a", FunctionName: "nightly", TriggerType: TriggerTypeCron, SystemOriginated: true},
		{Namespace: "tenant-a", FunctionName: "auth-claims-provider", TriggerType: TriggerTypeInternal, SystemOriginated: true},
		{Namespace: "tenant-a", FunctionName: "nightly", TriggerType: TriggerTypeHTTP},
	} {
		resp, err := inv.Invoke(context.Background(), req)
		if !errors.Is(err, ErrNamespaceNotServed) {
			t.Errorf("%s/%s (%s): err = %v, want ErrNamespaceNotServed", req.Namespace, req.FunctionName, req.TriggerType, err)
		}
		if !IsNotFound(err) {
			t.Errorf("%s: a function this gateway does not run should read as not found here: %v", req.FunctionName, err)
		}
		if resp == nil || resp.Status != InvocationStatusError || resp.RequestID == "" {
			t.Errorf("%s: response %+v, want an error response with a request id", req.FunctionName, resp)
		}
	}
	if reg.gets != 0 {
		t.Errorf("the registry was read %d times for functions this gateway does not run", reg.gets)
	}
}

// The gateway's own namespace gets past the check to the registry.
func TestInvoke_servesItsOwnNamespace(t *testing.T) {
	reg := &countingRegistry{fn: &Function{ID: "fn-1", Namespace: "tenant-a", Name: "ping", IsPublic: true}}
	inv := NewInvoker(nil, reg, nil, " tenant-a ", zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stop before the engine, which is nil here
	_, err := inv.Invoke(ctx, &InvokeRequest{Namespace: "tenant-a", FunctionName: "ping", TriggerType: TriggerTypeHTTP})

	if errors.Is(err, ErrNamespaceNotServed) {
		t.Fatalf("the gateway's own namespace was refused: %v", err)
	}
	if reg.gets != 1 {
		t.Errorf("registry lookups = %d, want 1", reg.gets)
	}
}

// An invoker built without a namespace serves none.
func TestInvoke_unconfiguredNamespaceServesNothing(t *testing.T) {
	inv := NewInvoker(nil, &countingRegistry{}, nil, "", zap.NewNop())

	_, err := inv.Invoke(context.Background(), &InvokeRequest{Namespace: "default", FunctionName: "x"})

	if !errors.Is(err, ErrNamespaceNotServed) {
		t.Errorf("err = %v, want ErrNamespaceNotServed", err)
	}
}
