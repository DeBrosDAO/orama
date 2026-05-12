package gateway

import (
	"strings"
	"testing"
)

// TestAppendRQLiteQueryParams_consistencyLevelWeak is the regression guard
// for bug #235. The DSN passed to gorqlite MUST encode `level=weak` so reads
// route to the leader and see all committed writes from earlier in the same
// serverless invocation. `level=none` (the previous default) read from the
// local follower's possibly-stale state and broke `INSERT → UPDATE → SELECT`
// patterns inside host functions.
func TestAppendRQLiteQueryParams_consistencyLevelWeak(t *testing.T) {
	got := appendRQLiteQueryParams("http://localhost:5001")
	if !strings.Contains(got, "level=weak") {
		t.Errorf("DSN missing level=weak (bug #235 regression):\n%s", got)
	}
	if strings.Contains(got, "level=none") {
		t.Errorf("DSN must NOT carry level=none (bug #235):\n%s", got)
	}
	if !strings.Contains(got, "disableClusterDiscovery=true") {
		t.Errorf("DSN missing disableClusterDiscovery=true:\n%s", got)
	}
}

// TestAppendRQLiteQueryParams_existingQueryString — when the inbound DSN
// already has a `?param=value` segment (e.g. authentication appended
// upstream), the new params must be `&`-joined, not start a fresh `?`.
func TestAppendRQLiteQueryParams_existingQueryString(t *testing.T) {
	got := appendRQLiteQueryParams("http://localhost:5001?foo=bar")
	if strings.Count(got, "?") != 1 {
		t.Errorf("expected exactly one '?' in DSN, got: %s", got)
	}
	if !strings.Contains(got, "?foo=bar&disableClusterDiscovery=true&level=weak") {
		t.Errorf("DSN didn't append params with '&' join:\n%s", got)
	}
}

// TestAppendRQLiteQueryParams_noExistingQueryString — when no `?` is present,
// the params must be introduced with a `?` not an `&`.
func TestAppendRQLiteQueryParams_noExistingQueryString(t *testing.T) {
	got := appendRQLiteQueryParams("http://localhost:5001")
	if !strings.HasSuffix(got, "?disableClusterDiscovery=true&level=weak") {
		t.Errorf("DSN didn't introduce query string with '?':\n%s", got)
	}
}

// TestAppendRQLiteQueryParams_preservesAuthCredentials — credentials injected
// upstream by injectRQLiteAuth must survive the param append unchanged.
func TestAppendRQLiteQueryParams_preservesAuthCredentials(t *testing.T) {
	got := appendRQLiteQueryParams("http://orama:secret@localhost:5001")
	if !strings.Contains(got, "orama:secret@localhost:5001") {
		t.Errorf("auth credentials lost:\n%s", got)
	}
	if !strings.Contains(got, "level=weak") {
		t.Errorf("level=weak missing after auth-injected DSN:\n%s", got)
	}
}
