package push

import (
	"context"
	"sync"
	"testing"

	"go.uber.org/zap"
)

// Bugboard #408 — target_provider dispatcher filter.
//
// Pin the four behaviors that matter for the AnChat CallKit-on-text
// bug class:
//
//  1. With TargetProvider="apns" set, ONLY apns devices are attempted.
//     VoIP-registered devices on the same iPhone are silently skipped
//     so a chat message doesn't trigger CallKit.
//
//  2. With TargetProvider="apns_voip", ONLY VoIP devices are attempted —
//     the alert device is skipped so an incoming-call signal doesn't
//     produce a silent alert.
//
//  3. With TargetProvider unset (legacy callers, unmigrated functions),
//     fan-out behavior is UNCHANGED — all devices attempted. This is
//     the back-compat guarantee that lets us ship the filter without
//     breaking every existing call site in every namespace.
//
//  4. DevicesAttempted in the SendDetailedResult reflects the
//     POST-FILTER count, not the raw device-store count. WASM callers
//     interpreting `attempted=0` as "no devices" need this to be the
//     real attempted count, not "user has zero devices anywhere".

// targetFilterDeviceStore returns a fixed device list and records what was
// asked for. PushDeviceStore-conformant for use as Dispatcher dep.
type targetFilterDeviceStore struct {
	devices []PushDevice
}

func (f *targetFilterDeviceStore) Upsert(ctx context.Context, dev PushDevice) error { return nil }
func (f *targetFilterDeviceStore) Delete(ctx context.Context, ns, id string) error  { return nil }
func (f *targetFilterDeviceStore) ListForUser(ctx context.Context, ns, userID string) ([]PushDevice, error) {
	return f.devices, nil
}

// recordingProvider implements PushProvider and just records which
// device tokens it was asked to send to. Lets the test assert exactly
// which devices reached which provider.
type recordingProvider struct {
	name string
	mu   sync.Mutex
	sent []string // device tokens received
}

func (r *recordingProvider) Name() string { return r.name }
func (r *recordingProvider) Send(ctx context.Context, msg PushMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, msg.DeviceToken)
	return nil
}
func (r *recordingProvider) tokens() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.sent))
	copy(out, r.sent)
	return out
}

// twoIPhoneDevicesUser returns the canonical AnChat scenario: one user
// with one iPhone registered TWICE — alert + voip — per the documented
// registration model.
func twoIPhoneDevicesUser() []PushDevice {
	return []PushDevice{
		{
			DeviceID: "ios-base",
			Provider: "apns",
			Token:    "ALERT-TOKEN",
		},
		{
			DeviceID: "ios-base:voip",
			Provider: "apns_voip",
			Token:    "VOIP-TOKEN",
		},
	}
}

func newTestDispatcher(t *testing.T, devs []PushDevice, providers ...PushProvider) *PushDispatcher {
	t.Helper()
	d := New(&targetFilterDeviceStore{devices: devs}, zap.NewNop())
	for _, p := range providers {
		d.Register(p)
	}
	return d
}

func TestDispatcher_TargetProvider_FiltersToApns(t *testing.T) {
	alert := &recordingProvider{name: "apns"}
	voip := &recordingProvider{name: "apns_voip"}
	d := newTestDispatcher(t, twoIPhoneDevicesUser(), alert, voip)

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u1", PushMessage{
		Title:          "new message",
		Body:           "hi",
		TargetProvider: "apns",
	})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}

	// Alert got the message; VoIP did NOT — this is the CallKit-on-text
	// bug guard. If voip.tokens() is non-empty here, message-push-handler
	// would ring CallKit on every chat message AnChat users receive.
	if got := alert.tokens(); len(got) != 1 || got[0] != "ALERT-TOKEN" {
		t.Errorf("alert provider tokens = %v; want [ALERT-TOKEN]", got)
	}
	if got := voip.tokens(); len(got) != 0 {
		t.Errorf("voip provider should NOT have been called (CallKit-on-text bug); got tokens=%v", got)
	}

	// DevicesAttempted reflects POST-filter count, not raw device count.
	// WASM callers parse this to decide whether to retry / log "no
	// devices" — must be the real attempt count.
	if res.DevicesAttempted != 1 {
		t.Errorf("DevicesAttempted = %d; want 1 (post-filter)", res.DevicesAttempted)
	}
	if res.DevicesSucceeded != 1 {
		t.Errorf("DevicesSucceeded = %d; want 1", res.DevicesSucceeded)
	}
	if len(res.Results) != 1 {
		t.Errorf("Results len = %d; want 1", len(res.Results))
	}
}

