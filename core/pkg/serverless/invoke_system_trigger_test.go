package serverless

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

// TestIsSystemTrigger covers every trigger type exhaustively. The list
// matters: user-driven triggers MUST go through CanInvoke (auth middleware
// is the source of truth for caller identity); system triggers MUST bypass
// it (they have no caller — the trigger row IS the authorization, set at
// registration time).
//
// If a future contributor adds a new TriggerType, this test forces them to
// classify it here. Without that, the default (false → goes through
// CanInvoke) is the safer choice — but if the new type is system-internal
// and the contributor doesn't update isSystemTrigger, the symptom is the
// exact bug we just fixed: every fire returns "unauthorized" silently.
func TestIsSystemTrigger(t *testing.T) {
	cases := []struct {
		trigger TriggerType
		system  bool
	}{
		// User-driven — must NOT be system.
		{TriggerTypeHTTP, false},
		{TriggerTypeWebSocket, false},

		// System-driven — fires from gateway-internal state.
		{TriggerTypeCron, true},
		{TriggerTypePubSub, true},
		{TriggerTypeDatabase, true},
		{TriggerTypeTimer, true},
		{TriggerTypeJob, true},

		// Unknown trigger types default to user-driven (safe default — go
		// through CanInvoke and fail closed if there's no caller).
		{TriggerType("future-unknown"), false},
		{TriggerType(""), false},
	}
	for _, c := range cases {
		got := isSystemTrigger(c.trigger)
		if got != c.system {
			t.Errorf("isSystemTrigger(%q) = %v, want %v", c.trigger, got, c.system)
		}
	}
}

// invokeMockRegistry is a minimal FunctionRegistry that returns a single
// canned function. Anything else panics so accidental drift is loud.
type invokeMockRegistry struct {
	FunctionRegistry // embedded — calling unimplemented methods panics

	fn *Function
}

func (m *invokeMockRegistry) Get(_ context.Context, _, _ string, _ int) (*Function, error) {
	return m.fn, nil
}

// TestInvoke_systemTriggerBypassesAuth is the regression guard for
// bugboard #264: a private function registered with a cron trigger fired
// every minute with `"unauthorized"` because Invoke called CanInvoke with
// an empty CallerWallet, which is a 100% blocker for private functions.
//
// The fix gates CanInvoke on !isSystemTrigger(req.TriggerType). This test
// asserts the gate works for every system trigger type (cron, pubsub,
// database, timer, job) AND that user-driven triggers (http, websocket)
// still hit the auth check.
//
// Implementation note: we use a cancelled ctx so the call short-circuits
// inside executeWithRetry's ctx.Err() check at line 223 BEFORE touching
// engine (which is nil in this test). That lets us distinguish "blocked at
// auth" (err = ErrUnauthorized) from "passed auth, blocked later" (err =
// context.Canceled) without standing up a real WASM engine.
func TestInvoke_systemTriggerBypassesAuth(t *testing.T) {
	privateFn := &Function{
		ID:        "fn-id",
		Namespace: "anchat-test",
		Name:      "push-fanout",
		IsPublic:  false,
	}
	inv := &Invoker{
		registry: &invokeMockRegistry{fn: privateFn},
		logger:   zap.NewNop(),
		// engine intentionally nil — cancelled-ctx short-circuit prevents reach.
	}

	cases := []struct {
		name      string
		trigger   TriggerType
		wantAuth  bool // true → must hit ErrUnauthorized; false → must NOT
	}{
		// System triggers — must bypass auth. The original bug was every
		// one of these returning ErrUnauthorized.
		{"cron bypasses auth", TriggerTypeCron, false},
		{"pubsub bypasses auth", TriggerTypePubSub, false},
		{"database bypasses auth", TriggerTypeDatabase, false},
		{"timer bypasses auth", TriggerTypeTimer, false},
		{"job bypasses auth", TriggerTypeJob, false},

		// User-driven triggers — must STILL block anonymous callers on
		// private functions. The fix narrows the gate; it does NOT
		// remove it.
		{"http blocks anonymous", TriggerTypeHTTP, true},
		{"websocket blocks anonymous", TriggerTypeWebSocket, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // pre-cancelled so executeWithRetry short-circuits

			req := &InvokeRequest{
				Namespace:    "anchat-test",
				FunctionName: "push-fanout",
				Input:        []byte(`{"trigger":"test"}`),
				TriggerType:  tc.trigger,
				CallerWallet: "", // anonymous — what cron/pubsub/etc. naturally have
			}
			resp, err := inv.Invoke(ctx, req)

			if tc.wantAuth {
				// User-driven path: must hit the auth wall.
				if !errors.Is(err, ErrUnauthorized) {
					t.Errorf("trigger=%s wallet='': err=%v, want ErrUnauthorized", tc.trigger, err)
				}
				if resp == nil || resp.Error != "unauthorized" {
					t.Errorf("trigger=%s: expected response.Error=\"unauthorized\", got %+v", tc.trigger, resp)
				}
			} else {
				// System trigger: must NOT hit auth. Any other error is
				// fine (we forced a cancelled ctx so we expect ctx.Err()
				// or a wrapped version of it). The key invariant is
				// "ErrUnauthorized must not appear".
				if errors.Is(err, ErrUnauthorized) {
					t.Errorf("trigger=%s: system trigger blocked at auth (regression of bugboard #264): %+v", tc.trigger, resp)
				}
			}
		})
	}
}

