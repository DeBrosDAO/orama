package rqlite

// freshness_read_test.go covers the native none+freshness HTTP read (#1021):
// the URL it builds, row materialization + caps, and the typed rejection path.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rqlite/gorqlite"
)

// freshTestClient builds a *client whose native none+freshness path points at
// the given test server URL (scheme://host).
func freshTestClient(serverURL string) *client {
	// serverURL is "http://127.0.0.1:PORT"; split scheme/host.
	rest := strings.TrimPrefix(serverURL, "http://")
	return &client{
		freshScheme: "http",
		freshHost:   rest,
		freshHTTP:   &http.Client{Timeout: 2 * time.Second},
	}
}

func TestQueryNoneFresh_buildsExactURLAndDecodesRows(t *testing.T) {
	var gotPath, gotRawQuery string
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"results":[{"columns":["id","name"],"types":["integer","text"],"values":[[1,"alice"],[2,"bob"]]}]}`))
	}))
	defer srv.Close()

	c := freshTestClient(srv.URL)
	stmts := []gorqlite.ParameterizedStatement{{Query: "SELECT id,name FROM t"}}
	results, err := c.queryNoneFresh(context.Background(), stmts, 2*time.Second, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPath != "/db/query" {
		t.Errorf("path = %q; want /db/query", gotPath)
	}
	// Exact param assertions: timings, level=none, freshness=2s, freshness_strict.
	for _, want := range []string{"timings", "level=none", "freshness=2s", "freshness_strict"} {
		if !strings.Contains(gotRawQuery, want) {
			t.Errorf("query %q missing %q", gotRawQuery, want)
		}
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("expected exactly 1 POST (no cross-peer retry), got %d", hits)
	}

	op := rqliteResultToOpResult(results[0])
	if op.Error != "" {
		t.Fatalf("unexpected per-op error: %s", op.Error)
	}
	if len(op.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(op.Rows))
	}
	if op.Rows[0]["name"] != "alice" || op.Rows[1]["id"].(float64) != 2 {
		t.Errorf("rows decoded wrong: %v", op.Rows)
	}
}

func TestQueryNoneFresh_noStrictOmitsParam(t *testing.T) {
	var gotRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"results":[{"columns":[],"types":[],"values":[]}]}`))
	}))
	defer srv.Close()

	c := freshTestClient(srv.URL)
	if _, err := c.queryNoneFresh(context.Background(), []gorqlite.ParameterizedStatement{{Query: "SELECT 1"}}, time.Second, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(gotRawQuery, "freshness_strict") {
		t.Errorf("strict=false must omit freshness_strict; got %q", gotRawQuery)
	}
}

func TestQueryNoneFresh_5xxMapsToFreshnessViolation(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"freshness expired: node has not heard from leader within 2s"}`))
	}))
	defer srv.Close()

	c := freshTestClient(srv.URL)
	results, err := c.queryNoneFresh(context.Background(), []gorqlite.ParameterizedStatement{{Query: "SELECT 1"}}, 2*time.Second, false)
	if err == nil {
		t.Fatal("a 5xx freshness rejection must be a Go error, not a per-op success")
	}
	if results != nil {
		t.Error("results must be nil on a freshness rejection")
	}
	if !errors.Is(err, ErrFreshnessViolation) {
		t.Fatalf("expected ErrFreshnessViolation, got %v", err)
	}
	var fe *FreshnessError
	if !errors.As(err, &fe) {
		t.Fatalf("expected *FreshnessError, got %T", err)
	}
	if fe.Bound != 2*time.Second {
		t.Errorf("FreshnessError.Bound = %v; want 2s", fe.Bound)
	}
	// No cross-peer retry: exactly one POST even on rejection.
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("a freshness rejection must NOT retry another peer; got %d POSTs", hits)
	}
}

func TestQueryNoneFresh_topLevelErrorOn200IsViolation(t *testing.T) {
	// rqlite can return 200 with a top-level "error" — still a freshness failure.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"stale read"}`))
	}))
	defer srv.Close()

	c := freshTestClient(srv.URL)
	_, err := c.queryNoneFresh(context.Background(), []gorqlite.ParameterizedStatement{{Query: "SELECT 1"}}, time.Second, false)
	if !errors.Is(err, ErrFreshnessViolation) {
		t.Fatalf("top-level error must map to ErrFreshnessViolation; got %v", err)
	}
}