func TestDispatcher_TargetProvider_FiltersToApnsVoip(t *testing.T) {
	alert := &recordingProvider{name: "apns"}
	voip := &recordingProvider{name: "apns_voip"}
	d := newTestDispatcher(t, twoIPhoneDevicesUser(), alert, voip)

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u1", PushMessage{
		Data:           map[string]interface{}{"call_id": "c-1"},
		TargetProvider: "apns_voip",
	})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}

	if got := voip.tokens(); len(got) != 1 || got[0] != "VOIP-TOKEN" {
		t.Errorf("voip provider tokens = %v; want [VOIP-TOKEN]", got)
	}
	if got := alert.tokens(); len(got) != 0 {
		t.Errorf("alert provider should NOT have been called (call-push targets voip only); got tokens=%v", got)
	}
	if res.DevicesAttempted != 1 {
		t.Errorf("DevicesAttempted = %d; want 1", res.DevicesAttempted)
	}
}

func TestDispatcher_TargetProvider_UnsetFansOut(t *testing.T) {
	// Back-compat guarantee. Every existing function in every namespace
	// that doesn't set target_provider must continue to see fan-out.
	// If this regresses, every unmigrated push call site breaks.
	alert := &recordingProvider{name: "apns"}
	voip := &recordingProvider{name: "apns_voip"}
	d := newTestDispatcher(t, twoIPhoneDevicesUser(), alert, voip)

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u1", PushMessage{
		Title: "x",
		// TargetProvider intentionally unset.
	})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}

	if got := alert.tokens(); len(got) != 1 {
		t.Errorf("fan-out: alert tokens = %v; want 1", got)
	}
	if got := voip.tokens(); len(got) != 1 {
		t.Errorf("fan-out: voip tokens = %v; want 1", got)
	}
	if res.DevicesAttempted != 2 {
		t.Errorf("DevicesAttempted = %d; want 2 (fan-out)", res.DevicesAttempted)
	}
}

func TestDispatcher_TargetProvider_NoMatchingDevices_NoOp(t *testing.T) {
	// User has only an alert device; call-push-handler asks for
	// target_provider="apns_voip". Expected: no error, zero attempts,
	// Ok=true (a user with no matching device is not an error — same
	// semantics as "user has zero devices anywhere").
	alert := &recordingProvider{name: "apns"}
	voip := &recordingProvider{name: "apns_voip"}
	d := newTestDispatcher(t, []PushDevice{
		{DeviceID: "ios-only", Provider: "apns", Token: "T"},
	}, alert, voip)

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u1", PushMessage{
		TargetProvider: "apns_voip",
	})
	if err != nil {
		t.Fatalf("expected no error for no-matching-devices; got %v", err)
	}
	if !res.Ok {
		t.Errorf("Ok = false; want true (no matching devices is not a failure)")
	}
	if res.DevicesAttempted != 0 {
		t.Errorf("DevicesAttempted = %d; want 0", res.DevicesAttempted)
	}
	if len(alert.tokens()) != 0 || len(voip.tokens()) != 0 {
		t.Error("no provider should have been called")
	}
}

func TestDispatcher_TargetProvider_LegacySendToUser_AlsoFilters(t *testing.T) {
	// SendToUser delegates to SendToUserDetailed under the hood, so the
	// filter should apply identically. Pin this so a future refactor
	// can't split the two paths.
	alert := &recordingProvider{name: "apns"}
	voip := &recordingProvider{name: "apns_voip"}
	d := newTestDispatcher(t, twoIPhoneDevicesUser(), alert, voip)

	err := d.SendToUser(context.Background(), "ns", "u1", PushMessage{
		Title:          "x",
		Body:           "y",
		TargetProvider: "apns",
	})
	if err != nil {
		t.Fatalf("SendToUser: %v", err)
	}
	if len(alert.tokens()) != 1 {
		t.Errorf("alert should have been called; got %v", alert.tokens())
	}
	if len(voip.tokens()) != 0 {
		t.Errorf("voip should NOT have been called via SendToUser+target_provider; got %v", voip.tokens())
	}
}
