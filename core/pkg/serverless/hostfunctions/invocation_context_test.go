package hostfunctions

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// TestCurrentInvocationContext_CtxOverridesSingleton verifies the basic
// precedence rule: when a ctx carries an invCtx via
// serverless.WithInvocationContext, host accessors must read from the
// ctx and ignore the singleton field.
//
// Without this precedence, the cross-tenant identity-leak fix is moot —
// every accessor would still read whatever the LAST persistent WS
// connection wrote to the singleton.
func TestCurrentInvocationContext_CtxOverridesSingleton(t *testing.T) {
	h := &HostFunctions{}

	// Singleton has identity for "userA".
	h.SetInvocationContext(&serverless.InvocationContext{
		CallerJWTSubject: "userA",
		WSClientID:       "clientA",
		Namespace:        "nsA",
	})

	// ctx carries identity for "userB" — what a per-instance persistent
	// WS connection's ctx would carry.
	ctxB := serverless.WithInvocationContext(context.Background(), &serverless.InvocationContext{
		CallerJWTSubject: "userB",
		WSClientID:       "clientB",
		Namespace:        "nsB",
	})

	if got := h.GetCallerJWTSubject(ctxB); got != "userB" {
		t.Errorf("ctx-attached invCtx must win over singleton: got %q, want %q (cross-tenant leak)", got, "userB")
	}
	if got := h.GetWSClientID(ctxB); got != "clientB" {
		t.Errorf("ctx-attached invCtx must win over singleton: got %q, want %q", got, "clientB")
	}

	// Sanity: singleton path still works for callers that don't propagate ctx.
	if got := h.GetCallerJWTSubject(context.Background()); got != "userA" {
		t.Errorf("singleton fallback broke: got %q, want %q", got, "userA")
	}
}

// TestCurrentInvocationContext_NilInvCtxReturnsCtxUnchanged verifies the
// guard inside WithInvocationContext: passing nil must not panic and must
// not attach a typed-nil to the ctx (which would defeat the
// InvocationContextFromCtx nil check).
func TestCurrentInvocationContext_NilInvCtxReturnsCtxUnchanged(t *testing.T) {
	h := &HostFunctions{}
	h.SetInvocationContext(&serverless.InvocationContext{CallerJWTSubject: "fallback"})

	// nil invCtx → ctx unchanged → falls back to singleton.
	ctx := serverless.WithInvocationContext(context.Background(), nil)
	if got := h.GetCallerJWTSubject(ctx); got != "fallback" {
		t.Errorf("nil invCtx should fall through to singleton: got %q, want %q", got, "fallback")
	}
}

// TestCurrentInvocationContext_NoCtxNoSingletonReturnsEmpty verifies the
// "no caller context anywhere" path returns clean zero values rather than
// panicking on nil dereference.
func TestCurrentInvocationContext_NoCtxNoSingletonReturnsEmpty(t *testing.T) {
	h := &HostFunctions{}
	if got := h.GetCallerJWTSubject(context.Background()); got != "" {
		t.Errorf("no invCtx should return empty: got %q", got)
	}
	if got := h.GetCallerWallet(context.Background()); got != "" {
		t.Errorf("no invCtx should return empty: got %q", got)
	}
}

// TestCurrentInvocationContext_NoCrossTenantLeak_Concurrent is the actual
// regression test for the cross-tenant identity-leak race. Without the
// per-call ctx propagation, two concurrent goroutines reading from a
// shared HostFunctions would observe each other's invCtx whenever
// SetInvocationContext was called between their reads.
//
// With the fix in place, each goroutine carries its own invCtx in its ctx
// and the singleton-field race is bypassed entirely. We assert that NO
// goroutine ever reads any other goroutine's identity.
//
// Run with -race for stronger signal — the race detector will also flag
// the underlying singleton field if anyone mutates it concurrently.
func TestCurrentInvocationContext_NoCrossTenantLeak_Concurrent(t *testing.T) {
	h := &HostFunctions{}

	const (
		numGoroutines = 32
		opsPerRoutine = 200
	)

	var leaks int64
	var wg sync.WaitGroup
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()

			myInvCtx := &serverless.InvocationContext{
				CallerJWTSubject: subjectForGoroutine(gid),
				WSClientID:       clientForGoroutine(gid),
				Namespace:        "ns-" + clientForGoroutine(gid),
				CallerWallet:     "wallet-" + itoa(gid),
				RequestID:        "req-" + itoa(gid),
				CallerClaims:     map[string]string{"tier": "tier-" + itoa(gid)},
				EnvVars:          map[string]string{"ENV_KEY": "env-" + itoa(gid)},
			}
			ctx := serverless.WithInvocationContext(context.Background(), myInvCtx)

			// Cover every accessor that previously read h.invCtx
			// directly. If any future regression special-cases ONE
			// accessor to bypass currentInvocationContext, this test
			// will catch it. (Earlier versions only checked 3
			// accessors — security audit follow-up.)
			for op := 0; op < opsPerRoutine; op++ {
				checks := map[string]string{
					"GetCallerJWTSubject": h.GetCallerJWTSubject(ctx),
					"GetWSClientID":       h.GetWSClientID(ctx),
					"GetCallerWallet":     h.GetCallerWallet(ctx),
					"GetCallerClaim":      h.GetCallerClaim(ctx, "tier"),
					"GetRequestID":        h.GetRequestID(ctx),
					"namespaceFromCtx":    h.namespaceFromCtx(ctx),
				}
				expected := map[string]string{
					"GetCallerJWTSubject": myInvCtx.CallerJWTSubject,
					"GetWSClientID":       myInvCtx.WSClientID,
					"GetCallerWallet":     myInvCtx.CallerWallet,
					"GetCallerClaim":      myInvCtx.CallerClaims["tier"],
					"GetRequestID":        myInvCtx.RequestID,
					"namespaceFromCtx":    myInvCtx.Namespace,
				}
				for name, got := range checks {
					if got != expected[name] {
						atomic.AddInt64(&leaks, 1)
						t.Errorf("goroutine %d %s leaked: got=%q want=%q", gid, name, got, expected[name])
						return
					}
				}
				envVal, _ := h.GetEnv(ctx, "ENV_KEY")
				if envVal != myInvCtx.EnvVars["ENV_KEY"] {
					atomic.AddInt64(&leaks, 1)
					t.Errorf("goroutine %d GetEnv leaked: got=%q want=%q", gid, envVal, myInvCtx.EnvVars["ENV_KEY"])
					return
				}
			}
		}(g)
	}

	// Concurrently churn the singleton field so any accessor that
	// accidentally falls back to it would see whatever was set last.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numGoroutines*opsPerRoutine; i++ {
			h.SetInvocationContext(&serverless.InvocationContext{
				CallerJWTSubject: "intruder",
				WSClientID:       "intruder",
				Namespace:        "intruder",
			})
		}
	}()

	wg.Wait()
	if atomic.LoadInt64(&leaks) != 0 {
		t.Fatalf("cross-tenant leak detected in %d operations", atomic.LoadInt64(&leaks))
	}
}

func subjectForGoroutine(g int) string {
	return "subject-" + itoa(g)
}

func clientForGoroutine(g int) string {
	return "client-" + itoa(g)
}

// itoa avoids strconv to keep the test file's deps minimal — small ints only.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
