package hetzner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New("test-token", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func errorBody(code, msg string) string {
	return `{"error":{"code":"` + code + `","message":"` + msg + `"}}`
}

func TestNew_emptyToken(t *testing.T) {
	if _, err := New("  ", ""); err == nil || !strings.Contains(err.Error(), "HCLOUD_TOKEN") {
		t.Fatalf("New with an empty token: %v", err)
	}
}

func TestDo_sendsBearerToken(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"server":{"id":7}}`))
	}))
	s, err := c.GetServer(context.Background(), 7)
	if err != nil || s.ID != 7 {
		t.Fatalf("GetServer: %v %+v", err, s)
	}
}

func TestDo_errorMappingWithoutRetry(t *testing.T) {
	cases := []struct {
		status int
		code   string
		want   string
	}{
		{http.StatusUnauthorized, "unauthorized", "HCLOUD_TOKEN was rejected"},
		{http.StatusForbidden, "forbidden", "read/write token"},
		{http.StatusUnprocessableEntity, codeLimitExceeded, "resource limit"},
		{http.StatusInternalServerError, "server_error", "not retried"},
		{http.StatusServiceUnavailable, "unavailable", "not retried"},
	}
	for _, tc := range cases {
		var calls atomic.Int32
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(errorBody(tc.code, "boom")))
		}))
		_, err := c.GetServer(context.Background(), 1)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("HTTP %d: error %v, want it to mention %q", tc.status, err, tc.want)
		}
		if calls.Load() != 1 {
			t.Errorf("HTTP %d: %d requests, want exactly 1 (no retry)", tc.status, calls.Load())
		}
	}
}

func TestDo_nonJSONErrorBody(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	}))
	_, err := c.GetServer(context.Background(), 1)
	if err == nil || !strings.Contains(err.Error(), "bad gateway") {
		t.Fatalf("error %v", err)
	}
}

func TestDo_rateLimitRetriesAfterDocumentedWait(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"server":{"id":3}}`))
	}))
	if _, err := c.GetServer(context.Background(), 3); err != nil {
		t.Fatalf("GetServer after a 429: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("%d requests, want 2", calls.Load())
	}
}

func TestDo_rateLimitWithoutHeaderFails(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	if _, err := c.GetServer(context.Background(), 1); err == nil {
		t.Fatal("a 429 with no documented wait succeeded")
	}
	if calls.Load() != 1 {
		t.Fatalf("%d requests, want 1", calls.Load())
	}
}

func TestDo_rateLimitWaitTooLongFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	start := time.Now()
	if _, err := c.GetServer(context.Background(), 1); err == nil {
		t.Fatal("a 429 asking for an hour succeeded")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the client waited on a wait longer than it honours")
	}
}

func TestDo_rateLimitRetriesAreBounded(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	if _, err := c.GetServer(context.Background(), 1); err == nil {
		t.Fatal("an endless 429 succeeded")
	}
	if got := int(calls.Load()); got != maxRateLimitRetries+1 {
		t.Fatalf("%d requests, want %d", got, maxRateLimitRetries+1)
	}
}

func TestRateLimitWait_resetHeader(t *testing.T) {
	c := &Client{now: func() time.Time { return time.Unix(1000, 0) }}
	h := http.Header{}
	h.Set("RateLimit-Reset", strconv.Itoa(1010))
	wait, ok := c.rateLimitWait(h)
	if !ok || wait != 10*time.Second {
		t.Fatalf("wait %s ok %v, want 10s", wait, ok)
	}
	h.Set("RateLimit-Reset", "900")
	if wait, ok := c.rateLimitWait(h); !ok || wait != 0 {
		t.Fatalf("a past reset: wait %s ok %v", wait, ok)
	}
}

func TestDo_contextCancelledDuringRateLimitWait(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.GetServer(ctx, 1); err == nil {
		t.Fatal("GetServer outlived its context")
	}
}
