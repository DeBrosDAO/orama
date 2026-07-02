package rqlite

// freshness_read.go implements a native none+freshness HTTP read (feature
// #1021) that gorqlite's own URL builder cannot express.
//
// gorqlite builds its read URL as only "?timings&level=<level>" and pins
// consistency PER CONNECTION — there is no per-query freshness option. rqlite
// itself supports "level=none&freshness=<dur>": serve from the local node, but
// REJECT the read (HTTP 5xx) when this node is staler than the bound. We POST
// directly to the SINGLE serving node and surface a rejection as a typed error
// so a WASM caller can fall back to a leader-routed read instead of silently
// reading stale data.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/rqlite/gorqlite"
)

const (
	// freshReadPath is the rqlite bulk-query endpoint.
	freshReadPath = "/db/query"
	// Query-param keys for the none+freshness read URL. Named so the assembled
	// URL has no magic strings and stays auditable.
	paramTimings         = "timings"
	paramLevel           = "level"
	paramLevelNone       = "none"
	paramFreshness       = "freshness"
	paramFreshnessStrict = "freshness_strict"
)

// ErrFreshnessViolation is the sentinel returned (wrapped in *FreshnessError)
// when rqlite rejects a none-read because the local node is staler than the
// requested freshness bound. Callers use errors.Is(err, ErrFreshnessViolation)
// to detect it and fall back to a leader-routed read.
var ErrFreshnessViolation = errors.New("rqlite: local read rejected — node staleness exceeds freshness bound")

// FreshnessError carries the bound that was violated and rqlite's detail. It
// unwraps to ErrFreshnessViolation so errors.Is works.
type FreshnessError struct {
	Bound  time.Duration
	Detail string
}

func (e *FreshnessError) Error() string {
	return fmt.Sprintf("rqlite freshness violation (bound=%s): %s", e.Bound, e.Detail)
}

func (e *FreshnessError) Unwrap() error { return ErrFreshnessViolation }

// queryNoneFresh POSTs stmts to the SINGLE local serving node at
// level=none&freshness=<dur>, returning one result per statement. It does NOT
// retry across peers — a freshness rejection on this follower must surface, not
// be masked by a fresher peer. On a freshness rejection it returns a
// *FreshnessError; on any other transport/HTTP failure a wrapped error. The
// password is never included in returned errors.
func (c *client) queryNoneFresh(ctx context.Context, stmts []gorqlite.ParameterizedStatement, freshness time.Duration, strict bool) ([]rqliteQueryResult, error) {
	if c.freshHTTP == nil {
		return nil, fmt.Errorf("rqlite.queryNoneFresh: native none+freshness path not configured (use NewClientWithDSN)")
	}
	body, err := encodeStatements(stmts)
	if err != nil {
		return nil, fmt.Errorf("rqlite.queryNoneFresh: encode statements: %w", err)
	}
	reqURL := c.buildFreshReadURL(freshness, strict)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		// reqURL carries no credentials (basic-auth is set via SetBasicAuth), so
		// it is safe to omit here; we still avoid echoing it to be conservative.
		return nil, fmt.Errorf("rqlite.queryNoneFresh: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.freshUser != "" || c.freshPass != "" {
		req.SetBasicAuth(c.freshUser, c.freshPass)
	}
	resp, err := c.freshHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rqlite.queryNoneFresh: POST to serving node failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("rqlite.queryNoneFresh: read response: %w", err)
	}
	return parseFreshReadResponse(resp.StatusCode, respBody, freshness)
}

// buildFreshReadURL assembles the none+freshness read URL WITHOUT credentials
// (basic-auth travels in the header, not the URL) so it is safe to log. The
// freshness value is URL-escaped: time.Duration.String() emits a non-ASCII "µs"
// for sub-millisecond bounds, which must never reach the raw request target.
func (c *client) buildFreshReadURL(freshness time.Duration, strict bool) string {
	// Path order is fixed so tests can assert it exactly:
	//   <scheme>://<host>/db/query?timings&level=none&freshness=<dur>[&freshness_strict]
	reqURL := fmt.Sprintf("%s://%s%s?%s&%s=%s&%s=%s",
		c.freshScheme, c.freshHost, freshReadPath,
		paramTimings,
		paramLevel, paramLevelNone,
		paramFreshness, neturl.QueryEscape(freshness.String()))
	if strict {
		reqURL += "&" + paramFreshnessStrict
	}
	return reqURL
}

// encodeStatements marshals parameterized statements into rqlite's positional
// JSON array shape: [["sql", arg1, arg2], ...]. Mirrors gorqlite's own request
// encoding (api.go rqliteApiPost).
func encodeStatements(stmts []gorqlite.ParameterizedStatement) ([]byte, error) {
	formatted := make([][]interface{}, 0, len(stmts))
	for _, s := range stmts {
		row := make([]interface{}, 0, len(s.Arguments)+1)
		row = append(row, s.Query)
		row = append(row, s.Arguments...)
		formatted = append(formatted, row)
	}
	return json.Marshal(formatted)
}

// rqliteQueryResult is the decoded shape of one entry in rqlite's "results"
// array. We decode into our own type because gorqlite.QueryResult has
// unexported fields and its Map()/Next() panic without an internal *Connection,
// so it cannot be constructed outside the gorqlite package.
type rqliteQueryResult struct {
	Columns []string        `json:"columns"`
	Types   []string        `json:"types"`
	Values  [][]interface{} `json:"values"`
	Error   string          `json:"error"`
}

