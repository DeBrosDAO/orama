package deployments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"go.uber.org/zap"
)

func logsRequest(query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/deployments/logs?"+query, nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "ns"))
	rec := httptest.NewRecorder()
	// Every refusal below happens before the deployment is looked up.
	NewLogsHandler(nil, nil, zap.NewNop()).HandleLogs(rec, req)
	return rec
}

func TestHandleLogs_refusesBadLineCounts(t *testing.T) {
	for _, lines := range []string{"0", "-3", "abc", "1001", "99999999999999999999"} {
		if rec := logsRequest("name=app&lines=" + lines); rec.Code != http.StatusBadRequest {
			t.Errorf("lines=%s answered %d, want 400", lines, rec.Code)
		}
	}
}

func TestHandleLogs_followIsRefusedWithAReason(t *testing.T) {
	rec := logsRequest("name=app&lines=20&follow=true")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("follow answered %d, want 501", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "--follow") {
		t.Errorf("the refusal does not say what to do: %q", rec.Body.String())
	}
}

func TestHandleLogs_requiresName(t *testing.T) {
	if rec := logsRequest("lines=20"); rec.Code != http.StatusBadRequest {
		t.Errorf("no name answered %d, want 400", rec.Code)
	}
}

func TestHandleLogs_busyNamespaceIsRefusedWith429(t *testing.T) {
	h := NewLogsHandler(nil, nil, zap.NewNop())
	for i := 0; i < maxConcurrentLogReadsPerNamespace; i++ {
		if !h.acquireLogRead("ns") {
			t.Fatalf("slot %d refused below the cap", i)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/deployments/logs?name=app", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "ns"))
	rec := httptest.NewRecorder()
	h.HandleLogs(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("busy namespace answered %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 has no Retry-After")
	}
}

func TestLogReadSlots_perNamespaceAndReleased(t *testing.T) {
	h := NewLogsHandler(nil, nil, zap.NewNop())
	for i := 0; i < maxConcurrentLogReadsPerNamespace; i++ {
		h.acquireLogRead("a")
	}
	if h.acquireLogRead("a") {
		t.Error("a slot past the cap was granted")
	}
	if !h.acquireLogRead("b") {
		t.Error("another namespace was starved by a")
	}
	h.releaseLogRead("a")
	if !h.acquireLogRead("a") {
		t.Error("a released slot was not reusable")
	}
	h.releaseLogRead("zzz") // never acquired: must not panic or go negative
	if !h.acquireLogRead("zzz") {
		t.Error("release of an unheld namespace broke its count")
	}
}
