//go:build e2e_fleet

package serverless

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	logBudget   = 2 * time.Minute
	cacheBudget = 30 * time.Second
	// Batch limits (docs/SERVERLESS.md#database-transactions).
	maxStatements = 100
	maxRows       = 10000
	codeTooMany   = "TOO_MANY_STATEMENTS"
	codeTooLarge  = "PAYLOAD_TOO_LARGE"
	codeConstrain = "CONSTRAINT_VIOLATION"
)

// waitLog polls `orama function logs` until it shows marker (the invocation
// log is written asynchronously; docs/SERVERLESS.md#invocation-logging).
func waitLog(t *testing.T, fx *fixture, fn, marker string) {
	t.Helper()
	eventually.Require(t, pollEvery, logBudget, "log line "+marker, func() (bool, error) {
		res, err := fx.n.CLI.For(t).Run(t.Context(), "function", "logs", fn, "--limit", "20")
		if err != nil {
			return false, err
		}
		if res.Exit == 0 && strings.Contains(res.Stdout, marker) {
			return true, nil
		}
		return false, fmt.Errorf("exit %d", res.Exit)
	})
}

// TestHostHTTPFetch_ssrfMatrix: http_fetch refuses loopback, RFC 1918,
// link-local, CGNAT, unspecified and the IPv6 forms wrapping IPv4, names that
// resolve to them, and redirects to them; a public destination works
// (docs/SECURITY.md#tenant-isolation: WASM http_fetch).
func TestHostHTTPFetch_ssrfMatrix(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	const fn = "e2e-fetch"
	deploy(t, fx, fnSpec{name: fn, yaml: "timeout: 60\n"})
	if res := sub(call(t, fx, fn, map[string]any{"op": "fetch", "url": "https://example.com/"}), "result"); res["status"] != float64(http.StatusOK) {
		t.Fatalf("control: fetching https://example.com/ returned %v", res)
	}
	wg := fx.f.State.Nodes[0].WGIP
	for _, u := range []string{
		"http://127.0.0.1:10104/v1/health", "http://localhost:10104/", "http://" + wg + ":10104/v1/health",
		"http://10.0.0.1/", "http://172.16.0.1/", "http://192.168.1.1/", "http://169.254.169.254/latest/meta-data/",
		"http://100.64.0.1/", "http://0.0.0.0:10104/", "http://[::1]:10104/", "http://[::ffff:127.0.0.1]:10104/",
		"http://[64:ff9b::7f00:1]:10104/", "http://[2002:7f00:1::1]:10104/", "http://metadata.google.internal/",
		"http://127.0.0.1.nip.io:10104/", "http://localtest.me:10104/", "http://2130706433:10104/", "http://0x7f000001:10104/",
		"file:///etc/passwd", "gopher://127.0.0.1:10104/",
	} {
		res := sub(call(t, fx, fn, map[string]any{"op": "fetch", "url": u}), "result")
		if res != nil && res["status"] != float64(0) {
			t.Errorf("http_fetch %s was not refused: status %v", u, res["status"])
		}
	}
	redirect := "https://httpbin.org/redirect-to?url=http%3A%2F%2F127.0.0.1%3A10104%2Fv1%2Fhealth"
	control := sub(call(t, fx, fn, map[string]any{"op": "fetch", "url": "https://httpbin.org/status/204"}), "result")
	if control["status"] != float64(http.StatusNoContent) {
		t.Logf("httpbin.org unreachable from the node (%v); the redirect case is not judged", control)
		return
	}
	if res := sub(call(t, fx, fn, map[string]any{"op": "fetch", "url": redirect}), "result"); res["status"] == float64(http.StatusOK) {
		t.Errorf("a redirect to loopback was followed: %v", res)
	}
}

