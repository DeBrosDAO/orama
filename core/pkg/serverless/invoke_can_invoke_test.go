package serverless

import "testing"

// CanInvokeFunction replaces an exported Invoker.CanInvoke that re-read the
// function from the registry — a leader-routed round trip — and then passed the
// invoke grant as a hardcoded `true`, so it answered "yes" for a caller holding
// no invoke grant at all. It had no callers outside its own tests.
//
// These cases are the decision itself, which is what the persistent-WebSocket
// upgrade and the invoker both make.

func TestCanInvokeFunction_publicIsOpenToAnyone(t *testing.T) {
	fn := &Function{Namespace: "anchat-test", Name: "username-check", IsPublic: true}
	for _, wallet := range []string{"", "   ", "0xSomeone"} {
		if !CanInvokeFunction(fn, wallet, false, false) {
			t.Errorf("a public function refused wallet %q", wallet)
		}
	}
}

func TestCanInvokeFunction_privateRefusesAnonymous(t *testing.T) {
	fn := &Function{Namespace: "anchat-test", Name: "user-create", IsPublic: false}
	for _, wallet := range []string{"", "   ", "\t"} {
		if CanInvokeFunction(fn, wallet, false, true) {
			t.Errorf("a private function accepted wallet %q", wallet)
		}
	}
}

// The case the hardcoded grant hid: an identified caller who does not hold the
// invoke grant (bugboard #259).
func TestCanInvokeFunction_privateNeedsTheInvokeGrant(t *testing.T) {
	fn := &Function{Namespace: "anchat-test", Name: "user-create", IsPublic: false}

	if CanInvokeFunction(fn, "0xIdentified", false, false) {
		t.Error("a caller with an identity but no invoke grant reached a private function")
	}
	if !CanInvokeFunction(fn, "0xIdentified", false, true) {
		t.Error("a caller holding the invoke grant was refused a private function")
	}
}

func TestCanInvokeFunction_adminReachesPrivate(t *testing.T) {
	fn := &Function{Namespace: "anchat-test", Name: "user-create", IsPublic: false}
	if !CanInvokeFunction(fn, "", true, false) {
		t.Error("an admin was refused a private function")
	}
}

// bugboard #152: internal means admin or the gateway, and nothing else — not
// even an identified caller holding the invoke grant, and not a function marked
// public as well.
func TestCanInvokeFunction_internalIsAdminOnly(t *testing.T) {
	internal := &Function{Namespace: "anchat-test", Name: "migrate", IsInternal: true}
	if CanInvokeFunction(internal, "0xIdentified", false, true) {
		t.Error("an identified non-admin caller reached an internal function")
	}
	if !CanInvokeFunction(internal, "0xAdmin", true, false) {
		t.Error("an admin was refused an internal function")
	}

	internalAndPublic := &Function{Namespace: "anchat-test", Name: "migrate", IsInternal: true, IsPublic: true}
	if CanInvokeFunction(internalAndPublic, "", false, false) {
		t.Error("marking an internal function public made it reachable by anyone")
	}
}
