package serverless

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// canInvokeMockRegistry is a minimal FunctionRegistry surface for the
// CanInvoke tests. Only Get is exercised; everything else panics so
// accidental drift is loud.
type canInvokeMockRegistry struct {
	FunctionRegistry // embedded interface — calling unimplemented methods panics

	fn  *Function
	err error
}

func (m *canInvokeMockRegistry) Get(_ context.Context, _, _ string, _ int) (*Function, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.fn, nil
}

func newInvokerForCanInvokeTest(fn *Function) *Invoker {
	return &Invoker{
		registry: &canInvokeMockRegistry{fn: fn},
		logger:   zap.NewNop(),
	}
}

// TestCanInvoke_publicAllowsAnyone — public functions bypass the
// authorization check entirely (auth middleware lets unauthenticated
// requests through to the handler too).
func TestCanInvoke_publicAllowsAnyone(t *testing.T) {
	inv := newInvokerForCanInvokeTest(&Function{
		Namespace: "anchat-test",
		Name:      "username-check",
		IsPublic:  true,
	})
	for _, wallet := range []string{"", "8oxu7UzzaSXc...", "anchat-test"} {
		ok, err := inv.CanInvoke(context.Background(), "anchat-test", "username-check", wallet)
		if err != nil {
			t.Fatalf("wallet=%q: %v", wallet, err)
		}
		if !ok {
			t.Errorf("wallet=%q: public function denied", wallet)
		}
	}
}

// TestCanInvoke_privateRejectsAnonymous — empty callerWallet means the
// auth middleware didn't establish any identity; private functions reject.
func TestCanInvoke_privateRejectsAnonymous(t *testing.T) {
	inv := newInvokerForCanInvokeTest(&Function{
		Namespace: "anchat-test",
		Name:      "user-create",
		IsPublic:  false,
	})
	ok, _ := inv.CanInvoke(context.Background(), "anchat-test", "user-create", "")
	if ok {
		t.Error("anonymous caller should be denied for private function")
	}
	// Whitespace-only is still anonymous.
	ok, _ = inv.CanInvoke(context.Background(), "anchat-test", "user-create", "   ")
	if ok {
		t.Error("whitespace-only callerWallet should be denied")
	}
}

// TestCanInvoke_privateAllowsJWTAuthenticatedNewWallet is the regression
// guard for bug #215 follow-up. A brand-new wallet (typical signup flow,
// `user-create` style) calls a private function in a namespace it doesn't
// "own" yet. Must succeed: auth middleware already validated the JWT
// belongs to this namespace, the only role of CanInvoke is to confirm
// SOMEONE is authenticated.
//
// Pre-fix this returned false (the wallet wasn't equal to the namespace
// string and wasn't the deployer), which was the entire reason AnChat saw
// 401 unauthorized after the cluster_secret_path fix unblocked JWT
// verification.
func TestCanInvoke_privateAllowsJWTAuthenticatedNewWallet(t *testing.T) {
	inv := newInvokerForCanInvokeTest(&Function{
		Namespace: "anchat-test",
		Name:      "user-create",
		IsPublic:  false,
		CreatedBy: "0xdeployer-wallet-not-this-caller",
	})
	const newUserWallet = "8oxu7UzzaSXcxZ9B3YuEqr3Qpmx7tgT9HzaA4NUGiand"
	ok, err := inv.CanInvoke(context.Background(), "anchat-test", "user-create", newUserWallet)
	if err != nil {
		t.Fatalf("CanInvoke: %v", err)
	}
	if !ok {
		t.Fatal("new user wallet should be allowed to invoke private function (auth middleware vouches for them)")
	}
}

// TestCanInvoke_privateAllowsAPIKeyCaller — API-key callers get
// callerWallet=namespace from the wallet resolver. They should still
// succeed; this preserves the pre-#215 working flow for tenants who
// only use API keys.
func TestCanInvoke_privateAllowsAPIKeyCaller(t *testing.T) {
	inv := newInvokerForCanInvokeTest(&Function{
		Namespace: "anchat-test",
		Name:      "user-create",
		IsPublic:  false,
		CreatedBy: "0xdeployer-wallet",
	})
	ok, err := inv.CanInvoke(context.Background(), "anchat-test", "user-create", "anchat-test")
	if err != nil {
		t.Fatalf("CanInvoke: %v", err)
	}
	if !ok {
		t.Error("API-key callers (callerWallet=namespace) must keep working")
	}
}

// TestCanInvoke_privateAllowsDeployer — the function deployer can invoke
// their own private function. Was true pre-fix and must remain true.
func TestCanInvoke_privateAllowsDeployer(t *testing.T) {
	const deployer = "0xdeployer-wallet"
	inv := newInvokerForCanInvokeTest(&Function{
		Namespace: "anchat-test",
		Name:      "user-create",
		IsPublic:  false,
		CreatedBy: deployer,
	})
	ok, err := inv.CanInvoke(context.Background(), "anchat-test", "user-create", deployer)
	if err != nil {
		t.Fatalf("CanInvoke: %v", err)
	}
	if !ok {
		t.Error("deployer must always be allowed to invoke their own function")
	}
}