func TestQueryNoneFresh_notConfiguredErrors(t *testing.T) {
	c := &client{} // no freshHTTP
	_, err := c.queryNoneFresh(context.Background(), []gorqlite.ParameterizedStatement{{Query: "SELECT 1"}}, time.Second, false)
	if err == nil {
		t.Fatal("queryNoneFresh must error when the native path is not configured")
	}
	if errors.Is(err, ErrFreshnessViolation) {
		t.Error("a config error must NOT masquerade as a freshness violation")
	}
}

// TestQueryNoneFresh_nonFreshnessFailuresAreNotViolations locks in the tightened
// mapping: a 401/400/500 or a non-staleness 200 error must surface as a real
// transport/query error, NOT a stale-node signal (which would make the caller
// silently fall back to the leader and mask the real failure forever).
func TestQueryNoneFresh_nonFreshnessFailuresAreNotViolations(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"401 auth failure", http.StatusUnauthorized, `{"error":"unauthorized"}`},
		{"400 bad SQL", http.StatusBadRequest, `{"error":"near \"slect\": syntax error"}`},
		{"500 internal", http.StatusInternalServerError, `{"error":"internal"}`},
		{"200 with non-staleness error", http.StatusOK, `{"error":"no such table: t"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != http.StatusOK {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := freshTestClient(srv.URL)
			_, err := c.queryNoneFresh(context.Background(), []gorqlite.ParameterizedStatement{{Query: "SELECT 1"}}, time.Second, false)
			if err == nil {
				t.Fatal("expected an error")
			}
			if errors.Is(err, ErrFreshnessViolation) {
				t.Errorf("%s must NOT classify as a freshness violation; got %v", tc.name, err)
			}
		})
	}
}

func TestRqliteResultToOpResult_rowCapEnforced(t *testing.T) {
	// More than MaxBatchQueryRowsPerOp rows must truncate + flag the cap.
	values := make([][]interface{}, MaxBatchQueryRowsPerOp+5)
	for i := range values {
		values[i] = []interface{}{float64(i)}
	}
	r := rqliteQueryResult{Columns: []string{"id"}, Types: []string{"integer"}, Values: values}

	op := rqliteResultToOpResult(r)
	if len(op.Rows) != MaxBatchQueryRowsPerOp {
		t.Errorf("rows = %d; want capped at %d", len(op.Rows), MaxBatchQueryRowsPerOp)
	}
	if !strings.Contains(op.Error, "row cap exceeded") {
		t.Errorf("expected row-cap error, got %q", op.Error)
	}
}

func TestRqliteResultToOpResult_perOpError(t *testing.T) {
	r := rqliteQueryResult{Error: "near \"slect\": syntax error"}
	op := rqliteResultToOpResult(r)
	if op.Error == "" || len(op.Rows) != 0 {
		t.Errorf("an rqlite per-result error must surface as OpResult.Error with no rows; got %+v", op)
	}
}

func TestRowToMap_dateColumnConverted(t *testing.T) {
	r := rqliteQueryResult{
		Columns: []string{"id", "created"},
		Types:   []string{"integer", "datetime"},
		Values:  [][]interface{}{{float64(1), "2024-01-02 03:04:05"}},
	}
	op := rqliteResultToOpResult(r)
	if len(op.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(op.Rows))
	}
	if _, ok := op.Rows[0]["created"].(time.Time); !ok {
		t.Errorf("datetime column must materialize as time.Time; got %T", op.Rows[0]["created"])
	}
}

func TestBuildFreshReadURL_carriesNoCredentials(t *testing.T) {
	// The assembled URL must never embed the basic-auth password (it travels in
	// the Authorization header). Guards the redaction requirement.
	c := &client{freshScheme: "http", freshHost: "10.0.0.5:4001", freshUser: "u", freshPass: "supersecret"}
	got := c.buildFreshReadURL(2*time.Second, true)
	if strings.Contains(got, "supersecret") || strings.Contains(got, "u:") {
		t.Errorf("URL must not contain credentials; got %q", got)
	}
	want := "http://10.0.0.5:4001/db/query?timings&level=none&freshness=2s&freshness_strict"
	if got != want {
		t.Errorf("URL = %q; want %q", got, want)
	}
}

func TestEncodeStatements_positionalArrayShape(t *testing.T) {
	// rqlite wants [["sql", arg1, arg2], ...].
	stmts := []gorqlite.ParameterizedStatement{{Query: "SELECT ?", Arguments: []interface{}{7}}}
	body, err := encodeStatements(stmts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := string(body)
	want := `[["SELECT ?",7]]`
	if got != want {
		t.Errorf("encodeStatements = %s; want %s", got, want)
	}
}