// freshReadEnvelope is rqlite's /db/query response: a top-level "error" (api
// failure) and/or a "results" array (one per statement).
type freshReadEnvelope struct {
	Results []rqliteQueryResult `json:"results"`
	Error   string              `json:"error"`
}

// parseFreshReadResponse decodes an rqlite /db/query response. rqlite returns
// HTTP 503 specifically when a none-read is rejected because the node is staler
// than the freshness bound — ONLY that (and a 200 body whose top-level error
// names staleness/freshness) maps to a *FreshnessError. Any other non-200 (401
// auth, 400 bad SQL, 500 internal) is a genuine transport/server error and must
// NOT masquerade as "stale" — otherwise a caller would silently fall back to a
// leader read and mask the real failure forever.
func parseFreshReadResponse(statusCode int, body []byte, freshness time.Duration) ([]rqliteQueryResult, error) {
	if statusCode == http.StatusServiceUnavailable {
		return nil, &FreshnessError{Bound: freshness, Detail: freshReadDetail(statusCode, body)}
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("rqlite.queryNoneFresh: unexpected status %d: %s", statusCode, freshReadDetail(statusCode, body))
	}
	var env freshReadEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("rqlite.queryNoneFresh: decode response: %w", err)
	}
	if env.Error != "" {
		if isFreshnessRejection(env.Error) {
			return nil, &FreshnessError{Bound: freshness, Detail: env.Error}
		}
		return nil, fmt.Errorf("rqlite.queryNoneFresh: query error: %s", env.Error)
	}
	return env.Results, nil
}

// isFreshnessRejection reports whether an rqlite error string is a
// bounded-staleness rejection (vs an unrelated query/server error). rqlite
// phrases this as "stale read" / a "freshness" message; match defensively so a
// genuine SQL error is never mislabeled as a stale-node signal.
func isFreshnessRejection(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "stale") || strings.Contains(m, "freshness")
}

// freshReadDetail extracts a short, credential-free detail from a non-200
// rqlite response for the FreshnessError. Prefers the JSON "error" field when
// present, else a truncated raw body.
func freshReadDetail(statusCode int, body []byte) string {
	var env freshReadEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Error != "" {
		return fmt.Sprintf("status %d: %s", statusCode, env.Error)
	}
	const maxDetail = 256
	raw := bytes.TrimSpace(body)
	if len(raw) > maxDetail {
		raw = raw[:maxDetail]
	}
	return fmt.Sprintf("status %d: %s", statusCode, string(raw))
}

// rqliteResultToOpResult converts one decoded rqlite result into our OpResult
// wire shape, enforcing MaxBatchQueryRowsPerOp exactly like
// queryResultToOpResult does for the gorqlite path. date/datetime columns are
// converted to time.Time to match gorqlite's Map() output.
func rqliteResultToOpResult(r rqliteQueryResult) OpResult {
	if r.Error != "" {
		return OpResult{Kind: BatchOpQuery, Error: r.Error}
	}
	var rows []map[string]interface{}
	for _, vals := range r.Values {
		if len(rows) >= MaxBatchQueryRowsPerOp {
			return OpResult{
				Kind: BatchOpQuery,
				Rows: rows,
				Error: fmt.Sprintf("rqlite.BatchQueryFresh: row cap exceeded (%d) — paginate via LIMIT/OFFSET",
					MaxBatchQueryRowsPerOp),
			}
		}
		row, err := rowToMap(r.Columns, r.Types, vals)
		if err != nil {
			return OpResult{Kind: BatchOpQuery, Rows: rows, Error: "rqlite.BatchQueryFresh: row map: " + err.Error()}
		}
		rows = append(rows, row)
	}
	return OpResult{Kind: BatchOpQuery, Rows: rows}
}

// rowToMap pairs a value row with its columns/types, mirroring gorqlite's
// QueryResult.Map(): date/datetime columns are parsed to time.Time, everything
// else passes through as the raw JSON value.
func rowToMap(columns, types []string, vals []interface{}) (map[string]interface{}, error) {
	ans := make(map[string]interface{}, len(columns))
	for i := 0; i < len(columns); i++ {
		var v interface{}
		if i < len(vals) {
			v = vals[i]
		}
		if i < len(types) && (types[i] == "date" || types[i] == "datetime") && v != nil {
			t, err := rqliteToTime(v)
			if err != nil {
				return nil, err
			}
			ans[columns[i]] = t
			continue
		}
		ans[columns[i]] = v
	}
	return ans, nil
}

// rqliteToTime mirrors gorqlite's toTime: parse a date/datetime cell into a
// time.Time from string, float64 (unix seconds), or int64 (unix seconds).
func rqliteToTime(src interface{}) (time.Time, error) {
	switch s := src.(type) {
	case string:
		const layout = "2006-01-02 15:04:05"
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
		return time.Parse(time.RFC3339, s)
	case float64:
		return time.Unix(int64(s), 0), nil
	case int64:
		return time.Unix(s, 0), nil
	}
	return time.Time{}, fmt.Errorf("invalid time type %T val %v", src, src)
}