// TestHostCache_atomicsAndTTL: cache_incr is atomic under concurrent
// invocations; set/get/delete round-trip; a TTL expires; a negative TTL is
// refused (docs/SERVERLESS.md#cache-olric-distributed-cache).
//
// Every increment's reply is checked. A call the edge refused (a 503 from an
// open circuit) never ran, so it is reported as a refusal and not left to show
// up as a counter that "lost" an increment; the calls that did run must return
// 1..n each exactly once, which is what atomic means.
func TestHostCache_atomicsAndTTL(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	const fn, workers = "e2e-cache", 20
	deploy(t, fx, fnSpec{name: fn})
	key := "e2e-counter-" + fx.n.Name
	replies := make([]*gw.Response, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int, c *gw.Client) {
			defer wg.Done()
			replies[i], errs[i] = c.Send(t.Context(), gw.Req{Method: http.MethodPost, Path: "/v1/functions/" + fn + "/invoke", Bearer: fx.admin,
				Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"op":"cache_incr","key":"` + key + `","delta":1}`)})
		}(i, fx.c.PinTo(fx.f.State.Nodes[i%len(fx.f.State.Nodes)].PublicIP))
	}
	wg.Wait()
	ran := incrementsThatRan(t, replies, errs)
	if !isCounting(ran) {
		t.Errorf("the %d increments that ran returned %v, want 1..%d each once (a repeated or skipped value is a lost update)", len(ran), ran, len(ran))
	}
	if got := call(t, fx, fn, map[string]any{"op": "cache_incr", "key": key, "delta": 0})["value"]; got != float64(len(ran)) {
		t.Errorf("%d concurrent increments across the gateways left %v after %d of them ran", workers, got, len(ran))
	}
	call(t, fx, fn, map[string]any{"op": "cache_set", "key": "k", "value": "v-ü", "ttl": 0})
	if got := call(t, fx, fn, map[string]any{"op": "cache_get", "key": "k"})["value"]; got != "v-ü" {
		t.Errorf("cache_get after set: %v", got)
	}
	if got := call(t, fx, fn, map[string]any{"op": "cache_delete", "key": "k"})["deleted"]; got != float64(1) {
		t.Errorf("cache_delete returned %v", got)
	}
	if got := call(t, fx, fn, map[string]any{"op": "cache_delete", "key": "never-set"})["deleted"]; got != float64(1) {
		t.Errorf("cache_delete of a missing key returned %v, want 1 (gone afterwards)", got)
	}
	call(t, fx, fn, map[string]any{"op": "cache_set", "key": "neg", "value": "x", "ttl": -1})
	if got := call(t, fx, fn, map[string]any{"op": "cache_get", "key": "neg"})["value"]; got != "" {
		t.Errorf("a negative TTL was stored: %v", got)
	}
	call(t, fx, fn, map[string]any{"op": "cache_set", "key": "short", "value": "x", "ttl": 2})
	eventually.Require(t, time.Second, cacheBudget, "a 2s entry to expire", func() (bool, error) {
		return call(t, fx, fn, map[string]any{"op": "cache_get", "key": "short"})["value"] == "", nil
	})
}

// incrementsThatRan reports every increment that did not get a 200 and returns,
// sorted, the values of those that did.
func incrementsThatRan(t *testing.T, replies []*gw.Response, errs []error) []int {
	t.Helper()
	var ran []int
	for i := range replies {
		if errs[i] != nil {
			t.Errorf("increment %d was not sent: %v", i, errs[i])
			continue
		}
		if replies[i].Status != http.StatusOK {
			t.Errorf("increment %d was refused, so it never ran: status %d %s", i, replies[i].Status, replies[i].Body)
			continue
		}
		var out struct {
			Value int `json:"value"`
		}
		if err := json.Unmarshal(replies[i].Body, &out); err != nil {
			t.Errorf("increment %d: undecodable reply %q: %v", i, replies[i].Body, err)
			continue
		}
		ran = append(ran, out.Value)
	}
	sort.Ints(ran)
	return ran
}

// isCounting reports whether sorted is exactly 1..len(sorted).
func isCounting(sorted []int) bool {
	for i, v := range sorted {
		if v != i+1 {
			return false
		}
	}
	return true
}

// TestHostDB_guardRefusals: a function's SQL may not ATTACH, PRAGMA, VACUUM,
// CREATE TRIGGER, read sqlite_dbpage/dbstat, run two statements, or name a
// platform table in any quoting or role (docs/SERVERLESS.md#database-rqlite).
func TestHostDB_guardRefusals(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	const fn = "e2e-dbguard"
	deploy(t, fx, fnSpec{name: fn})
	// A refused statement is a failed host call: db_query_v2 and db_execute_v2
	// return 0, which the function sees as no result at all (null), where an
	// accepted statement always gives a result object (engine.go hDBQueryV2).
	ok := call(t, fx, fn, map[string]any{"op": "db_exec", "sql": "CREATE TABLE IF NOT EXISTS e2e_t (id INTEGER PRIMARY KEY, v TEXT UNIQUE)"})
	if hostCallRefused(ok) {
		t.Fatalf("control DDL refused: %v", ok)
	}
	bound := call(t, fx, fn, map[string]any{"op": "db_exec", "sql": "INSERT INTO e2e_t (v) VALUES (?)", "args": []any{"grants"}})
	if hostCallRefused(bound) {
		t.Errorf("a reserved name as a bound value was refused: %v", bound)
	}
	for _, sql := range []string{
		"ATTACH DATABASE '/tmp/e2e.db' AS x", "DETACH DATABASE main", "PRAGMA table_info(e2e_t)", "VACUUM",
		"CREATE TRIGGER e2e_tr AFTER INSERT ON e2e_t BEGIN SELECT 1; END", "SELECT * FROM sqlite_dbpage", "SELECT * FROM dbstat",
		"SELECT 1; SELECT 2", "SELECT * FROM api_keys", `SELECT * FROM "api_keys"`, "SELECT * FROM [function_secrets]",
		"SELECT * FROM `grants`", "SELECT * FROM main.namespaces", "SELECT 1 AS api_keys", "SELECT :grants",
		"SELECT * FROM e2e_t WHERE v = 'grants'", "INSERT INTO e2e_t (v) VALUES ('ipfs_content_ownership')",
		"SELECT * /* x */ FROM push_topics", "SELECT * FROM API_KEYS", "DELETE FROM namespace_quotas",
	} {
		for _, op := range []string{"db_query", "db_exec"} {
			res := call(t, fx, fn, map[string]any{"op": op, "sql": sql})
			if !hostCallRefused(res) {
				t.Errorf("%s %q was not refused: %v", op, sql, res)
			}
		}
	}
}

// hostCallRefused reports whether a database host call's result says it was
// refused: no result at all (null, the host returned 0), or an error string.
func hostCallRefused(res map[string]any) bool {
	if res == nil {
		return true
	}
	e, _ := res["error"].(string)
	return e != ""
}

// TestHostDB_batchLimitsAndTransactions: 100 statements commit and 101 fail
// the whole batch (TOO_MANY_STATEMENTS); a constraint failure rolls every
// exec back and is reported on its op; a query after the execs sees them;
// the 10,000-row cap truncates with PAYLOAD_TOO_LARGE.
func TestHostDB_batchLimitsAndTransactions(t *testing.T) {
	t.Parallel()
	fx := setup(t)
	const fn = "e2e-batch"
	deploy(t, fx, fnSpec{name: fn, yaml: "memory: 256\ntimeout: 60\n"})
	call(t, fx, fn, map[string]any{"op": "db_exec", "sql": "CREATE TABLE IF NOT EXISTS e2e_b (id INTEGER PRIMARY KEY, v TEXT UNIQUE)"})
	if res := call(t, fx, fn, map[string]any{"op": "db_tx", "ops": inserts(maxStatements, "ok")}); res["committed"] != true {
		t.Errorf("100 statements: %v", trim(res))
	}
	if res := call(t, fx, fn, map[string]any{"op": "db_tx", "ops": inserts(maxStatements+1, "over")}); res["code"] != codeTooMany || res["committed"] == true {
		t.Errorf("101 statements: want %s, got %v", codeTooMany, trim(res))
	}
	dup := map[string]any{"ops": []map[string]any{
		{"kind": "exec", "sql": "INSERT INTO e2e_b (v) VALUES (?)", "args": []any{"dup"}},
		{"kind": "exec", "sql": "INSERT INTO e2e_b (v) VALUES (?)", "args": []any{"dup"}},
	}}
	res := call(t, fx, fn, map[string]any{"op": "db_tx", "ops": dup})
	results, _ := res["results"].([]any)
	if res["committed"] != false || res["failed_index"] != float64(1) || len(results) < 2 || sub(map[string]any{"r": results[1]}, "r")["code"] != codeConstrain {
		t.Errorf("duplicate insert: %v", trim(res))
	}
	rows := call(t, fx, fn, map[string]any{"op": "db_query", "sql": "SELECT COUNT(*) AS n FROM e2e_b WHERE v = ?", "args": []any{"dup"}})
	if !strings.Contains(fmt.Sprint(rows["rows"]), "n:0") {
		t.Errorf("a rolled-back insert is visible: %v", rows)
	}
	seen := map[string]any{"ops": []map[string]any{
		{"kind": "exec", "sql": "INSERT INTO e2e_b (v) VALUES (?)", "args": []any{"seen"}},
		{"kind": "query", "sql": "SELECT v FROM e2e_b WHERE v = ?", "args": []any{"seen"}},
	}}
	if res := call(t, fx, fn, map[string]any{"op": "db_tx", "ops": seen}); !strings.Contains(fmt.Sprint(res["results"]), "seen") {
		t.Errorf("a query after the exec does not see it: %v", trim(res))
	}
	rowCap(t, fx, fn)
}

func rowCap(t *testing.T, fx *fixture, fn string) {
	t.Helper()
	fill := fmt.Sprintf("WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < %d) INSERT INTO e2e_rows (x) SELECT x FROM c", maxRows+1)
	call(t, fx, fn, map[string]any{"op": "db_exec", "sql": "CREATE TABLE IF NOT EXISTS e2e_rows (x INTEGER)"})
	call(t, fx, fn, map[string]any{"op": "db_exec", "sql": fill})
	q := map[string]any{"ops": []map[string]any{{"kind": "query", "sql": "SELECT x FROM e2e_rows"}}}
	res := call(t, fx, fn, map[string]any{"op": "db_batch_sum", "ops": q})
	ops, _ := res["ops"].([]any)
	if len(ops) != 1 || !strings.Contains(fmt.Sprint(ops[0]), codeTooLarge) || !strings.Contains(fmt.Sprint(ops[0]), fmt.Sprint(maxRows)) {
		t.Errorf("a %d-row query: want %d rows and %s, got %v", maxRows+1, maxRows, codeTooLarge, res)
	}
	over := map[string]any{"ops": make([]map[string]any, 0, maxStatements+1)}
	for i := 0; i <= maxStatements; i++ {
		over["ops"] = append(over["ops"].([]map[string]any), map[string]any{"kind": "query", "sql": "SELECT 1"})
	}
	if res := call(t, fx, fn, map[string]any{"op": "db_batch_sum", "ops": over}); res["code"] != codeTooMany {
		t.Errorf("a 101-query batch: want %s, got %v", codeTooMany, res)
	}
}

func inserts(n int, prefix string) map[string]any {
	ops := make([]map[string]any, n)
	for i := range ops {
		ops[i] = map[string]any{"kind": "exec", "sql": "INSERT INTO e2e_b (v) VALUES (?)", "args": []any{fmt.Sprintf("%s-%d", prefix, i)}}
	}
	return map[string]any{"ops": ops}
}

// trim keeps a batch result readable in a failure message.
func trim(m map[string]any) string {
	s := fmt.Sprint(map[string]any{"committed": m["committed"], "failed_index": m["failed_index"], "error": m["error"], "code": m["code"]})
	return s
}
