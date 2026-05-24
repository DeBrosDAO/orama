package apns

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/push"
	"github.com/sideshow/apns2"
	"go.uber.org/zap"
)

// fakePushClient implements pushClient for unit tests so we don't have
// to spin up a TLS endpoint mimicking api.push.apple.com.
//
// `block` (when non-nil) makes PushWithContext block until either the
// channel closes OR ctx is cancelled — used by ctx-cancellation tests.
type fakePushClient struct {
	resp     *apns2.Response
	err      error
	lastSent *apns2.Notification
	block    chan struct{} // optional — blocks Push until ctx done or channel closed
}

func (f *fakePushClient) PushWithContext(ctx apns2.Context, n *apns2.Notification) (*apns2.Response, error) {
	f.lastSent = n
	if f.block != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.block:
		}
	}
	return f.resp, f.err
}

// newTestProvider constructs an alert-kind Provider with a stub
// pushClient, bypassing real APNs. Existing call sites get the same
// behavior as pre-#408 — no need to thread a Kind through every test.
func newTestProvider(t *testing.T, bundle string, fake *fakePushClient) *Provider {
	t.Helper()
	return newTestProviderKind(t, bundle, KindAlert, fake)
}

// newTestProviderKind constructs a Provider of the given kind for
// VoIP-path coverage. Bugboard #408.
func newTestProviderKind(t *testing.T, bundle string, kind Kind, fake *fakePushClient) *Provider {
	t.Helper()
	return &Provider{
		bundleID: bundle,
		kind:     kind,
		client:   fake,
		logger:   zap.NewNop(),
	}
}

// validP8 is a real-looking PEM-encoded EC P-256 private key. Not the
// real one — generated for tests only. Used to validate the
// happy-path constructor; New() will still fail because authKey parsing
// will reject this synthetic key, so we don't use it for Send() tests.
const validP8 = `-----BEGIN PRIVATE KEY-----
MIGTAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBHkwdwIBAQQg2pV1mEzh4n1mY3y4
i7Ww8gJZ7lxFm6dlGn3PMOzCq2egCgYIKoZIzj0DAQehRANCAAS8Pn8VKWUe9wm8
e1JFvSTSj1RxLm2sj8cKpFnSdF5g3kfQ9ueJmFVnZbR3VRJOzn0FNyEJYUkXOdYx
PRIVATE_KEY_PLACEHOLDER==
-----END PRIVATE KEY-----`

// ---- Validator tests ------------------------------------------------

func TestValidator_AcceptsWellFormedConfig(t *testing.T) {
	v := NewValidator()
	raw := []byte(`{
		"team_id": "ABCDEFGHIJ",
		"key_id": "1234567890",
		"bundle_id": "com.example.app",
		"p8_key": "-----BEGIN PRIVATE KEY-----\nMIGTAg...\n-----END PRIVATE KEY-----",
		"environment": "production"
	}`)
	if err := v.Validate(raw); err != nil {
		t.Errorf("expected valid config to pass; got %v", err)
	}
}

