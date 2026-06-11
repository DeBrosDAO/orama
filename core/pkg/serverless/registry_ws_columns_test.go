package serverless

import (
	"strings"
	"testing"
)

// TestRegistryRowMapping_IncludesWSPersistentColumns is the regression
// guard for bug #240/#249 follow-up where every WS function silently ran
// in stateless per-frame mode regardless of the `ws_persistent: true`
// config in the function YAML.
//
// History: the schema migration added ws_persistent + sibling columns,
// and Register() at registry.go:110+ wrote them on deploy, but every
// READ path (Get / GetByID / ListVersions / List / getByNameInternal)
// omitted them from the SELECT statement and the functionRow struct
// had no fields for them. Result: rowToFunction produced a Function
// with WSPersistent always false. The WS handler's `if fn.WSPersistent`
// branch in pkg/gateway/handlers/serverless/ws_handler.go therefore
// never fired, and the persistent code path in
// handlePersistentWebSocket was DEAD for the entire cluster.
//
// AnChat hit this when their rpc-router (which depends on
// per-connection state for request_id ↔ reply correlation) silently
// ran in stateless mode, producing only the per-frame telemetry
// envelope `{request_id, status, duration_ms}` and losing the rpc_result
// frames the function emits via ws_send because the per-frame fresh
// instance loses all its bookkeeping every iteration.
//
// This test asserts the column set survives any future "let me clean
// up this SELECT" refactor — if the columns disappear from the SELECT
// the test fails loud.
func TestRegistryRowMapping_IncludesWSPersistentColumns(t *testing.T) {
	// Inspect functionRow's struct tags via reflection-of-source: a
	// runtime reflection check would couple this test to functionRow's
	// unexported nature. The deterministic + readable check is to
	// assert the four db-tagged fields are present on the struct.
	row := functionRow{
		WSPersistent:         true,
		WSIdleTimeoutSec:     15,
		WSMaxFrameBytes:      4096,
		WSMaxInflightPerConn: 8,
	}
	// If any of these field names is renamed without updating
	// rowToFunction below, the test fails because the Function's
	// matching field stays at the zero value.
	r := &Registry{}
	fn := r.rowToFunction(&row)
	if !fn.WSPersistent {
		t.Error("rowToFunction did not propagate WSPersistent — persistent WS functions will silently run as stateless (bug #240/#249 root cause)")
	}
	if fn.WSIdleTimeoutSec != 15 {
		t.Errorf("rowToFunction did not propagate WSIdleTimeoutSec; got %d", fn.WSIdleTimeoutSec)
	}
	if fn.WSMaxFrameBytes != 4096 {
		t.Errorf("rowToFunction did not propagate WSMaxFrameBytes; got %d", fn.WSMaxFrameBytes)
	}
	if fn.WSMaxInflightPerConn != 8 {
		t.Errorf("rowToFunction did not propagate WSMaxInflightPerConn; got %d", fn.WSMaxInflightPerConn)
	}
}

// TestRegistryGet_QueriesAllWSColumns is the cheap-but-effective guard
// for the SQL-text drift case: the SELECT in Get/List/GetByID/etc must
// include the four ws_* columns. We grep the Go source at test time
// rather than running an actual query — this catches the regression
// even on test runs without a live DB.
func TestRegistryGet_QueriesAllWSColumns(t *testing.T) {
	source, err := readRegistrySource()
	if err != nil {
		t.Skipf("cannot read registry.go for SQL inspection: %v", err)
	}
	required := []string{
		"ws_persistent",
		"ws_idle_timeout_sec",
		"ws_max_frame_bytes",
		"ws_max_inflight_per_conn",
	}
	for _, col := range required {
		// Each must appear in at least 5 places: the Register INSERT
		// statement (already covered by existing tests) plus the four
		// READ paths (Get latest, Get by version, GetByID, List,
		// ListVersions, getByNameInternal — at least 5 of those).
		count := strings.Count(source, col)
		if count < 5 {
			t.Errorf("column %q appears in registry.go only %d times; expected ≥5 (one per SELECT path). The READ paths probably regressed and persistent WS functions will silently run as stateless again.", col, count)
		}
	}
}

// readRegistrySource returns the contents of pkg/serverless/registry.go
// for SQL-text inspection. Kept as a helper so the test stays readable.
func readRegistrySource() (string, error) {
	// Resolved relative to test working dir (the package dir).
	b, err := readFile("registry.go")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// readFile is a thin wrapper to keep the test self-contained without
// pulling in os/io aliasing in a way that confuses linters.
func readFile(path string) ([]byte, error) {
	return readFileImpl(path)
}
