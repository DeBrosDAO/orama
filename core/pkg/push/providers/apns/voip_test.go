package apns

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/push"
	"github.com/sideshow/apns2"
)

// Bugboard #408 — KindVoIP / PushKit Provider variant.
//
// These tests pin the three places where the VoIP path MUST differ
// from the alert path:
//
//  1. apns-topic header gets the ".voip" suffix appended (Apple routes
//     this to the PushKit delivery system that wakes the app via
//     CallKit; without the suffix, Apple silently rejects the push or
//     ignores PushKit semantics).
//
//  2. apns-push-type header is "voip" (required since iOS 13; without
//     it Apple rejects at the edge with InvalidPushType).
//
//  3. hasVisibleContent guard is SKIPPED. VoIP pushes legally have no
//     alert content — iOS renders the CallKit UI from the `data` dict
//     alone (caller name, call ID, etc.). The bugboard #348 empty-
//     content guard would reject these — we bypass it ONLY on the
//     VoIP kind so the alert path keeps its silent-drop protection.
//
//  4. Priority is forced to HIGH regardless of msg.Priority — Apple
//     rejects VoIP pushes with priority 5 (`BadPriority`).
//
// Without these, the dispatcher path for `apns_voip`-registered
// devices either silently drops or returns errors at send time and
// CallKit never fires on the receiver — which defeats the whole
// purpose of registering a separate VoIP device row.

func TestVoIP_Name_ReturnsApnsVoipForRouting(t *testing.T) {
	// Dispatcher routes by device.Provider == provider.Name(). If the
	// VoIP Provider returns "apns" the dispatcher would conflate it
	// with the alert provider (or the second Register call would
	// overwrite the first in the providers map). MUST be "apns_voip".
	p := newTestProviderKind(t, "com.example.app", KindVoIP, &fakePushClient{})
	if got := p.Name(); got != "apns_voip" {
		t.Errorf("KindVoIP Name() = %q; want %q (dispatcher routes by this)", got, "apns_voip")
	}
	// Alert kind unchanged — back-compat.
	alert := newTestProviderKind(t, "com.example.app", KindAlert, &fakePushClient{})
	if got := alert.Name(); got != "apns" {
		t.Errorf("KindAlert Name() = %q; want %q (back-compat)", got, "apns")
	}
}

func TestVoIP_Send_TopicHasVoIPSuffix(t *testing.T) {
	fake := &fakePushClient{
		resp: &apns2.Response{StatusCode: http.StatusOK, ApnsID: "voip-1"},
	}
	p := newTestProviderKind(t, "com.example.app", KindVoIP, fake)
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "DEADBEEFVOIPTOKEN",
		Data: map[string]interface{}{
			"call_id":   "abc-123",
			"caller_id": "user-42",
		},
	})
	if err != nil {
		t.Fatalf("VoIP Send: %v", err)
	}
	if fake.lastSent == nil {
		t.Fatal("Send didn't dispatch to client")
	}
	const wantTopic = "com.example.app.voip"
	if fake.lastSent.Topic != wantTopic {
		t.Errorf("topic = %q; want %q (Apple routes the .voip suffix to PushKit)", fake.lastSent.Topic, wantTopic)
	}
}

func TestVoIP_Send_PushTypeIsVOIP(t *testing.T) {
	fake := &fakePushClient{
		resp: &apns2.Response{StatusCode: http.StatusOK, ApnsID: "voip-2"},
	}
	p := newTestProviderKind(t, "com.example.app", KindVoIP, fake)
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "VOIP-TOKEN",
		Data:        map[string]interface{}{"call_id": "x"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if fake.lastSent.PushType != apns2.PushTypeVOIP {
		t.Errorf("apns-push-type = %q; want %q (required since iOS 13)",
			fake.lastSent.PushType, apns2.PushTypeVOIP)
	}
}

func TestVoIP_Send_EmptyContentAccepted(t *testing.T) {
	// CallKit-only pushes carry no alert. The bugboard #348 visible-
	// content guard MUST be bypassed on the VoIP path or every
	// incoming-call signal would fail with ErrEmptyContent before
	// reaching Apple.
	fake := &fakePushClient{
		resp: &apns2.Response{StatusCode: http.StatusOK, ApnsID: "voip-3"},
	}
	p := newTestProviderKind(t, "com.example.app", KindVoIP, fake)
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "VOIP-TOKEN",
		// No Title, Body, Badge, Sound, or content_available marker —
		// this would be ErrEmptyContent on the alert path.
	})
	if err != nil {
		t.Fatalf("VoIP empty-content Send should succeed; got %v", err)
	}
	if fake.lastSent == nil {
		t.Fatal("Send didn't dispatch to client")
	}
}