// TestInvoke_systemTriggerStillAllowsPublic is a sanity check: public
// functions invoked by a system trigger should work exactly the same as
// before (the auth gate was a no-op for them anyway). The bypass must
// not change semantics for public functions.
func TestInvoke_systemTriggerStillAllowsPublic(t *testing.T) {
	publicFn := &Function{
		ID:        "fn-id",
		Namespace: "anchat-test",
		Name:      "ping",
		IsPublic:  true,
	}
	inv := &Invoker{
		registry: &invokeMockRegistry{fn: publicFn},
		logger:   zap.NewNop(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := &InvokeRequest{
		Namespace:    "anchat-test",
		FunctionName: "ping",
		Input:        []byte(`{}`),
		TriggerType:  TriggerTypeCron,
		CallerWallet: "",
	}
	_, err := inv.Invoke(ctx, req)
	if errors.Is(err, ErrUnauthorized) {
		t.Errorf("public function + system trigger should never be unauthorized: %v", err)
	}
}

// TestInvoke_internalFunctionGate is the bugboard #152 integration guard on
// the full Invoke gate: an `internal: true` function may be invoked by an
// admin caller or a system trigger, but a normal (non-admin) app-runtime key
// invoking it by name over HTTP is rejected `unauthorized` — even though it
// carries a valid identity that would satisfy a plain private function.
func TestInvoke_internalFunctionGate(t *testing.T) {
	internalFn := &Function{
		ID:         "fn-id",
		Namespace:  "anchat-test",
		Name:       "migrate",
		IsPublic:   false,
		IsInternal: true,
	}
	inv := &Invoker{
		registry: &invokeMockRegistry{fn: internalFn},
		logger:   zap.NewNop(),
		// engine nil — cancelled-ctx short-circuit prevents reaching it.
	}

	cases := []struct {
		name          string
		trigger       TriggerType
		callerWallet  string
		callerIsAdmin bool
		wantAuth      bool // true → must hit ErrUnauthorized
	}{
		// Non-admin app-runtime key with a real identity: the exact hole
		// #152 closes. A private function would allow this; an internal one
		// must not.
		{"http non-admin identified caller denied", TriggerTypeHTTP, "0xAppRuntime", false, true},
		// Admin caller: allowed to reach execution (no auth error).
		{"http admin caller allowed", TriggerTypeHTTP, "0xAdmin", true, false},
		// System trigger (cron): bypasses canInvokeFn entirely, so an
		// internal function still fires — this is how internal functions are
		// meant to be driven.
		{"cron system trigger bypasses gate", TriggerTypeCron, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // pre-cancelled so executeWithRetry short-circuits post-auth

			req := &InvokeRequest{
				Namespace:     "anchat-test",
				FunctionName:  "migrate",
				Input:         []byte(`{}`),
				TriggerType:   tc.trigger,
				CallerWallet:  tc.callerWallet,
				CallerIsAdmin: tc.callerIsAdmin,
			}
			resp, err := inv.Invoke(ctx, req)

			if tc.wantAuth {
				if !errors.Is(err, ErrUnauthorized) {
					t.Errorf("err=%v, want ErrUnauthorized", err)
				}
				if resp == nil || resp.Error != "unauthorized" {
					t.Errorf("expected response.Error=\"unauthorized\", got %+v", resp)
				}
			} else if errors.Is(err, ErrUnauthorized) {
				t.Errorf("internal function wrongly blocked at auth: %+v", resp)
			}
		})
	}
}

// TestInvoke_userTriggerWithCallerStillWorks verifies the fix doesn't
// regress the happy path for user-driven triggers: an HTTP request with a
// real CallerWallet on a private function still succeeds at the auth gate.
func TestInvoke_userTriggerWithCallerStillWorks(t *testing.T) {
	privateFn := &Function{
		ID:        "fn-id",
		Namespace: "anchat-test",
		Name:      "user-create",
		IsPublic:  false,
		CreatedBy: "0xdeployer",
	}
	inv := &Invoker{
		registry: &invokeMockRegistry{fn: privateFn},
		logger:   zap.NewNop(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := &InvokeRequest{
		Namespace:    "anchat-test",
		FunctionName: "user-create",
		Input:        []byte(`{}`),
		TriggerType:  TriggerTypeHTTP,
		CallerWallet: "0xRealUser",
	}
	_, err := inv.Invoke(ctx, req)
	if errors.Is(err, ErrUnauthorized) {
		t.Errorf("authenticated HTTP caller on private function must pass auth: %v", err)
	}
}
