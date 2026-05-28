package serverless

import (
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Bugboard #24 — slow-invoke diagnostic logging.
//
// The WS handler enforces a 30s ceiling on function-invoke. Pre-#24,
// when that ceiling fired AnChat saw "RPC timeout after 30s" with no
// way to tell whether the engine was blocked in rate-limit checks,
// module compile, or WASM execution itself. Engine.Execute now emits
// a structured "slow serverless invocation" warning above
// slowInvokeThreshold (5s) with per-phase breakdown so the next test
// run gives operators a smoking gun pointing at the actual sink.
//
// These tests pin the log shape so a refactor can't silently drop
// fields AnChat will be looking for.

func TestLogSlowInvocation_belowThresholdEmitsNothing(t *testing.T) {
	// Trivial: fast invocations don't pollute logs. The threshold
	// exists specifically so warning-grade logs stay actionable.
	core, observed := observer.New(zapcore.WarnLevel)
	e := &Engine{logger: zap.New(core)}
	invCtx := &InvocationContext{Namespace: "ns", FunctionName: "fast-fn"}

	now := time.Now()
	e.logSlowInvocation(invCtx, now, now.Add(1*time.Millisecond), now.Add(2*time.Millisecond), now.Add(100*time.Millisecond), "success", nil)

	if got := observed.Len(); got != 0 {
		t.Errorf("fast invocation (100ms < 5s threshold) emitted %d log lines; want 0", got)
	}
}

func TestLogSlowInvocation_aboveThresholdEmitsBreakdown(t *testing.T) {
	// The actual bug-24 diagnostic. Total > 5s → emit warning with
	// ALL phase fields populated so AnChat's next slow-call report
	// pins which layer is the sink.
	core, observed := observer.New(zapcore.WarnLevel)
	e := &Engine{logger: zap.New(core)}
	invCtx := &InvocationContext{
		Namespace:    "anchat-test",
		FunctionName: "signaling.relay",
		RequestID:    "req-abc-123",
		TriggerType:  TriggerTypeWebSocket,
		WSClientID:   "ws-client-xyz",
	}

	// Simulate a 30s-class invocation that spent the bulk in execute.
	start := time.Now().Add(-30 * time.Second)
	ratelimitDone := start.Add(50 * time.Millisecond)
	moduleLoaded := start.Add(150 * time.Millisecond)
	executeDone := start.Add(30 * time.Second)
	e.logSlowInvocation(invCtx, start, ratelimitDone, moduleLoaded, executeDone, "timeout", nil)

	logs := observed.All()
	if len(logs) != 1 {
		t.Fatalf("slow invocation emitted %d log lines; want 1", len(logs))
	}
	got := logs[0]

	// Smoking-gun fields AnChat's diagnostic will read:
	want := map[string]interface{}{
		"namespace":         "anchat-test",
		"function":          "signaling.relay",
		"request_id":        "req-abc-123",
		"ws_client_id":      "ws-client-xyz",
		"invocation_status": "timeout",
	}
	for k, v := range want {
		field, ok := got.ContextMap()[k]
		if !ok {
			t.Errorf("missing field %q in slow-invoke log (AnChat depends on this)", k)
			continue
		}
		if field != v {
			t.Errorf("field %q = %v; want %v", k, field, v)
		}
	}

	// Phase timings — the actual diagnostic value. Total ≈ 30s, with
	// rate-limit + module-load being trivial fractions, so execute_ms
	// should dominate. This tells operators "WASM execution is the
	// sink, not rate-limit or module compile."
	contextMap := got.ContextMap()
	totalMs, _ := contextMap["total_ms"].(int64)
	executeMs, _ := contextMap["execute_ms"].(int64)
	if totalMs < 29000 || totalMs > 31000 {
		t.Errorf("total_ms = %d; want ~30000 for the simulated 30s invocation", totalMs)
	}
	if executeMs < 29000 || executeMs > 30000 {
		t.Errorf("execute_ms = %d; want ~29900 (proves the phase-breakdown points at execute)", executeMs)
	}
}

func TestLogSlowInvocation_zeroPhaseTimestampsMeanUnreached(t *testing.T) {
	// Defensive: if Execute bails early (e.g. module compile fails
	// before WASM runs), executeDoneAt is zero. The log must still
	// emit with executeMs=0 rather than producing negative or absurd
	// values from subtracting zero.Time. This shape lets ops see
	// "we never reached execute" as a distinct signal from "execute
	// was fast."
	core, observed := observer.New(zapcore.WarnLevel)
	e := &Engine{logger: zap.New(core)}
	invCtx := &InvocationContext{Namespace: "ns", FunctionName: "fn"}

	start := time.Now().Add(-10 * time.Second)
	ratelimitDone := start.Add(100 * time.Millisecond)
	// moduleLoadedAt and executeDoneAt left as zero — module-load failed
	e.logSlowInvocation(invCtx, start, ratelimitDone, time.Time{}, time.Time{}, "module-load-failed", nil)

	logs := observed.All()
	if len(logs) != 1 {
		t.Fatalf("want 1 log line; got %d", len(logs))
	}
	cm := logs[0].ContextMap()
	moduleLoadMs, _ := cm["module_load_ms"].(int64)
	executeMs, _ := cm["execute_ms"].(int64)
	if moduleLoadMs != 0 {
		t.Errorf("module_load_ms = %d; want 0 when moduleLoadedAt was never set (signals 'unreached')", moduleLoadMs)
	}
	if executeMs != 0 {
		t.Errorf("execute_ms = %d; want 0 when executeDoneAt was never set", executeMs)
	}
}
