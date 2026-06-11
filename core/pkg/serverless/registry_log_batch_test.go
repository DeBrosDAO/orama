package serverless

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// recordingExecClient is an rqlite.Client that records every Exec call. It
// embeds the interface so we only override Exec; calling any other method is a
// test bug (will nil-panic), which is what we want — Log must only Exec.
type recordingExecClient struct {
	rqlite.Client
	mu    sync.Mutex
	execs []recordedExec
}

type recordedExec struct {
	query string
	args  []interface{}
}

func (c *recordingExecClient) Exec(_ context.Context, query string, args ...any) (sql.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.execs = append(c.execs, recordedExec{query: query, args: args})
	return &recordingResult{}, nil
}

type recordingResult struct{}

func (recordingResult) LastInsertId() (int64, error) { return 0, nil }
func (recordingResult) RowsAffected() (int64, error) { return 1, nil }

func TestBuildFunctionLogsInsert_multi_row_shape(t *testing.T) {
	ts := time.Unix(1700000000, 0).UTC()
	entries := []LogEntry{
		{Level: "info", Message: "a", Timestamp: ts},
		{Level: "error", Message: "b", Timestamp: ts},
	}
	query, args := buildFunctionLogsInsert("fn-1", "inv-1", entries)

	wantPrefix := "INSERT INTO function_logs (id, function_id, invocation_id, level, message, timestamp) VALUES "
	if !strings.HasPrefix(query, wantPrefix) {
		t.Fatalf("unexpected query prefix: %q", query)
	}
	if got, want := strings.Count(query, "(?, ?, ?, ?, ?, ?)"), 2; got != want {
		t.Errorf("expected %d value tuples, got %d in %q", want, got, query)
	}
	if got, want := len(args), 2*6; got != want {
		t.Fatalf("expected %d args, got %d", want, got)
	}
	// Row 0: id (generated), function_id, invocation_id, level, message, timestamp.
	if args[1] != "fn-1" || args[2] != "inv-1" || args[3] != "info" || args[4] != "a" || args[5] != ts {
		t.Errorf("row 0 args wrong: %#v", args[0:6])
	}
	if args[7] != "fn-1" || args[8] != "inv-1" || args[9] != "error" || args[10] != "b" || args[11] != ts {
		t.Errorf("row 1 args wrong: %#v", args[6:12])
	}
	// Generated IDs must be present and distinct.
	if args[0] == "" || args[6] == "" || args[0] == args[6] {
		t.Errorf("expected distinct non-empty generated IDs, got %v and %v", args[0], args[6])
	}
}

func TestChunkLogEntries(t *testing.T) {
	if got := chunkLogEntries(nil, 100); got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
	entries := make([]LogEntry, 250)
	chunks := chunkLogEntries(entries, 100)
	if len(chunks) != 3 {
		t.Fatalf("expected ceil(250/100)=3 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 100 || len(chunks[1]) != 100 || len(chunks[2]) != 50 {
		t.Errorf("unexpected chunk sizes: %d %d %d", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
}

func TestRegistryLog_batches_logs_into_ceil_div_exec_calls(t *testing.T) {
	db := &recordingExecClient{}
	r := NewRegistry(db, nil, RegistryConfig{}, zap.NewNop())

	// 5 log lines should collapse to: 1 invocation INSERT + 1 logs INSERT = 2 Execs.
	logs := make([]LogEntry, 5)
	for i := range logs {
		logs[i] = LogEntry{Level: "info", Message: "x", Timestamp: time.Now()}
	}
	inv := &InvocationRecord{ID: "inv-1", FunctionID: "fn-1", Logs: logs}

	if err := r.Log(context.Background(), inv); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.execs) != 2 {
		t.Fatalf("expected 2 Exec calls (1 invocation + 1 batched logs), got %d", len(db.execs))
	}
	if !strings.HasPrefix(db.execs[0].query, "\n\t\tINSERT INTO function_invocations") &&
		!strings.Contains(db.execs[0].query, "function_invocations") {
		t.Errorf("first Exec should be the invocation insert, got %q", db.execs[0].query)
	}
	if !strings.Contains(db.execs[1].query, "INSERT INTO function_logs") {
		t.Errorf("second Exec should be the batched logs insert, got %q", db.execs[1].query)
	}
	if got := strings.Count(db.execs[1].query, "(?, ?, ?, ?, ?, ?)"); got != 5 {
		t.Errorf("expected 5 value tuples in the batched logs insert, got %d", got)
	}
}

func TestRegistryLog_chunks_logs_over_cap(t *testing.T) {
	db := &recordingExecClient{}
	r := NewRegistry(db, nil, RegistryConfig{}, zap.NewNop())

	// maxLogRowsPerInsert+1 lines => ceil((cap+1)/cap)=2 logs INSERTs, plus
	// the single invocation INSERT = 3 Execs total.
	n := maxLogRowsPerInsert + 1
	logs := make([]LogEntry, n)
	for i := range logs {
		logs[i] = LogEntry{Level: "info", Message: "x", Timestamp: time.Now()}
	}
	inv := &InvocationRecord{ID: "inv-1", FunctionID: "fn-1", Logs: logs}

	if err := r.Log(context.Background(), inv); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.execs) != 3 {
		t.Fatalf("expected 3 Exec calls (1 invocation + 2 chunked logs), got %d", len(db.execs))
	}
}

func TestRegistryLog_no_logs_single_exec(t *testing.T) {
	db := &recordingExecClient{}
	r := NewRegistry(db, nil, RegistryConfig{}, zap.NewNop())

	if err := r.Log(context.Background(), &InvocationRecord{ID: "inv-1", FunctionID: "fn-1"}); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.execs) != 1 {
		t.Fatalf("expected only the invocation Exec, got %d", len(db.execs))
	}
}
