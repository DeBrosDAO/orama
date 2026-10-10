package hostfunctions

import (
	"context"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// get_caller_device_id is to the device what get_caller_jwt_subject is to the
// account: the id of a key the device proved it holds, set by the gateway.

func TestGetCallerDeviceID_returnsTheBoundDevice(t *testing.T) {
	h := &HostFunctions{}
	ctx := invocationCtx(&serverless.InvocationContext{
		CallerJWTSubject: "0xwallet",
		CallerDeviceID:   "kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k",
	})
	if got := h.GetCallerDeviceID(ctx); got != "kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k" {
		t.Errorf("GetCallerDeviceID = %q", got)
	}
}

func TestGetCallerDeviceID_emptyWithoutADeviceOrAContext(t *testing.T) {
	h := &HostFunctions{}
	if got := h.GetCallerDeviceID(invocationCtx(&serverless.InvocationContext{CallerJWTSubject: "0xwallet"})); got != "" {
		t.Errorf("an account-only session reported device %q", got)
	}
	if got := h.GetCallerDeviceID(context.Background()); got != "" {
		t.Errorf("no invocation reported device %q", got)
	}
}

// A nested call runs for the same caller, from the same device. Dropping it
// would have the callee believe the account called from nowhere in particular.
func TestFunctionInvoke_carriesTheCallersDevice(t *testing.T) {
	inv := &nestedRecordingInvoker{}
	h := hostWithInvoker(inv)
	ctx := parentCtx(&serverless.InvocationContext{
		Namespace:        "anchat-test",
		TriggerType:      serverless.TriggerTypeWebSocket,
		CallerJWTSubject: "0xwallet",
		CallerDeviceID:   "device-1",
	})

	if _, err := h.FunctionInvoke(ctx, "callee", []byte(`{}`)); err != nil {
		t.Fatalf("FunctionInvoke: %v", err)
	}
	if got := inv.last().CallerDeviceID; got != "device-1" {
		t.Errorf("sync nested call device = %q", got)
	}

	if err := h.FunctionInvokeAsync(ctx, "callee", []byte(`{}`)); err != nil {
		t.Fatalf("FunctionInvokeAsync: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && requestCount(inv) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if requestCount(inv) < 2 || inv.last().CallerDeviceID != "device-1" {
		t.Errorf("async nested call lost the device: %+v", inv.last())
	}
}

func requestCount(inv *nestedRecordingInvoker) int {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	return len(inv.reqs)
}
