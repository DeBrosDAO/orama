package push

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// Bugboard feat-10 — exclude_provider dispatcher filter.
//
// Inverse of #408's target_provider. Pin behaviors that matter for the
// "fan out to everyone EXCEPT VoIP" pattern:
//
//  1. With ExcludeProvider="apns_voip", apns/ntfy/expo devices are
//     attempted; apns_voip devices are dropped. Cleaner than listing
//     every included provider on every call.
//
//  2. With both TargetProvider and ExcludeProvider set, TargetProvider
//     wins (positive filter is strictly narrower; combining them is
//     ambiguous — e.g. target=apns + exclude=apns is empty). Documented
//     and pinned so a future refactor can't accidentally let exclude
//     subtract from target.
//
//  3. With neither set, fan-out unchanged (back-compat for every
//     existing caller).
//
//  4. DevicesAttempted reflects the POST-filter count.

func threeDeviceUser() []PushDevice {
	return []PushDevice{
		{DeviceID: "ios-base", Provider: "apns", Token: "ALERT-TOKEN"},
		{DeviceID: "ios-base:voip", Provider: "apns_voip", Token: "VOIP-TOKEN"},
		{DeviceID: "expo-1", Provider: "expo", Token: "EXPO-TOKEN"},
	}
}

func TestDispatcher_ExcludeProvider_DropsApnsVoip(t *testing.T) {
	alert := &recordingProvider{name: "apns"}
	voip := &recordingProvider{name: "apns_voip"}
	expo := &recordingProvider{name: "expo"}
	d := New(&targetFilterDeviceStore{devices: threeDeviceUser()}, zap.NewNop())
	for _, p := range []PushProvider{alert, voip, expo} {
		d.Register(p)
	}

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u1", PushMessage{
		Title:           "new message",
		Body:            "hi",
		ExcludeProvider: "apns_voip",
	})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}

	if got := alert.tokens(); len(got) != 1 {
		t.Errorf("alert should have been called once; got %v", got)
	}
	if got := expo.tokens(); len(got) != 1 {
		t.Errorf("expo should have been called once; got %v", got)
	}
	if got := voip.tokens(); len(got) != 0 {
		t.Errorf("FEAT-10 REGRESSION: voip was attempted despite ExcludeProvider=apns_voip; "+
			"this would CallKit-ring on every chat message even when caller meant to skip it. got=%v", got)
	}
	if res.DevicesAttempted != 2 {
		t.Errorf("DevicesAttempted = %d; want 2 (post-exclude: apns + expo)", res.DevicesAttempted)
	}
}

func TestDispatcher_ExcludeProvider_TargetProviderWinsWhenBothSet(t *testing.T) {
	// Ambiguity guard: if both are set, the documented behavior is
	// "TargetProvider wins; ExcludeProvider is ignored." Without this
	// pin, a future refactor could chain the filters (e.g.
	// target=apns + exclude=apns → 0 devices, surprise no-op) — which
	// would silently break any caller that set both, even harmlessly.
	alert := &recordingProvider{name: "apns"}
	voip := &recordingProvider{name: "apns_voip"}
	d := New(&targetFilterDeviceStore{devices: twoIPhoneDevicesUser()}, zap.NewNop())
	d.Register(alert)
	d.Register(voip)

	_, err := d.SendToUserDetailed(context.Background(), "ns", "u1", PushMessage{
		Title:           "x",
		TargetProvider:  "apns",      // positive: only apns
		ExcludeProvider: "apns_voip", // negative: also exclude voip — redundant when target is set
	})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}
	// Only the positive filter should have applied → alert called once.
	if got := alert.tokens(); len(got) != 1 {
		t.Errorf("alert attempts = %v; want 1 (TargetProvider should win when both set)", got)
	}
	if got := voip.tokens(); len(got) != 0 {
		t.Errorf("voip should not have been called (target filter excludes it implicitly); got %v", got)
	}
}

func TestDispatcher_ExcludeProvider_UnsetFansOut(t *testing.T) {
	// Back-compat: every existing caller that doesn't set either filter
	// must continue to see the full fan-out behavior.
	alert := &recordingProvider{name: "apns"}
	voip := &recordingProvider{name: "apns_voip"}
	expo := &recordingProvider{name: "expo"}
	d := New(&targetFilterDeviceStore{devices: threeDeviceUser()}, zap.NewNop())
	for _, p := range []PushProvider{alert, voip, expo} {
		d.Register(p)
	}

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u1", PushMessage{
		Title: "x",
		// Neither TargetProvider nor ExcludeProvider set.
	})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}
	if res.DevicesAttempted != 3 {
		t.Errorf("DevicesAttempted = %d; want 3 (fan-out)", res.DevicesAttempted)
	}
	if len(alert.tokens()) != 1 || len(voip.tokens()) != 1 || len(expo.tokens()) != 1 {
		t.Errorf("all three providers should have been attempted; got alert=%d voip=%d expo=%d",
			len(alert.tokens()), len(voip.tokens()), len(expo.tokens()))
	}
}

func TestDispatcher_ExcludeProvider_NoMatchingExclusion_NoOp(t *testing.T) {
	// If the exclude target doesn't match any registered device,
	// everyone is still attempted (back-compat fan-out).
	alert := &recordingProvider{name: "apns"}
	voip := &recordingProvider{name: "apns_voip"}
	d := New(&targetFilterDeviceStore{devices: twoIPhoneDevicesUser()}, zap.NewNop())
	d.Register(alert)
	d.Register(voip)

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u1", PushMessage{
		Title:           "x",
		ExcludeProvider: "ntfy", // user has no ntfy device — no-op exclusion
	})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}
	if res.DevicesAttempted != 2 {
		t.Errorf("DevicesAttempted = %d; want 2 (exclude matched nothing)", res.DevicesAttempted)
	}
}
