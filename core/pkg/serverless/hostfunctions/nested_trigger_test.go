package hostfunctions

import (
	"context"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// Bugboard #159 — a cron-triggered function could not FunctionInvoke another
// function. The nested call was hardcoded to TriggerTypeWebSocket, so it
// re-entered the authorization gate as an anonymous external caller
// (CallerWallet "" / CallerIsAdmin false, which a system trigger never carries)
// and every `public: false` callee was rejected. AnChat's cron reconciler
// reported SUCCESS on ~25 runs while settling zero payments.
//
// The invariant: a system-originated parent propagates as a system trigger; an
// externally-triggered parent stays gated (bugboard #152 must not regress).
func TestNestedTriggerType(t *testing.T) {
	systemParents := []serverless.TriggerType{
		serverless.TriggerTypeCron,
		serverless.TriggerTypePubSub,
		serverless.TriggerTypeDatabase,
		serverless.TriggerTypeTimer,
		serverless.TriggerTypeJob,
		serverless.TriggerTypeInternal,
	}
	for _, p := range systemParents {
		got := nestedTriggerType(p)
		if got != serverless.TriggerTypeInternal {
			t.Errorf("parent %q: nested trigger = %q, want %q — a system-triggered parent must be able to invoke a non-public function in its own namespace",
				p, got, serverless.TriggerTypeInternal)
		}
		if !serverless.IsSystemTrigger(got) {
			t.Errorf("parent %q: nested trigger %q must satisfy IsSystemTrigger so the auth gate is skipped", p, got)
		}
	}

	// External parents must remain gated — this is the #152 boundary.
	externalParents := []serverless.TriggerType{
		serverless.TriggerTypeHTTP,
		serverless.TriggerTypeWebSocket,
	}
	for _, p := range externalParents {
		got := nestedTriggerType(p)
		if serverless.IsSystemTrigger(got) {
			t.Errorf("parent %q: nested trigger = %q must NOT be a system trigger — external→internal must stay blocked (bugboard #152)", p, got)
		}
		if got != serverless.TriggerTypeWebSocket {
			t.Errorf("parent %q: nested trigger = %q, want %q (unchanged behavior)", p, got, serverless.TriggerTypeWebSocket)
		}
	}

	// An unknown/empty parent must fail CLOSED (gated), never escalate.
	if serverless.IsSystemTrigger(nestedTriggerType("")) {
		t.Error("an empty parent trigger must not escalate to a system trigger")
	}
	if serverless.IsSystemTrigger(nestedTriggerType("something-new")) {
		t.Error("an unrecognised parent trigger must fail closed, not escalate")
	}
}

// The helper test above pins the DECISION; these pin the CALL SITES. Without
// them, re-hardcoding `TriggerType: TriggerTypeWebSocket` in FunctionInvoke or
// FunctionInvokeAsync would reintroduce bugboard #159 with the helper test
// still green.
func TestFunctionInvoke_propagatesSystemOriginToNestedCall(t *testing.T) {
	inv := &recordingInvoker{}
	h := newAsyncHF(inv, 4)

	// Parent fired by cron — the exact shape of AnChat's payment reconciler.
	ctx := serverless.WithInvocationContext(context.Background(), &serverless.InvocationContext{
		Namespace:   "ns-test",
		TriggerType: serverless.TriggerTypeCron,
	})
	if _, err := h.FunctionInvoke(ctx, "settle-payments", []byte(`{}`)); err != nil {
		t.Fatalf("FunctionInvoke: %v", err)
	}

	inv.mu.Lock()
	defer inv.mu.Unlock()
	if len(inv.reqs) != 1 {
		t.Fatalf("got %d invocations, want 1", len(inv.reqs))
	}
	got := inv.reqs[0]
	if !serverless.IsSystemTrigger(got.TriggerType) {
		t.Errorf("nested TriggerType = %q — a cron parent's nested invoke must stay system-originated, else a public:false callee is refused (bugboard #159)", got.TriggerType)
	}
	if got.Namespace != "ns-test" {
		t.Errorf("nested Namespace = %q, want the parent's own namespace (containment)", got.Namespace)
	}
}

// External parent must remain gated at the call site — the #152 boundary.
func TestFunctionInvoke_externalParentStaysGated(t *testing.T) {
	inv := &recordingInvoker{}
	h := newAsyncHF(inv, 4)

	ctx := serverless.WithInvocationContext(context.Background(), &serverless.InvocationContext{
		Namespace:   "ns-test",
		TriggerType: serverless.TriggerTypeHTTP,
	})
	if _, err := h.FunctionInvoke(ctx, "settle-payments", []byte(`{}`)); err != nil {
		t.Fatalf("FunctionInvoke: %v", err)
	}

	inv.mu.Lock()
	defer inv.mu.Unlock()
	if serverless.IsSystemTrigger(inv.reqs[0].TriggerType) {
		t.Errorf("nested TriggerType = %q — an HTTP-triggered parent must NOT escalate to a system trigger (bugboard #152)", inv.reqs[0].TriggerType)
	}
}

func TestFunctionInvokeAsync_propagatesSystemOriginToNestedCall(t *testing.T) {
	inv := &recordingInvoker{called: make(chan *serverless.InvokeRequest, 1)}
	h := newAsyncHF(inv, 4)

	ctx := serverless.WithInvocationContext(context.Background(), &serverless.InvocationContext{
		Namespace:   "ns-test",
		TriggerType: serverless.TriggerTypePubSub,
	})
	if err := h.FunctionInvokeAsync(ctx, "settle-payments", []byte(`{}`)); err != nil {
		t.Fatalf("FunctionInvokeAsync: %v", err)
	}

	select {
	case req := <-inv.called:
		if !serverless.IsSystemTrigger(req.TriggerType) {
			t.Errorf("async nested TriggerType = %q — a pubsub parent's nested invoke must stay system-originated", req.TriggerType)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("async invoke never reached the invoker")
	}
}
