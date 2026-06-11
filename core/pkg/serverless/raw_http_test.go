package serverless

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestSetRawHTTPResponse_happyPath(t *testing.T) {
	ctx := WithRawHTTPCollector(context.Background())

	headers := map[string]string{"Content-Type": "application/json"}
	body := []byte(`{"jsonrpc":"2.0","result":42}`)
	if err := SetRawHTTPResponse(ctx, 200, headers, body); err != nil {
		t.Fatalf("SetRawHTTPResponse: unexpected error: %v", err)
	}

	res, ok := TakeRawHTTPResponse(ctx)
	if !ok {
		t.Fatal("TakeRawHTTPResponse: expected a response to be set")
	}
	if res.Status != 200 {
		t.Errorf("status = %d, want 200", res.Status)
	}
	if res.Headers["Content-Type"] != "application/json" {
		t.Errorf("Content-Type header = %q, want application/json", res.Headers["Content-Type"])
	}
	if !bytes.Equal(res.Body, body) {
		t.Errorf("body = %q, want %q", res.Body, body)
	}
}

func TestSetRawHTTPResponse_copiesBodyAndHeaders(t *testing.T) {
	ctx := WithRawHTTPCollector(context.Background())

	headers := map[string]string{"X-Test": "v1"}
	body := []byte("original")
	if err := SetRawHTTPResponse(ctx, 200, headers, body); err != nil {
		t.Fatalf("SetRawHTTPResponse: %v", err)
	}

	// Mutate caller-owned buffers AFTER the call — the stored copy must not change.
	body[0] = 'X'
	headers["X-Test"] = "mutated"

	res, _ := TakeRawHTTPResponse(ctx)
	if string(res.Body) != "original" {
		t.Errorf("body was not copied: got %q", res.Body)
	}
	if res.Headers["X-Test"] != "v1" {
		t.Errorf("headers were not copied: got %q", res.Headers["X-Test"])
	}
}

func TestSetRawHTTPResponse_noCollector(t *testing.T) {
	// No collector attached → the function is not in raw mode; must error.
	err := SetRawHTTPResponse(context.Background(), 200, nil, []byte("x"))
	if err == nil {
		t.Fatal("expected error when no collector is attached")
	}
	if !strings.Contains(err.Error(), "raw_http_response") {
		t.Errorf("error = %q, want it to mention raw_http_response", err.Error())
	}
}

func TestSetRawHTTPResponse_rejectsBadStatus(t *testing.T) {
	for _, status := range []int{0, 99, 600, 1000, -1} {
		ctx := WithRawHTTPCollector(context.Background())
		if err := SetRawHTTPResponse(ctx, status, nil, nil); err == nil {
			t.Errorf("status %d: expected validation error, got nil", status)
		}
		if _, ok := TakeRawHTTPResponse(ctx); ok {
			t.Errorf("status %d: response should not be set after a rejected status", status)
		}
	}
}

func TestSetRawHTTPResponse_rejectsTooManyHeaders(t *testing.T) {
	ctx := WithRawHTTPCollector(context.Background())
	headers := make(map[string]string, rawHTTPMaxHeaders+1)
	for i := 0; i <= rawHTTPMaxHeaders; i++ {
		headers["h"+string(rune('a'+i%26))+string(rune('0'+i/26))] = "v"
	}
	if len(headers) <= rawHTTPMaxHeaders {
		t.Fatalf("test setup: expected > %d headers, got %d", rawHTTPMaxHeaders, len(headers))
	}
	if err := SetRawHTTPResponse(ctx, 200, headers, nil); err == nil {
		t.Fatal("expected error for too many headers")
	}
}

func TestSetRawHTTPResponse_rejectsOversizedBody(t *testing.T) {
	ctx := WithRawHTTPCollector(context.Background())
	body := make([]byte, rawHTTPMaxBodyBytes+1)
	if err := SetRawHTTPResponse(ctx, 200, nil, body); err == nil {
		t.Fatal("expected error for oversized body")
	}
}

func TestTakeRawHTTPResponse_notSet(t *testing.T) {
	// Collector attached but set_http_response never called → (zero, false).
	ctx := WithRawHTTPCollector(context.Background())
	if _, ok := TakeRawHTTPResponse(ctx); ok {
		t.Fatal("expected ok=false when no response was set")
	}

	// No collector at all → also (zero, false).
	if _, ok := TakeRawHTTPResponse(context.Background()); ok {
		t.Fatal("expected ok=false with no collector")
	}
}

func TestSetRawHTTPResponse_lastWriteWins(t *testing.T) {
	ctx := WithRawHTTPCollector(context.Background())
	if err := SetRawHTTPResponse(ctx, 200, nil, []byte("first")); err != nil {
		t.Fatalf("first SetRawHTTPResponse: %v", err)
	}
	if err := SetRawHTTPResponse(ctx, 503, map[string]string{"Retry-After": "5"}, []byte("second")); err != nil {
		t.Fatalf("second SetRawHTTPResponse: %v", err)
	}
	res, ok := TakeRawHTTPResponse(ctx)
	if !ok {
		t.Fatal("expected response to be set")
	}
	if res.Status != 503 || string(res.Body) != "second" || res.Headers["Retry-After"] != "5" {
		t.Errorf("last-write-wins failed: got status=%d body=%q headers=%v", res.Status, res.Body, res.Headers)
	}
}
