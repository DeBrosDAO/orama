package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/cli/printer"
)

// The gateway answers newest first, because that is the half of the trail a
// page limit should keep. A trail reads the other way round, and --follow
// depends on the last line printed being the newest one it has seen.

func auditServer(t *testing.T, events []map[string]any, seen *seenRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			seen.path = r.URL.Path
			seen.query = r.URL.Query().Encode()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"namespace": "acme", "events": events, "count": len(events)})
	}))
}

type seenRequest struct{ path, query string }

func TestFetchAuditPage_returnsOldestFirst(t *testing.T) {
	server := auditServer(t, []map[string]any{
		{"action": "key.revoke", "created_at": "2026-09-04 12:00:00", "result": "success"},
		{"action": "key.issue", "created_at": "2026-09-04 10:00:00", "result": "success"},
	}, nil)
	defer server.Close()

	rows, newest, err := fetchAuditPage(server.URL, "key", AuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("fetchAuditPage: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d rows", len(rows))
	}
	if rows[0][1] != "key.issue" || rows[1][1] != "key.revoke" {
		t.Errorf("order = %v, %v — want oldest first", rows[0][1], rows[1][1])
	}
	if newest != "2026-09-04 12:00:00" {
		t.Errorf("newest = %q, want the latest created_at in the page", newest)
	}
}

func TestFetchAuditPage_sendsEveryFilter(t *testing.T) {
	seen := &seenRequest{}
	server := auditServer(t, nil, seen)
	defer server.Close()

	if _, _, err := fetchAuditPage(server.URL, "key", AuditFilter{
		Action: "key.issue", Principal: "0xowner", Since: "2026-09-04 10:00:00", Limit: 7,
	}); err != nil {
		t.Fatalf("fetchAuditPage: %v", err)
	}

	for _, want := range []string{"action=key.issue", "principal=0xowner", "limit=7", "since="} {
		if !strings.Contains(seen.query, want) {
			t.Errorf("query %q is missing %q", seen.query, want)
		}
	}
	if seen.path != "/v1/audit" {
		t.Errorf("path = %q", seen.path)
	}
}

// An empty page has no newest row; carrying "" forward would make --follow ask
// for the whole trail again on the next tick.
func TestFetchAuditPage_emptyPageHasNoNewest(t *testing.T) {
	server := auditServer(t, []map[string]any{}, nil)
	defer server.Close()

	rows, newest, err := fetchAuditPage(server.URL, "key", AuditFilter{Limit: 50})
	if err != nil {
		t.Fatalf("fetchAuditPage: %v", err)
	}
	if len(rows) != 0 || newest != "" {
		t.Errorf("rows = %v, newest = %q", rows, newest)
	}
}

func TestAuditList_refusesAnUnknownAction(t *testing.T) {
	err := AuditList(printer.New(&bytes.Buffer{}, &bytes.Buffer{}), AuditFilter{Action: "key.issued"})
	if err == nil {
		t.Fatal("an unknown action was accepted")
	}
	if !strings.Contains(err.Error(), "key.issued") {
		t.Errorf("error = %v, want it to name the action", err)
	}
}