func TestVoIP_Send_ForcesHighPriority(t *testing.T) {
	// Apple rejects VoIP pushes with `apns-priority: 5` (BadPriority).
	// Even if the caller passes Priority="" or PriorityNormal, the
	// VoIP path forces High so we never produce a request Apple will
	// reject for that reason.
	cases := []struct {
		name         string
		callerPrio   push.PushPriority
		wantApnsPrio int
	}{
		{"caller_unset", "", apns2.PriorityHigh},
		{"caller_normal", push.PriorityNormal, apns2.PriorityHigh},
		{"caller_high", push.PriorityHigh, apns2.PriorityHigh},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakePushClient{
				resp: &apns2.Response{StatusCode: http.StatusOK},
			}
			p := newTestProviderKind(t, "com.example.app", KindVoIP, fake)
			_ = p.Send(context.Background(), push.PushMessage{
				DeviceToken: "T",
				Priority:    tc.callerPrio,
				Data:        map[string]interface{}{"call_id": "x"},
			})
			if fake.lastSent.Priority != tc.wantApnsPrio {
				t.Errorf("apns-priority = %d; want %d (VoIP forces High)",
					fake.lastSent.Priority, tc.wantApnsPrio)
			}
		})
	}
}

func TestAlert_Send_TopicIsBundleIDWithoutSuffix(t *testing.T) {
	// Regression guard: VoIP suffix logic must NOT bleed into the alert
	// path. Pre-#408 the topic was always the bare bundle; this test
	// pins that behavior so a future refactor can't break the alert
	// route by accident.
	fake := &fakePushClient{
		resp: &apns2.Response{StatusCode: http.StatusOK},
	}
	p := newTestProviderKind(t, "com.example.app", KindAlert, fake)
	_ = p.Send(context.Background(), push.PushMessage{
		DeviceToken: "T",
		Title:       "hello",
	})
	if fake.lastSent.Topic != "com.example.app" {
		t.Errorf("alert topic = %q; want %q (bare bundle)",
			fake.lastSent.Topic, "com.example.app")
	}
	if fake.lastSent.PushType != apns2.PushTypeAlert {
		t.Errorf("alert push-type = %q; want %q", fake.lastSent.PushType, apns2.PushTypeAlert)
	}
}

func TestAlert_Send_EmptyContentStillRejected(t *testing.T) {
	// Bugboard #348 guard MUST remain intact on the alert path even
	// after the VoIP bypass landed. If this regresses, alert-path
	// silent-drop bugs come back.
	p := newTestProviderKind(t, "com.example.app", KindAlert, &fakePushClient{})
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "T",
		// No Title/Body/Badge/Sound/content_available — should reject
		// on the alert path even though the VoIP path accepts it.
	})
	if err == nil {
		t.Fatal("alert path should still reject empty-content (bugboard #348); got nil")
	}
}

// Bugboard #132: VoIP call-invites MUST carry a short apns-expiration so APNs
// never store-and-forwards a stale invite into a phantom missed-call ring
// minutes later. Without it apns2 omits the header → store-and-forward.
func TestVoIP_Send_ExpirationCappedToRingWindow(t *testing.T) {
	fake := &fakePushClient{resp: &apns2.Response{StatusCode: http.StatusOK, ApnsID: "voip-exp"}}
	p := newTestProviderKind(t, "com.example.app", KindVoIP, fake)
	before := time.Now()
	if err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "VOIP-TOKEN",
		Data:        map[string]interface{}{"call_id": "x"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	exp := fake.lastSent.Expiration
	if exp.IsZero() {
		t.Fatal("VoIP push has NO apns-expiration — APNs store-and-forwards → late phantom ring (#132)")
	}
	if !exp.After(before) {
		t.Errorf("expiration %v not in the future (before=%v)", exp, before)
	}
	if exp.After(before.Add(voipPushExpiry + 2*time.Second)) {
		t.Errorf("expiration %v exceeds the ring-window cap (%s) — would allow a late ring", exp, voipPushExpiry)
	}
}

// Alert (message) pushes intentionally keep store-and-forward (no expiration) so
// a notification still lands after reconnect — only the VoIP path is capped.
func TestAlert_Send_NoExpiration_keepsStoreAndForward(t *testing.T) {
	fake := &fakePushClient{resp: &apns2.Response{StatusCode: http.StatusOK, ApnsID: "alert-1"}}
	p := newTestProviderKind(t, "com.example.app", KindAlert, fake)
	if err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "ALERT-TOKEN", Title: "hi", Body: "msg",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !fake.lastSent.Expiration.IsZero() {
		t.Errorf("alert push set expiration %v; want none (store-and-forward)", fake.lastSent.Expiration)
	}
}
