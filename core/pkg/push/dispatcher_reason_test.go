package push

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

// Bugboard #274. On anchat-v2 every Android push failed with
// `http=0 reason="" unreg=false` — the ntfy provider had no base URL, so it
// returned a plain error before any HTTP exchange. DeviceSendResult.Reason was
// only ever populated from a structured *PushError, so a pre-HTTP failure
// reached the caller with nothing to log or act on. These tests pin the
// guarantee that a failed send always carries a non-empty Reason.

// TestSendToUserDetailed_reasonSetForPreHTTPProviderError is the exact
// reproduction: a provider that fails before making any HTTP request.
func TestSendToUserDetailed_reasonSetForPreHTTPProviderError(t *testing.T) {
	store := &fakeStore{devices: []PushDevice{
		{Namespace: "ns", UserID: "u", DeviceID: "android-1", Provider: "ntfy", Token: "tok"},
	}}
	d := New(store, zap.NewNop())
	d.Register(&fakeProvider{name: "ntfy", err: errors.New("ntfy: base URL not configured")})

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u", PushMessage{Title: "x"})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("results len = %d; want 1", len(res.Results))
	}
	r := res.Results[0]
	if r.Success {
		t.Fatal("expected the send to fail")
	}
	if r.HTTPStatus != 0 {
		t.Errorf("HTTPStatus = %d; want 0 (no HTTP exchange happened)", r.HTTPStatus)
	}
	if r.Reason == "" {
		t.Error("Reason is empty — this is the #274 bug: a failure with http=0 and no reason gives the caller nothing to act on")
	}
	if r.Reason != "ntfy: base URL not configured" {
		t.Errorf("Reason = %q; want the provider error text", r.Reason)
	}
}

// TestSendToUserDetailed_reasonSetForUnknownProvider covers the other path that
// never reaches HTTP: a device row whose provider is not registered.
func TestSendToUserDetailed_reasonSetForUnknownProvider(t *testing.T) {
	store := &fakeStore{devices: []PushDevice{
		{Namespace: "ns", UserID: "u", DeviceID: "old-android", Provider: "ghost", Token: "tok"},
	}}
	d := New(store, zap.NewNop())

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u", PushMessage{Title: "x"})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}
	r := res.Results[0]
	if r.Reason != "UnknownProvider" {
		t.Errorf("Reason = %q; want %q", r.Reason, "UnknownProvider")
	}
	if r.Message == "" {
		t.Error("Message should still carry the human-readable detail")
	}
	// The sentinel chain must survive — legacy SendToUser callers rely on it.
	if !errors.Is(r.Err(), ErrUnknownProvider) {
		t.Error("ErrUnknownProvider chain lost")
	}
}

// TestSendToUserDetailed_providerReasonNotOverwritten guards the regression risk
// of the fix: when the remote DID give a machine-readable reason, that reason
// must survive untouched rather than being replaced by the error text.
func TestSendToUserDetailed_providerReasonNotOverwritten(t *testing.T) {
	store := &fakeStore{devices: []PushDevice{
		{Namespace: "ns", UserID: "u", DeviceID: "ios-1", Provider: "apns", Token: "tok"},
	}}
	d := New(store, zap.NewNop())
	d.Register(&fakeProvider{name: "apns", err: &PushError{
		HTTPStatus:   410,
		Reason:       "Unregistered",
		Message:      "device token is no longer active",
		Unregistered: true,
	}})

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u", PushMessage{Title: "x"})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}
	r := res.Results[0]
	if r.Reason != "Unregistered" {
		t.Errorf("Reason = %q; want the provider's own reason preserved", r.Reason)
	}
	if r.HTTPStatus != 410 || !r.Unregistered {
		t.Errorf("structured fields lost: %+v", r)
	}
}

// TestSendToUserDetailed_pushErrorWithoutReasonStillGetsOne covers a PushError
// raised before the HTTP exchange: HTTPStatus 0 and an empty Reason, which the
// PushError contract explicitly allows. The dispatcher must still fill it in.
func TestSendToUserDetailed_pushErrorWithoutReasonStillGetsOne(t *testing.T) {
	store := &fakeStore{devices: []PushDevice{
		{Namespace: "ns", UserID: "u", DeviceID: "ios-1", Provider: "apns", Token: "tok"},
	}}
	d := New(store, zap.NewNop())
	d.Register(&fakeProvider{name: "apns", err: &PushError{
		Message: "dial tcp: connection refused",
	}})

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u", PushMessage{Title: "x"})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}
	if r := res.Results[0]; r.Reason == "" {
		t.Error("Reason is empty for a pre-HTTP PushError — caller sees http=0 reason=\"\" again")
	}
}

// TestSendToUserDetailed_successLeavesReasonEmpty pins the negative case: Reason
// is a failure field, so a successful send must not start carrying one.
func TestSendToUserDetailed_successLeavesReasonEmpty(t *testing.T) {
	store := &fakeStore{devices: []PushDevice{
		{Namespace: "ns", UserID: "u", DeviceID: "android-1", Provider: "ntfy", Token: "tok"},
	}}
	d := New(store, zap.NewNop())
	d.Register(&fakeProvider{name: "ntfy"})

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u", PushMessage{Title: "x"})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}
	r := res.Results[0]
	if !r.Success {
		t.Fatal("expected success")
	}
	if r.Reason != "" {
		t.Errorf("Reason = %q on success; want empty", r.Reason)
	}
}

// TestSendToUserDetailed_noDevicesReportsNothing is the empty-input edge case:
// no devices means no results and no failure.
func TestSendToUserDetailed_noDevicesReportsNothing(t *testing.T) {
	d := New(&fakeStore{}, zap.NewNop())

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u", PushMessage{Title: "x"})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}
	if !res.Ok || res.DevicesAttempted != 0 || len(res.Results) != 0 {
		t.Errorf("unexpected result for a user with no devices: %+v", res)
	}
}