func TestValidator_RejectsMissingFields(t *testing.T) {
	v := NewValidator()
	tests := []struct {
		name string
		body string
		want string
	}{
		{"no team_id", `{"key_id":"1234567890","bundle_id":"com.x","p8_key":"-----BEGIN PRIVATE KEY-----","environment":"sandbox"}`, "team_id required"},
		{"short team_id", `{"team_id":"ABC","key_id":"1234567890","bundle_id":"com.x.y","p8_key":"-----BEGIN PRIVATE KEY-----","environment":"sandbox"}`, "team_id must be 10"},
		{"no key_id", `{"team_id":"ABCDEFGHIJ","bundle_id":"com.x.y","p8_key":"-----BEGIN PRIVATE KEY-----","environment":"sandbox"}`, "key_id required"},
		{"no bundle_id", `{"team_id":"ABCDEFGHIJ","key_id":"1234567890","p8_key":"-----BEGIN PRIVATE KEY-----","environment":"sandbox"}`, "bundle_id required"},
		{"bundle_id no dot", `{"team_id":"ABCDEFGHIJ","key_id":"1234567890","bundle_id":"comx","p8_key":"-----BEGIN PRIVATE KEY-----","environment":"sandbox"}`, "reverse-DNS"},
		{"no p8_key", `{"team_id":"ABCDEFGHIJ","key_id":"1234567890","bundle_id":"com.x.y","environment":"sandbox"}`, "p8_key required"},
		{"p8_key not PEM", `{"team_id":"ABCDEFGHIJ","key_id":"1234567890","bundle_id":"com.x.y","p8_key":"not-pem","environment":"sandbox"}`, "PEM-encoded"},
		{"bad env", `{"team_id":"ABCDEFGHIJ","key_id":"1234567890","bundle_id":"com.x.y","p8_key":"-----BEGIN PRIVATE KEY-----","environment":"staging"}`, "sandbox"},
		{"no env", `{"team_id":"ABCDEFGHIJ","key_id":"1234567890","bundle_id":"com.x.y","p8_key":"-----BEGIN PRIVATE KEY-----"}`, "environment required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate([]byte(tt.body))
			if err == nil {
				t.Fatalf("expected error containing %q; got nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v; want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidator_RejectsMalformedJSON(t *testing.T) {
	v := NewValidator()
	if err := v.Validate([]byte(`{not json`)); err == nil {
		t.Error("expected JSON parse error")
	}
}

func TestValidator_RedactNeverEchoesP8Key(t *testing.T) {
	v := NewValidator()
	raw := []byte(`{
		"team_id": "ABCDEFGHIJ",
		"key_id": "1234567890",
		"bundle_id": "com.example.app",
		"p8_key": "-----BEGIN PRIVATE KEY-----\nSUPERSECRETKEY\n-----END PRIVATE KEY-----",
		"environment": "production"
	}`)
	out, err := v.Redact(raw)
	if err != nil {
		t.Fatalf("redact: %v", err)
	}
	enc, _ := json.Marshal(out)
	if strings.Contains(string(enc), "SUPERSECRETKEY") {
		t.Errorf("redacted output leaks p8 key material: %s", enc)
	}
	if strings.Contains(string(enc), "BEGIN PRIVATE KEY") {
		t.Errorf("redacted output includes PEM header: %s", enc)
	}
	// Should still surface non-secret fields for tenant confirmation.
	if !strings.Contains(string(enc), "ABCDEFGHIJ") {
		t.Errorf("redacted output should include team_id; got %s", enc)
	}
	if !strings.Contains(string(enc), `"has_p8_key":true`) {
		t.Errorf("redacted output should set has_p8_key=true; got %s", enc)
	}
}

// ---- buildAPSPayload tests ------------------------------------------

func TestBuildAPSPayload_basicAlert(t *testing.T) {
	msg := push.PushMessage{Title: "hi", Body: "from orama"}
	raw, err := buildAPSPayload(msg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var out struct {
		APS struct {
			Alert struct {
				Title, Body string
			}
		} `json:"aps"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.APS.Alert.Title != "hi" || out.APS.Alert.Body != "from orama" {
		t.Errorf("alert wrong: %+v", out.APS.Alert)
	}
}

func TestBuildAPSPayload_dataAlongsideAPS(t *testing.T) {
	msg := push.PushMessage{
		Title: "x",
		Body:  "y",
		Data:  map[string]interface{}{"thread": "abc", "deeplink": "anchat://room/42"},
	}
	raw, _ := buildAPSPayload(msg)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	if _, hasAPS := out["aps"]; !hasAPS {
		t.Error("payload missing aps")
	}
	if out["thread"] != "abc" {
		t.Errorf("data.thread missing; got %v", out)
	}
	if out["deeplink"] != "anchat://room/42" {
		t.Errorf("data.deeplink missing; got %v", out)
	}
}

func TestBuildAPSPayload_dataCannotClobberAPS(t *testing.T) {
	msg := push.PushMessage{
		Title: "x",
		Data:  map[string]interface{}{"aps": "evil"},
	}
	raw, _ := buildAPSPayload(msg)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	apsField, ok := out["aps"]
	if !ok {
		t.Fatal("aps missing")
	}
	if _, isMap := apsField.(map[string]interface{}); !isMap {
		t.Errorf("aps overwritten by tenant data: got %T (%v)", apsField, apsField)
	}
}

func TestBuildAPSPayload_badgeAndSound(t *testing.T) {
	msg := push.PushMessage{
		Title: "x", Badge: 3, Sound: "ding.caf",
	}
	raw, _ := buildAPSPayload(msg)
	if !strings.Contains(string(raw), `"badge":3`) {
		t.Errorf("badge not in payload: %s", raw)
	}
	if !strings.Contains(string(raw), `"sound":"ding.caf"`) {
		t.Errorf("sound not in payload: %s", raw)
	}
}

func TestBuildAPSPayload_channelMapsToThreadID(t *testing.T) {
	msg := push.PushMessage{Title: "x", Channel: "messages"}
	raw, _ := buildAPSPayload(msg)
	if !strings.Contains(string(raw), `"thread-id":"messages"`) {
		t.Errorf("channel not mapped to thread-id: %s", raw)
	}
}

// ---- Send dispatch tests --------------------------------------------

func TestSend_Success(t *testing.T) {
	fake := &fakePushClient{
		resp: &apns2.Response{StatusCode: http.StatusOK, ApnsID: "abc-123"},
	}
	p := newTestProvider(t, "com.example.app", fake)
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "ABCDEF1234",
		Title:       "hello",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if fake.lastSent == nil {
		t.Fatal("Send didn't dispatch to client")
	}
	if fake.lastSent.Topic != "com.example.app" {
		t.Errorf("topic = %q; want com.example.app", fake.lastSent.Topic)
	}
	if fake.lastSent.DeviceToken != "ABCDEF1234" {
		t.Errorf("token mismatch: %q", fake.lastSent.DeviceToken)
	}
}

func TestSend_EmptyTokenRejected(t *testing.T) {
	p := newTestProvider(t, "com.example.app", &fakePushClient{})
	err := p.Send(context.Background(), push.PushMessage{Title: "x"})
	if !errors.Is(err, push.ErrEmptyToken) {
		t.Errorf("expected ErrEmptyToken; got %v", err)
	}
}

func TestSend_Gone410ReturnsSentinel(t *testing.T) {
	fake := &fakePushClient{
		resp: &apns2.Response{StatusCode: http.StatusGone, Reason: "Unregistered", ApnsID: "x"},
	}
	p := newTestProvider(t, "com.example.app", fake)
	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Title: "x"})
	if !errors.Is(err, ErrDeviceUnregistered) {
		t.Errorf("expected ErrDeviceUnregistered for 410; got %v", err)
	}
	if !strings.Contains(err.Error(), "Unregistered") {
		t.Errorf("error should include APNs reason; got %v", err)
	}
}

func TestSend_OtherErrorStatusBubblesUp(t *testing.T) {
	fake := &fakePushClient{
		resp: &apns2.Response{StatusCode: http.StatusForbidden, Reason: "BadDeviceToken"},
	}
	p := newTestProvider(t, "com.example.app", fake)
	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Title: "x"})
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if errors.Is(err, ErrDeviceUnregistered) {
		t.Error("403 should not be classified as Unregistered")
	}
	if !strings.Contains(err.Error(), "BadDeviceToken") {
		t.Errorf("error should surface reason; got %v", err)
	}
}

func TestSend_NilResponseHandled(t *testing.T) {
	fake := &fakePushClient{} // both nil
	p := newTestProvider(t, "com.example.app", fake)
	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Title: "x"})
	if err == nil {
		t.Fatal("expected error on nil response")
	}
}

func TestSend_ContextCancellationPropagates(t *testing.T) {
	// Regression: previously Send launched a goroutine and selected on
	// ctx.Done — which made cancel "work" from the caller's point of
	// view, but the in-flight request kept running until the apns2
	// client's HTTPClient.Timeout fired (10s). PushWithContext fixes
	// this by routing ctx into the HTTP/2 stream.
	fake := &fakePushClient{
		resp:  &apns2.Response{StatusCode: 200},
		block: make(chan struct{}), // never closed → blocks forever absent ctx cancel
	}
	p := newTestProvider(t, "com.example.app", fake)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel almost immediately.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := p.Send(ctx, push.PushMessage{DeviceToken: "t", Title: "x"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected cancellation error; got nil")
	}
	// Must have returned via the ctx-cancel path, not the (non-existent)
	// fallback timeout. Should be well under 1 second.
	if elapsed > 1*time.Second {
		t.Errorf("Send took too long under cancellation (%v); ctx should kill the request promptly", elapsed)
	}
}

func TestSend_HighPrioritySetsAPNsHigh(t *testing.T) {
	fake := &fakePushClient{
		resp: &apns2.Response{StatusCode: http.StatusOK},
	}
	p := newTestProvider(t, "com.example.app", fake)
	_ = p.Send(context.Background(), push.PushMessage{
		DeviceToken: "t",
		Title:       "x",
		Priority:    push.PriorityHigh,
	})
	if fake.lastSent.Priority != apns2.PriorityHigh {
		t.Errorf("Priority = %d; want %d", fake.lastSent.Priority, apns2.PriorityHigh)
	}
}

// ---- ParseCredentials tests -----------------------------------------

func TestParseCredentials_RoundTrip(t *testing.T) {
	raw := []byte(`{
		"team_id":"ABCDEFGHIJ",
		"key_id":"1234567890",
		"bundle_id":"com.example.app",
		"p8_key":"-----BEGIN PRIVATE KEY-----\nzzz\n-----END PRIVATE KEY-----",
		"environment":"sandbox"
	}`)
	c, err := ParseCredentials(raw)
	if err != nil {
		t.Fatalf("ParseCredentials: %v", err)
	}
	if c.TeamID != "ABCDEFGHIJ" || c.KeyID != "1234567890" {
		t.Errorf("wrong: %+v", c)
	}
	if c.Environment != EnvSandbox {
		t.Errorf("env = %s; want sandbox", c.Environment)
	}
}

func TestParseCredentials_RejectsBadConfig(t *testing.T) {
	raw := []byte(`{"team_id":"too-short"}`)
	if _, err := ParseCredentials(raw); err == nil {
		t.Error("expected error on bad config")
	}
}

// ---- Bugboard #348 hardening: empty-content + structured PushError -------

// TestSend_EmptyContentRejected verifies the bugboard #348 root-cause
// guard: a message with no title, body, badge, sound, or
// content_available marker MUST fail upfront — not silently 200 from
// Apple and look like delivery success.
func TestSend_EmptyContentRejected(t *testing.T) {
	p := newTestProvider(t, "com.example.app", &fakePushClient{})
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "ABCDEF1234",
		// No Title, Body, Badge, Sound, or content_available in Data.
	})
	if !errors.Is(err, push.ErrEmptyContent) {
		t.Errorf("expected push.ErrEmptyContent for empty payload; got %v", err)
	}
}

// TestSend_ContentAvailableAccepted ensures background-only pushes
// (content_available without alert) ARE allowed — iOS uses this for
// silent data pushes that wake the app without UI. Bugboard #348:
// don't over-reject; only reject pushes that have NOTHING.
func TestSend_ContentAvailableAccepted(t *testing.T) {
	fake := &fakePushClient{
		resp: &apns2.Response{StatusCode: http.StatusOK, ApnsID: "ok-1"},
	}
	p := newTestProvider(t, "com.example.app", fake)
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "ABCDEF1234",
		Data:        map[string]interface{}{"content_available": true},
	})
	if err != nil {
		t.Fatalf("content-available push should be allowed: %v", err)
	}
	if fake.lastSent == nil {
		t.Fatal("Send didn't dispatch to client")
	}
	// Verify content-available landed in the aps dict.
	var payload map[string]interface{}
	if err := json.Unmarshal(fake.lastSent.Payload.([]byte), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	aps, _ := payload["aps"].(map[string]interface{})
	if aps["content-available"] != float64(1) {
		t.Errorf("aps.content-available = %v; want 1", aps["content-available"])
	}
}

// TestSend_Non200ReturnsPushError verifies non-200 responses return a
// structured *push.PushError with the HTTP status, reason, and (for
// 410) the Unregistered flag — so SendToUserDetailed can extract them
// for the WASM caller. Bugboard #348.
func TestSend_Non200ReturnsPushError(t *testing.T) {
	cases := []struct {
		name             string
		status           int
		reason           string
		wantUnregistered bool
	}{
		{"410_unregistered", http.StatusGone, "Unregistered", true},
		{"400_bad_device_token", http.StatusBadRequest, "BadDeviceToken", false},
		{"403_invalid_provider_token", http.StatusForbidden, "InvalidProviderToken", false},
		{"500_internal_apple_error", http.StatusInternalServerError, "InternalServerError", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakePushClient{
				resp: &apns2.Response{StatusCode: tc.status, Reason: tc.reason, ApnsID: "x"},
			}
			p := newTestProvider(t, "com.example.app", fake)
			err := p.Send(context.Background(), push.PushMessage{
				DeviceToken: "tok",
				Title:       "x",
			})
			if err == nil {
				t.Fatal("expected error for non-200 response")
			}
			var perr *push.PushError
			if !errors.As(err, &perr) {
				t.Fatalf("expected *push.PushError; got %T: %v", err, err)
			}
			if perr.HTTPStatus != tc.status {
				t.Errorf("HTTPStatus = %d; want %d", perr.HTTPStatus, tc.status)
			}
			if perr.Reason != tc.reason {
				t.Errorf("Reason = %q; want %q", perr.Reason, tc.reason)
			}
			if perr.Unregistered != tc.wantUnregistered {
				t.Errorf("Unregistered = %v; want %v", perr.Unregistered, tc.wantUnregistered)
			}
		})
	}
}

// TestSend_410StillCompatibleWithLegacySentinel ensures the structured
// PushError for 410 ALSO satisfies errors.Is(ErrDeviceUnregistered) so
// existing callers using the sentinel keep working.
func TestSend_410StillCompatibleWithLegacySentinel(t *testing.T) {
	fake := &fakePushClient{
		resp: &apns2.Response{StatusCode: http.StatusGone, Reason: "Unregistered", ApnsID: "x"},
	}
	p := newTestProvider(t, "com.example.app", fake)
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "tok",
		Title:       "x",
	})
	if !errors.Is(err, ErrDeviceUnregistered) {
		t.Errorf("expected errors.Is(err, ErrDeviceUnregistered) to be true; got %v", err)
	}
}

// TestHasVisibleContent exercises every accepted shape so the guard
// matches the WASM caller's mental model.
func TestHasVisibleContent(t *testing.T) {
	cases := []struct {
		name string
		msg  push.PushMessage
		want bool
	}{
		{"empty", push.PushMessage{}, false},
		{"title only", push.PushMessage{Title: "hi"}, true},
		{"body only", push.PushMessage{Body: "hi"}, true},
		{"badge only", push.PushMessage{Badge: 1}, true},
		{"sound only", push.PushMessage{Sound: "ping.aiff"}, true},
		{"content_available bool true", push.PushMessage{Data: map[string]interface{}{"content_available": true}}, true},
		{"content_available bool false", push.PushMessage{Data: map[string]interface{}{"content_available": false}}, false},
		{"content_available int 1", push.PushMessage{Data: map[string]interface{}{"content_available": 1}}, true},
		{"content_available string 1", push.PushMessage{Data: map[string]interface{}{"content_available": "1"}}, true},
		{"content_available string true", push.PushMessage{Data: map[string]interface{}{"content_available": "true"}}, true},
		{"data without content_available", push.PushMessage{Data: map[string]interface{}{"other_key": "value"}}, false},
		{"title and badge", push.PushMessage{Title: "x", Badge: 5}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasVisibleContent(tc.msg); got != tc.want {
				t.Errorf("hasVisibleContent(%+v) = %v; want %v", tc.msg, got, tc.want)
			}
		})
	}
}
