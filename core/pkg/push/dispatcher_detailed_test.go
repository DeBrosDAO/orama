package push

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.uber.org/zap"
)

// TestSendToUserDetailed_happyPath verifies the per-device result shape
// for the success case: ok=true, attempted=N, succeeded=N, every entry
// has Success=true.
func TestSendToUserDetailed_happyPath(t *testing.T) {
	store := &fakeStore{devices: []PushDevice{
		{Namespace: "ns", UserID: "u", DeviceID: "ios-A", Provider: "ntfy", Token: "tok-1"},
		{Namespace: "ns", UserID: "u", DeviceID: "ios-B", Provider: "ntfy", Token: "tok-2"},
	}}
	ntfy := &fakeProvider{name: "ntfy"}

	d := New(store, zap.NewNop())
	d.Register(ntfy)

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u", PushMessage{Title: "hi"})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}
	if !res.Ok {
		t.Error("expected Ok=true on all-success")
	}
	if res.DevicesAttempted != 2 || res.DevicesSucceeded != 2 {
		t.Errorf("attempted=%d succeeded=%d; want 2/2", res.DevicesAttempted, res.DevicesSucceeded)
	}
	if len(res.Results) != 2 {
		t.Fatalf("results len = %d; want 2", len(res.Results))
	}
	for i, r := range res.Results {
		if !r.Success {
			t.Errorf("result[%d] should be success, got %+v", i, r)
		}
		if r.Provider != "ntfy" {
			t.Errorf("result[%d].Provider = %q; want ntfy", i, r.Provider)
		}
	}
}

// TestSendToUserDetailed_unknownProvider verifies the "ghost provider"
// case populates Message + preserves the ErrUnknownProvider chain on
// the unexported err field (so the legacy SendToUser still sees the
// sentinel via errors.Is).
func TestSendToUserDetailed_unknownProvider(t *testing.T) {
	store := &fakeStore{devices: []PushDevice{
		{Namespace: "ns", UserID: "u", DeviceID: "old-android", Provider: "ghost", Token: "tok"},
	}}
	d := New(store, zap.NewNop())

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u", PushMessage{Title: "x"})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}
	if res.Ok {
		t.Error("Ok should be false when any device failed")
	}
	if res.DevicesAttempted != 1 || res.DevicesSucceeded != 0 {
		t.Errorf("attempted=%d succeeded=%d; want 1/0", res.DevicesAttempted, res.DevicesSucceeded)
	}
	r := res.Results[0]
	if r.Success {
		t.Error("unknown provider should not be Success")
	}
	if r.Message == "" {
		t.Error("Message should describe the unknown provider")
	}
	// The unexported err field carries the sentinel for errors.Is.
	if !errors.Is(r.Err(), ErrUnknownProvider) {
		t.Errorf("expected r.Err() to wrap ErrUnknownProvider, got %v", r.Err())
	}
}

// TestSendToUserDetailed_structuredPushError verifies that when a
// provider returns a *PushError (APNs 410/400/etc.), the detailed
// result faithfully reflects HTTPStatus, Reason, and Unregistered.
func TestSendToUserDetailed_structuredPushError(t *testing.T) {
	store := &fakeStore{devices: []PushDevice{
		{Namespace: "ns", UserID: "u", DeviceID: "ios-dead", Provider: "apns", Token: "tok"},
	}}
	apnsErr := &PushError{
		HTTPStatus:   410,
		Reason:       "Unregistered",
		Message:      "apns: 410 Unregistered",
		Unregistered: true,
	}
	apns := &fakeProvider{name: "apns", err: apnsErr}

	d := New(store, zap.NewNop())
	d.Register(apns)

	res, err := d.SendToUserDetailed(context.Background(), "ns", "u", PushMessage{Title: "x"})
	if err != nil {
		t.Fatalf("SendToUserDetailed: %v", err)
	}
	if res.Ok {
		t.Error("Ok should be false")
	}
	r := res.Results[0]
	if r.HTTPStatus != 410 {
		t.Errorf("HTTPStatus = %d; want 410", r.HTTPStatus)
	}
	if r.Reason != "Unregistered" {
		t.Errorf("Reason = %q; want Unregistered", r.Reason)
	}
	if !r.Unregistered {
		t.Error("Unregistered flag should be true for 410")
	}
}

// TestSendToUserDetailed_jsonShapeForWASM verifies the JSON encoding
// of SendDetailedResult matches what the WASM `oh.PushSendV2` host fn
// will produce. The unexported err field MUST be excluded from JSON
// (it's an in-process plumbing detail, not a wire field).
func TestSendToUserDetailed_jsonShapeForWASM(t *testing.T) {
	res := &SendDetailedResult{
		Ok:               false,
		DevicesAttempted: 2,
		DevicesSucceeded: 1,
		Results: []DeviceSendResult{
			{DeviceID: "good", Provider: "apns", Success: true},
			{
				DeviceID:     "bad",
				Provider:     "apns",
				Success:      false,
				HTTPStatus:   410,
				Reason:       "Unregistered",
				Message:      "apns: 410 Unregistered",
				Unregistered: true,
				err:          errors.New("must-not-leak"),
			},
		},
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	// Required fields present:
	for _, want := range []string{
		`"ok":false`,
		`"devices_attempted":2`,
		`"devices_succeeded":1`,
		`"device_id":"good"`,
		`"success":true`,
		`"device_id":"bad"`,
		`"http_status":410`,
		`"reason":"Unregistered"`,
		`"unregistered":true`,
	} {
		if !contains(s, want) {
			t.Errorf("expected JSON to contain %q; got: %s", want, s)
		}
	}
	// The unexported err must NOT leak into JSON.
	if contains(s, "must-not-leak") {
		t.Errorf("unexported err field leaked into JSON: %s", s)
	}
}

// TestSendToUser_legacyContract_preservedAcrossDetailedRefactor verifies
// that SendToUser (now layered on SendToUserDetailed) still returns the
// FIRST per-device error with its sentinel chain intact. Regression
// guard against accidentally losing the errors.Is contract for the
// pre-#348 callers.
func TestSendToUser_legacyContract_preservedAcrossDetailedRefactor(t *testing.T) {
	store := &fakeStore{devices: []PushDevice{
		{Namespace: "ns", UserID: "u", DeviceID: "phone", Provider: "ghost", Token: "tok"},
	}}
	d := New(store, zap.NewNop())

	err := d.SendToUser(context.Background(), "ns", "u", PushMessage{Title: "x"})
	if err == nil {
		t.Fatal("expected SendToUser to surface the unknown-provider error")
	}
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("SendToUser err = %v; want errors.Is(..., ErrUnknownProvider)", err)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
