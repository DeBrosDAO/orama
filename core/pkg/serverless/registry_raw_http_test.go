package serverless

import (
	"strings"
	"testing"
)

// TestRegistryRowMapping_IncludesRawHTTPResponse guards the raw-HTTP-response
// column (bugboard #835): rowToFunction must copy raw_http_response off the DB
// row, otherwise the engine's `if fn.RawHTTPResponse` branch never attaches a
// collector and set_http_response is a permanent no-op for every function.
func TestRegistryRowMapping_IncludesRawHTTPResponse(t *testing.T) {
	row := functionRow{RawHTTPResponse: true}
	r := &Registry{}
	fn := r.rowToFunction(&row)
	if !fn.RawHTTPResponse {
		t.Error("rowToFunction did not propagate RawHTTPResponse — raw-HTTP functions would silently fall back to JSON/Ack output (bugboard #835)")
	}
}

// TestRegistry_QueriesRawHTTPResponseColumn is the SQL-text drift guard: the
// raw_http_response column must appear in the INSERT plus every READ-path
// SELECT, mirroring the ws_* column guard. Counted ≥5 (one INSERT + the
// Get/GetByID/List/ListVersions/getByNameInternal SELECTs).
func TestRegistry_QueriesRawHTTPResponseColumn(t *testing.T) {
	source, err := readRegistrySource()
	if err != nil {
		t.Skipf("cannot read registry.go for SQL inspection: %v", err)
	}
	count := strings.Count(source, "raw_http_response")
	if count < 5 {
		t.Errorf("column raw_http_response appears in registry.go only %d times; expected ≥5 (INSERT + each SELECT path). A READ path probably dropped it and raw-HTTP functions will silently fall back to JSON output.", count)
	}
}
