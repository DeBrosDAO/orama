package serverless

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

func logsOf(t *testing.T, reg *mockRegistry, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := asCredentialOf(httptest.NewRequest(http.MethodGet, "/v1/functions/myFunc/logs"+query, nil), "test")
	rec := httptest.NewRecorder()
	newTestHandlers(reg).GetFunctionLogs(rec, req, "myFunc")
	return rec
}

// Bug: the logs of a function that does not exist were a 200 with an empty
// list, which a caller cannot tell from a function that has not run yet.
func TestGetFunctionLogs_aFunctionThatDoesNotExistIsNotFound(t *testing.T) {
	for _, query := range []string{"", "?wasm_only=1"} {
		if rec := logsOf(t, newMockRegistry(), query); rec.Code != http.StatusNotFound {
			t.Errorf("logs%s of an absent function: status %d, want 404", query, rec.Code)
		}
	}
}

// A function that exists and has not run has nothing to show: that is a 200,
// and one that ran keeps its history.
func TestGetFunctionLogs_aFunctionThatHasNotRunIsAnEmptyList(t *testing.T) {
	reg := newMockRegistry()
	reg.functions["test/myFunc"] = &serverless.Function{Name: "myFunc", Namespace: "test"}
	for _, query := range []string{"", "?wasm_only=1"} {
		rec := logsOf(t, reg, query)
		if rec.Code != http.StatusOK {
			t.Fatalf("logs%s of an existing function that has not run: status %d", query, rec.Code)
		}
		if body := decodeBody(t, rec); body["count"] != float64(0) {
			t.Errorf("count = %v, want 0", body["count"])
		}
	}
}

// A function that was deleted keeps the history it made; the answer for it
// does not depend on the registry still knowing it.
func TestGetFunctionLogs_theHistoryOfADeletedFunctionIsStillShown(t *testing.T) {
	reg := newMockRegistry()
	reg.invocations = []serverless.Invocation{{ID: "inv-1", RequestID: "req-A", Status: "success"}}
	if rec := logsOf(t, reg, ""); rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
}

func TestGetFunctionLogs_aFailedLookupIsNotReportedAsNotFound(t *testing.T) {
	reg := newMockRegistry()
	reg.getErr = http.ErrHandlerTimeout
	if rec := logsOf(t, reg, ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

func TestWriteVersions_noVersionsIsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	writeVersions(rec, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("no versions: status %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	writeVersions(rec, []*serverless.Function{{Name: "myFunc", Version: 1}, {Name: "myFunc", Version: 2}})
	if rec.Code != http.StatusOK {
		t.Fatalf("two versions: status %d, want 200", rec.Code)
	}
	if body := decodeBody(t, rec); body["count"] != float64(2) {
		t.Errorf("count = %v, want 2", body["count"])
	}
}
