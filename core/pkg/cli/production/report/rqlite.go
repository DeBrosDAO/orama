package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"io"
	"net/http"
	"strconv"
	"time"
)

var rqliteBase = constants.LocalRQLiteURL()

// collectRQLite queries the local RQLite HTTP API to build a health report.
func collectRQLite() *RQLiteReport {
	r := &RQLiteReport{}

	// 1. GET /status — core Raft and node metadata.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	statusBody, err := httpGet(ctx, rqliteBase+"/status")
	if err != nil {
		r.Responsive = false
		return r
	}

	var status map[string]interface{}
	if err := json.Unmarshal(statusBody, &status); err != nil {
		r.Responsive = false
		return r
	}
	r.Responsive = true

	// Extract fields from the nested status JSON.
	r.RaftState = getNestedString(status, "store", "raft", "state")
	r.LeaderAddr = getNestedString(status, "store", "leader", "addr")
	r.LeaderID = getNestedString(status, "store", "leader", "node_id")
	r.NodeID = getNestedString(status, "store", "node_id")
	r.Term = uint64(getNestedFloat(status, "store", "raft", "current_term"))
	r.Applied = uint64(getNestedFloat(status, "store", "raft", "applied_index"))
	r.Commit = uint64(getNestedFloat(status, "store", "raft", "commit_index"))
	r.FsmPending = uint64(getNestedFloat(status, "store", "raft", "fsm_pending"))
	r.LastContact = getNestedString(status, "store", "raft", "last_contact")
	r.Voter = getNestedBool(status, "store", "raft", "voter")
	r.DBSize = getNestedString(status, "store", "sqlite3", "db_size_friendly")
	r.Uptime = getNestedString(status, "http", "uptime")
	r.Version = getNestedString(status, "build", "version")
	r.Goroutines = int(getNestedFloat(status, "runtime", "num_goroutine"))

	// HeapMB: bytes → MB.
	heapBytes := getNestedFloat(status, "runtime", "memory", "heap_alloc")
	if heapBytes > 0 {
		r.HeapMB = int(heapBytes / (1024 * 1024))
	}

	// NumPeers may be a number or a string in the JSON; handle both.
	r.NumPeers = getNestedInt(status, "store", "raft", "num_peers")

	// 2. GET /nodes?nonvoters — cluster node list.
	{
		ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel2()

		if body, err := httpGet(ctx2, rqliteBase+"/nodes?nonvoters"); err == nil {
			var rawNodes map[string]struct {
				Addr      string  `json:"addr"`
				Reachable bool    `json:"reachable"`
				Leader    bool    `json:"leader"`
				Voter     bool    `json:"voter"`
				Time      float64 `json:"time"`
				Error     string  `json:"error"`
			}
			if err := json.Unmarshal(body, &rawNodes); err == nil {
				r.Nodes = make(map[string]RQLiteNodeInfo, len(rawNodes))
				for id, n := range rawNodes {
					r.Nodes[id] = RQLiteNodeInfo{
						Reachable: n.Reachable,
						Leader:    n.Leader,
						Voter:     n.Voter,
						TimeMS:    n.Time * 1000, // seconds → milliseconds
						Error:     n.Error,
					}
				}
			}
		}
	}

	// 3. GET /readyz — readiness probe.
	{
		ctx3, cancel3 := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel3()

		req, err := http.NewRequestWithContext(ctx3, http.MethodGet, rqliteBase+"/readyz", nil)
		if err == nil {
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
				r.Ready = resp.StatusCode == http.StatusOK
			}
		}
	}

	// 4. POST /db/query?level=strong — strong read test.
	{
		ctx4, cancel4 := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel4()

		payload := []byte(`["SELECT 1"]`)
		req, err := http.NewRequestWithContext(ctx4, http.MethodPost, rqliteBase+"/db/query?level=strong", bytes.NewReader(payload))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			if resp, err := http.DefaultClient.Do(req); err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				r.StrongRead = resp.StatusCode == http.StatusOK
			}
		}
	}

	// 5. GET /debug/vars — error counters.
	{
		ctx5, cancel5 := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel5()

		if body, err := httpGet(ctx5, rqliteBase+"/debug/vars"); err == nil {
			var vars map[string]interface{}
			if err := json.Unmarshal(body, &vars); err == nil {
				r.DebugVars = &RQLiteDebugVarsReport{
					QueryErrors:      jsonUint64(vars, "api_query_errors"),
					ExecuteErrors:    jsonUint64(vars, "api_execute_errors"),
					RemoteExecErrors: jsonUint64(vars, "api_remote_exec_errors"),
					LeaderNotFound:   jsonUint64(vars, "store_leader_not_found"),
					SnapshotErrors:   jsonUint64(vars, "snapshot_errors"),
					ClientRetries:    jsonUint64(vars, "client_retries"),
					ClientTimeouts:   jsonUint64(vars, "client_timeouts"),
				}
			}
		}
	}

	return r
}

// ---------------------------------------------------------------------------
// Nested-map extraction helpers
// ---------------------------------------------------------------------------

// getNestedString traverses nested map[string]interface{} values and returns
// the final value as a string. Returns "" if any key is missing or the leaf
// is not a string.
func getNestedString(m map[string]interface{}, keys ...string) string {
	v := getNestedValue(m, keys...)
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// getNestedFloat traverses nested maps and returns the leaf as a float64.
// JSON numbers are decoded as float64 by encoding/json into interface{}.
func getNestedFloat(m map[string]interface{}, keys ...string) float64 {
	v := getNestedValue(m, keys...)
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f
		}
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f
		}
	}
	return 0
}

// getNestedBool traverses nested maps and returns the leaf as a bool.
func getNestedBool(m map[string]interface{}, keys ...string) bool {
	v := getNestedValue(m, keys...)
	if v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// getNestedInt traverses nested maps and returns the leaf as an int.
// Handles both numeric and string representations (RQLite sometimes
// returns num_peers as a string).
func getNestedInt(m map[string]interface{}, keys ...string) int {
	v := getNestedValue(m, keys...)
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i
		}
	}
	return 0
}

// getNestedValue walks through nested map[string]interface{} following the
// given key path and returns the leaf value, or nil if any step fails.
func getNestedValue(m map[string]interface{}, keys ...string) interface{} {
	if len(keys) == 0 {
		return nil
	}
	current := interface{}(m)
	for _, key := range keys {
		cm, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current, ok = cm[key]
		if !ok {
			return nil
		}
	}
	return current
}

// jsonUint64 reads a top-level key from a flat map as uint64.
func jsonUint64(m map[string]interface{}, key string) uint64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return uint64(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return uint64(i)
		}
	case string:
		if i, err := strconv.ParseUint(n, 10, 64); err == nil {
			return i
		}
	}
	return 0
}
