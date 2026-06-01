package hostfunctions

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// feat-6 / feat-12: function_invoke_async lets a single stateful dispatcher
// (rpc-router) fan out slow per-RPC handlers WITHOUT freezing its serial frame
// loop. These pin: the target runs with inherited identity, the call returns
// immediately, the payload is copied before return (guest memory is reused),
// missing wiring is rejected, and the in-flight cap applies backpressure.

// recordingInvoker captures Invoke calls. When blockOn is non-nil the
// goroutine inside Invoke blocks on it — used to hold in-flight slots for the
// backpressure test.
type recordingInvoker struct {
	mu      sync.Mutex
	reqs    []*serverless.InvokeRequest
	called  chan *serverless.InvokeRequest
	blockOn chan struct{}
}

func (r *recordingInvoker) Invoke(ctx context.Context, req *serverless.InvokeRequest) (*serverless.InvokeResponse, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	if r.called != nil {
		r.called <- req
	}
	if r.blockOn != nil {
		<-r.blockOn
	}
	return &serverless.InvokeResponse{Status: serverless.InvocationStatusSuccess}, nil
}

func newAsyncHF(inv serverless.FunctionInvoker, semSize int) *HostFunctions {
	h := &HostFunctions{logger: zap.NewNop()}
	if semSize > 0 {
		h.asyncInvokeSem = make(chan struct{}, semSize)
	}
	if inv != nil {
		h.SetInvoker(inv)
	}
	return h
}

func asyncCtx() context.Context {
	return serverless.WithInvocationContext(context.Background(), &serverless.InvocationContext{
		Namespace:    "ns-test",
		WSClientID:   "client-1",
		CallerWallet: "0xwallet",
	})
}

func TestFunctionInvokeAsync_runsTargetWithInheritedIdentity(t *testing.T) {
	inv := &recordingInvoker{called: make(chan *serverless.InvokeRequest, 1)}
	h := newAsyncHF(inv, 4)

	if err := h.FunctionInvokeAsync(asyncCtx(), "sync-deltas", []byte(`{"x":1}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case req := <-inv.called:
		if req.FunctionName != "sync-deltas" {
			t.Errorf("FunctionName = %q; want sync-deltas", req.FunctionName)
		}
		if req.Namespace != "ns-test" {
			t.Errorf("Namespace = %q; want ns-test", req.Namespace)
		}
		if req.WSClientID != "client-1" {
			t.Errorf("WSClientID = %q; want client-1 (inherited so target can ws_send its own reply)", req.WSClientID)
		}
		if req.CallerWallet != "0xwallet" {
			t.Errorf("CallerWallet = %q; want 0xwallet", req.CallerWallet)
		}
		if string(req.Input) != `{"x":1}` {
			t.Errorf("Input = %q; want {\"x\":1}", req.Input)
		}
		if req.TriggerType != serverless.TriggerTypeWebSocket {
			t.Errorf("TriggerType = %v; want WebSocket", req.TriggerType)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("target was never invoked")
	}
}

func TestFunctionInvokeAsync_noInvokerReturnsError(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop(), asyncInvokeSem: make(chan struct{}, 4)}
	if err := h.FunctionInvokeAsync(asyncCtx(), "x", nil); err == nil {
		t.Fatal("expected an error when no invoker is wired")
	}
}

func TestFunctionInvokeAsync_noInvocationContextReturnsError(t *testing.T) {
	h := newAsyncHF(&recordingInvoker{}, 4)
	if err := h.FunctionInvokeAsync(context.Background(), "x", nil); err == nil {
		t.Fatal("expected an error when there is no invocation context")
	}
}

func TestFunctionInvokeAsync_backpressureWhenSaturated(t *testing.T) {
	block := make(chan struct{})
	inv := &recordingInvoker{called: make(chan *serverless.InvokeRequest, 1), blockOn: block}
	h := newAsyncHF(inv, 1) // single in-flight slot

	// First call acquires the only slot; its goroutine blocks inside Invoke.
	if err := h.FunctionInvokeAsync(asyncCtx(), "slow", nil); err != nil {
		t.Fatalf("first call should be accepted: %v", err)
	}
	<-inv.called // ensure the goroutine has entered Invoke and is holding the slot

	// Second call: the cap is reached → must be rejected (backpressure).
	if err := h.FunctionInvokeAsync(asyncCtx(), "slow2", nil); err == nil {
		t.Fatal("expected backpressure rejection when the in-flight cap is reached")
	}

	// Release the first invocation so its slot frees and the goroutine exits.
	close(block)
}

func TestFunctionInvokeAsync_slotReclaimedAfterCompletion(t *testing.T) {
	// Proves the defer-release returns the slot: with a single-slot cap, a
	// second call must succeed once the first target has finished.
	inv := &recordingInvoker{called: make(chan *serverless.InvokeRequest, 2)}
	h := newAsyncHF(inv, 1)

	if err := h.FunctionInvokeAsync(asyncCtx(), "first", nil); err != nil {
		t.Fatalf("first call should be accepted: %v", err)
	}
	<-inv.called // first target ran (non-blocking invoker) → its slot is freed on return

	// Retry until the deferred release has run (the goroutine releases the
	// slot just after Invoke returns; poll briefly to avoid a timing flake).
	deadline := time.Now().Add(2 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		if err = h.FunctionInvokeAsync(asyncCtx(), "second", nil); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("second call should succeed after the first slot is reclaimed; got %v", err)
	}
	<-inv.called
}

func TestFunctionInvokeAsync_copiesPayloadBeforeReturn(t *testing.T) {
	inv := &recordingInvoker{called: make(chan *serverless.InvokeRequest, 1)}
	h := newAsyncHF(inv, 4)

	payload := []byte("original")
	if err := h.FunctionInvokeAsync(asyncCtx(), "x", payload); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Simulate the guest reusing its memory the instant the host call returns.
	for i := range payload {
		payload[i] = 'X'
	}

	select {
	case req := <-inv.called:
		if string(req.Input) != "original" {
			t.Errorf("payload was not copied before return; target saw %q (guest-memory reuse corrupted it)", req.Input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("target was never invoked")
	}
}
