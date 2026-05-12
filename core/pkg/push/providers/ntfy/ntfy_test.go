package ntfy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/push"
)

func TestSend_happy_path(t *testing.T) {
	var (
		gotPath    string
		gotBody    string
		gotTitle   string
		gotPriority string
		gotAuth    string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, AuthToken: "secret"}, nil)
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "ns/myapp/user-1",
		Title:       "Hello",
		Body:        "World",
		Priority:    push.PriorityHigh,
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if gotPath != "/ns/myapp/user-1" {
		t.Errorf("expected path /ns/myapp/user-1, got %s", gotPath)
	}
	if gotTitle != "Hello" {
		t.Errorf("expected Title=Hello, got %s", gotTitle)
	}
	if gotPriority != "high" {
		t.Errorf("expected Priority=high, got %s", gotPriority)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("expected Authorization=Bearer secret, got %s", gotAuth)
	}
	if gotBody != "World" {
		t.Errorf("expected body=World, got %s", gotBody)
	}
}

func TestSend_includes_data_header_when_data_set(t *testing.T) {
	var gotData string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotData = r.Header.Get("X-Data")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL}, nil)
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "topic",
		Body:        "x",
		Data:        map[string]interface{}{"call_id": "abc-123"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(gotData)
	if err != nil {
		t.Fatalf("X-Data not valid base64: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(decoded, &got); err != nil {
		t.Fatalf("X-Data not valid JSON: %v", err)
	}
	if got["call_id"] != "abc-123" {
		t.Errorf("data round-trip failed: got %v", got)
	}
}

func TestSend_no_data_no_data_header(t *testing.T) {
	var gotData string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotData = r.Header.Get("X-Data")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL}, nil)
	if err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if gotData != "" {
		t.Errorf("expected no X-Data header, got %q", gotData)
	}
}

func TestSend_no_auth_header_when_token_empty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL}, nil)
	if err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Errorf("expected no Authorization header, got %q", gotAuth)
	}
}

func TestSend_4xx_returns_error_with_body_excerpt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden topic"))
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL}, nil)
	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"})
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("error should mention status and body, got: %v", err)
	}
}

func TestSend_empty_token_returns_ErrEmptyToken(t *testing.T) {
	p := New(Config{BaseURL: "http://example.invalid"}, nil)
	err := p.Send(context.Background(), push.PushMessage{Body: "x"})
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if err != push.ErrEmptyToken {
		t.Errorf("expected ErrEmptyToken, got %v", err)
	}
}

func TestSend_short_timeout_returns_error(t *testing.T) {
	// Server that blocks for 2s — provider with 100ms timeout should give up.
	blockUntil := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-blockUntil:
		case <-time.After(2 * time.Second):
		}
	}))
	defer func() { close(blockUntil); srv.Close() }()

	p := New(Config{BaseURL: srv.URL, Timeout: 100 * time.Millisecond}, nil)
	start := time.Now()
	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"})
	elapsed := time.Since(start)
	if err == nil {
		t.Error("expected timeout error")
	}
	if elapsed > 1*time.Second {
		t.Errorf("expected fast timeout, took %v", elapsed)
	}
}

func TestSend_no_baseURL_returns_error(t *testing.T) {
	p := New(Config{}, nil)
	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"})
	if err == nil {
		t.Fatal("expected error for missing base URL")
	}
}

func TestName(t *testing.T) {
	p := New(Config{BaseURL: "http://x"}, nil)
	if p.Name() != "ntfy" {
		t.Errorf("expected Name=ntfy, got %s", p.Name())
	}
}
