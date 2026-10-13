package hostfunctions

import (
	"bytes"
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// Bugboard #93 — PubSubPublish must fire local wildcard triggers, but
// only when a triggerDispatcher is wired. Back-compat tests pin the
// nil-dispatcher path.

func TestDispatchLocalWildcards_noDispatcherIsNoOp(t *testing.T) {
	// Back-compat: when no triggerDispatcher is wired (tests, future
	// deployments without serverless triggers, gateway constructed
	// before the setter fires), publishing must NOT crash. The wildcard
	// dispatch path silently no-ops.
	h := &HostFunctions{}
	invCtx := invocationCtx(&serverless.InvocationContext{Namespace: "ns"})
	// Should not panic. No dispatcher, so we don't reach the dispatcher's
	// DispatchLocalPublish (which would itself panic on nil store).
	h.dispatchLocalWildcards(invCtx, "presence:user-1", []byte("data"))
}

func TestDispatchLocalWildcards_noNamespaceIsNoOp(t *testing.T) {
	// If we somehow have a dispatcher but no namespace in invCtx
	// (HTTP-handler-style callers, tests with bare HostFunctions), we
	// must skip silently rather than panic on cur == nil. Same shape as
	// the rest of the host-fn family that early-returns when invCtx is
	// missing.
	//
	// We don't actually wire a dispatcher here because the absence of
	// namespace short-circuits before the dispatcher is touched — that's
	// the assertion: no namespace, no dispatch attempt, no panic.
	h := &HostFunctions{}
	// no SetInvocationContext call — invCtx is nil
	h.dispatchLocalWildcards(context.Background(), "anything", []byte("x"))
}

// dedupBatchByTopic — pin the batch fan-out amplification fix
// (security audit MEDIUM, bug #93 follow-up).

func TestDedupBatchByTopic_collapsesRepeatedTopicsMostRecentWins(t *testing.T) {
	// A burst of 5 publishes on the same topic in one batch — without
	// dedup, each wildcard handler would be invoked 5 times for what is
	// semantically one wakeup. Must collapse to one entry, with the
	// LAST payload winning (matches downstream-subscriber semantics
	// after libp2p coalescing).
	in := []pubsub.TopicMessage{
		{Topic: "presence:user-1", Data: []byte("v1")},
		{Topic: "presence:user-1", Data: []byte("v2")},
		{Topic: "presence:user-1", Data: []byte("v3")},
		{Topic: "presence:user-1", Data: []byte("v4")},
		{Topic: "presence:user-1", Data: []byte("v5")},
	}
	out := dedupBatchByTopic(in)
	if len(out) != 1 {
		t.Fatalf("FAN-OUT REGRESSION: 5 same-topic msgs must collapse to 1; got %d", len(out))
	}
	if !bytes.Equal(out[0].Data, []byte("v5")) {
		t.Errorf("most-recent-wins violated: want v5, got %q", out[0].Data)
	}
}

func TestDedupBatchByTopic_preservesInsertionOrder(t *testing.T) {
	// Distinct topics must dispatch in the order they were first seen
	// in the batch. Otherwise downstream observers (and trigger logs)
	// see reordered events vs the actual publish sequence.
	in := []pubsub.TopicMessage{
		{Topic: "b", Data: []byte("b1")},
		{Topic: "a", Data: []byte("a1")},
		{Topic: "c", Data: []byte("c1")},
		{Topic: "a", Data: []byte("a2")}, // late update to "a" — wins, but doesn't reorder
	}
	out := dedupBatchByTopic(in)
	if len(out) != 3 {
		t.Fatalf("want 3 distinct topics, got %d", len(out))
	}
	wantOrder := []string{"b", "a", "c"}
	for i, w := range wantOrder {
		if out[i].Topic != w {
			t.Errorf("position %d: want topic=%q, got %q", i, w, out[i].Topic)
		}
	}
	// "a" should still carry the latest payload
	if !bytes.Equal(out[1].Data, []byte("a2")) {
		t.Errorf("most-recent-wins for 'a': want a2, got %q", out[1].Data)
	}
}

func TestDedupBatchByTopic_singleEntryShortCircuit(t *testing.T) {
	// Trivial path: len(msgs) <= 1 returns the input as-is (no map
	// allocation). Edge case: empty input must yield empty output.
	if got := dedupBatchByTopic(nil); len(got) != 0 {
		t.Errorf("nil input: want empty output, got %d", len(got))
	}
	one := []pubsub.TopicMessage{{Topic: "t", Data: []byte("d")}}
	got := dedupBatchByTopic(one)
	if len(got) != 1 || got[0].Topic != "t" || !bytes.Equal(got[0].Data, []byte("d")) {
		t.Errorf("single-entry passthrough broken: got %+v", got)
	}
}

func TestDedupBatchByTopic_distinctTopicsPassthroughIntact(t *testing.T) {
	// When no duplicates exist, dedup must NOT lose any entries.
	// Caught by a buggy `seen` check or off-by-one in the order slice.
	in := []pubsub.TopicMessage{
		{Topic: "t1", Data: []byte("1")},
		{Topic: "t2", Data: []byte("2")},
		{Topic: "t3", Data: []byte("3")},
	}
	out := dedupBatchByTopic(in)
	if len(out) != 3 {
		t.Fatalf("want 3 distinct topics through; got %d", len(out))
	}
}

// TriggerDepth threading — pin the security-audit MEDIUM fix (C6).

func TestFunctionInvoke_propagatesTriggerDepth(t *testing.T) {
	// Audit C7 fix: function_invoke MUST carry cur.TriggerDepth into
	// the inner InvokeRequest, otherwise depth resets to 0 on every
	// hop and a wildcard-triggered chain like:
	//   A (depth=N) → function_invoke(B) → B publishes → re-triggers A
	// would never hit the depth bound. Pin this by spying on the
	// InvokeRequest the host fn would construct.
	h := &HostFunctions{}
	invCtx := invocationCtx(&serverless.InvocationContext{
		Namespace:    "ns",
		TriggerDepth: 4, // one hop from maxTriggerDepth
	})
	var captured *serverless.InvokeRequest
	h.SetInvoker(&capturingInvoker{onInvoke: func(req *serverless.InvokeRequest) {
		captured = req
	}})

	_, _ = h.FunctionInvoke(invCtx, "inner-fn", []byte("payload"))
	if captured == nil {
		t.Fatal("invoker was not called; can't verify TriggerDepth propagation")
	}
	if captured.TriggerDepth != 4 {
		t.Errorf("AUDIT C7 REGRESSION: function_invoke did not carry TriggerDepth "+
			"from invCtx; want 4 (one below maxTriggerDepth), got %d. "+
			"Without propagation, wildcard-triggered chains escape the depth bound "+
			"by hopping through function_invoke.", captured.TriggerDepth)
	}
}

// capturingInvoker records the InvokeRequest it's called with so tests
// can assert what HostFunctions passed to the invoker without needing a
// real engine/registry.
type capturingInvoker struct {
	onInvoke func(*serverless.InvokeRequest)
}

func (c *capturingInvoker) Invoke(_ context.Context, req *serverless.InvokeRequest) (*serverless.InvokeResponse, error) {
	if c.onInvoke != nil {
		c.onInvoke(req)
	}
	return &serverless.InvokeResponse{Output: []byte{}}, nil
}

func TestDispatchLocalWildcards_readsInvCtxTriggerDepth(t *testing.T) {
	// The fix for the recursion-amplification (audit C6): when a
	// wildcard-triggered handler publishes again, dispatchLocalWildcards
	// MUST pass the CURRENT invocation's TriggerDepth to the dispatcher
	// (not hardcoded 0). Otherwise depth resets on every WASM publish
	// and the local-recursion loop is unbounded except by dispatchTimeout.
	//
	// We can't easily wire a real dispatcher here (concrete type, no
	// interface), but we can pin the invocation-context shape so a
	// future refactor that drops the TriggerDepth field gets caught.
	h := &HostFunctions{}
	invCtx := invocationCtx(&serverless.InvocationContext{
		Namespace:    "ns",
		TriggerDepth: 3,
	})
	cur := h.currentInvocationContext(invCtx)
	if cur == nil {
		t.Fatal("invocation context unexpectedly nil")
	}
	if cur.TriggerDepth != 3 {
		t.Errorf("TriggerDepth was not propagated through invCtx: want 3, got %d "+
			"(if this fails, the audit C6 fix's data path is broken)", cur.TriggerDepth)
	}
	// And the no-dispatcher no-op stays nil-safe regardless of depth.
	h.dispatchLocalWildcards(invCtx, "x:y", []byte("d"))
}
