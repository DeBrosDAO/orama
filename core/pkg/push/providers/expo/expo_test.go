package expo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/push"
)

// roundTripFunc lets us mock http.Client transport for the Expo provider so
// we can assert against requests without hitting exp.host.
type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func newTestProvider(rt roundTripFunc) *Provider {
	p := New(Config{}, nil)
	p.httpClient.Transport = rt
	return p
}

func TestSend_empty_token_returns_ErrEmptyToken(t *testing.T) {
	p := New(Config{}, nil)
	err := p.Send(context.Background(), push.PushMessage{Body: "x"})
	if err != push.ErrEmptyToken {
		t.Errorf("expected ErrEmptyToken, got %v", err)
	}
}

func TestSend_happy_path(t *testing.T) {
	var gotPayload map[string]interface{}
	var gotAuth string

	p := newTestProvider(func(req *http.Request) (*http.Response, error) {
		gotAuth = req.Header.Get("Authorization")
		body, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(body, &gotPayload)
		resp := httptest.NewRecorder()
		resp.WriteHeader(200)
		_, _ = resp.WriteString(`{"data":[{"status":"ok"}]}`)
		return resp.Result(), nil
	})
	p.accessToken = "secret-token"

	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "ExponentPushToken[abc]",
		Title:       "T", Body: "B",
		Priority: push.PriorityHigh,
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("auth header wrong: %s", gotAuth)
	}
	if gotPayload["to"] != "ExponentPushToken[abc]" {
		t.Errorf("to wrong: %v", gotPayload["to"])
	}
	if gotPayload["priority"] != "high" {
		t.Errorf("priority wrong: %v", gotPayload["priority"])
	}
}

func TestSend_ticket_error_returns_error(t *testing.T) {
	p := newTestProvider(func(req *http.Request) (*http.Response, error) {
		resp := httptest.NewRecorder()
		resp.WriteHeader(200)
		_, _ = resp.WriteString(`{"data":[{"status":"error","message":"DeviceNotRegistered"}]}`)
		return resp.Result(), nil
	})
	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "x", Body: "y"})
	if err == nil {
		t.Fatal("expected error for ticket failure")
	}
}

func TestSend_http_error_returns_error(t *testing.T) {
	p := newTestProvider(func(req *http.Request) (*http.Response, error) {
		resp := httptest.NewRecorder()
		resp.WriteHeader(500)
		_, _ = resp.WriteString(`upstream broken`)
		return resp.Result(), nil
	})
	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "x", Body: "y"})
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestSend_normal_priority_maps_to_default(t *testing.T) {
	var gotPayload map[string]interface{}
	p := newTestProvider(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(body, &gotPayload)
		resp := httptest.NewRecorder()
		resp.WriteHeader(200)
		_, _ = resp.WriteString(`{"data":[{"status":"ok"}]}`)
		return resp.Result(), nil
	})
	if err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "x", Body: "y", Priority: push.PriorityNormal,
	}); err != nil {
		t.Fatal(err)
	}
	if gotPayload["priority"] != "default" {
		t.Errorf("expected priority=default, got %v", gotPayload["priority"])
	}
}

// TestSend_setsCollapseIDFromMessageID is the bugboard #833 guard: a non-empty
// MessageID flows to Expo's collapseId field (which Expo maps to FCM
// collapse_key / APNs apns-collapse-id) so a superseded push collapses.
func TestSend_setsCollapseIDFromMessageID(t *testing.T) {
	var gotPayload map[string]interface{}
	p := newTestProvider(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(body, &gotPayload)
		resp := httptest.NewRecorder()
		resp.WriteHeader(200)
		_, _ = resp.WriteString(`{"data":[{"status":"ok"}]}`)
		return resp.Result(), nil
	})
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "ExponentPushToken[abc]", Title: "T", Body: "B",
		MessageID: "msg-123",
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if gotPayload["collapseId"] != "msg-123" {
		t.Errorf("collapseId = %v; want msg-123", gotPayload["collapseId"])
	}
}

// TestSend_emptyMessageID_omitsCollapseID: no MessageID → collapseId omitted
// (omitempty) so each push stays distinct.
func TestSend_emptyMessageID_omitsCollapseID(t *testing.T) {
	var gotPayload map[string]interface{}
	p := newTestProvider(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(body, &gotPayload)
		resp := httptest.NewRecorder()
		resp.WriteHeader(200)
		_, _ = resp.WriteString(`{"data":[{"status":"ok"}]}`)
		return resp.Result(), nil
	})
	_ = p.Send(context.Background(), push.PushMessage{DeviceToken: "ExponentPushToken[abc]", Body: "B"})
	if _, present := gotPayload["collapseId"]; present {
		t.Errorf("collapseId should be omitted when MessageID empty, got %v", gotPayload["collapseId"])
	}
}

func TestName(t *testing.T) {
	if New(Config{}, nil).Name() != "expo" {
		t.Error("expected Name=expo")
	}
}
