package serverless

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A namespace database without a leader answered every function route with an
// unclassified 500 (stagenet e2e, 2026-09-30: listing functions just after
// provisioning). It is a retryable 503 now, and no driver text reaches the
// caller either way.
func TestWriteStoreError(t *testing.T) {
	unavailable := errors.New("tried all peers unsuccessfully: 503 Service Unavailable, message: leader not found")
	rec := httptest.NewRecorder()
	writeStoreError(rec, "Failed to list functions", unavailable)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"retryable":true`) {
		t.Fatalf("no leader: %d %s, want a retryable 503", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "peers") {
		t.Errorf("the driver's text reached the caller: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	writeStoreError(rec, "Failed to set secret", errors.New("UNIQUE constraint failed: function_secrets.name"))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "UNIQUE") {
		t.Fatalf("a fault: %d %s, want a 500 without the driver's text", rec.Code, rec.Body.String())
	}
}
