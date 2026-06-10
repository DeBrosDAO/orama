package registry

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

// recordingExecDB records Exec calls. It embeds rqlite.Client so only Exec is
// implemented — Log must not call any other method.
type recordingExecDB struct {
	rqlite.Client
	mu    sync.Mutex
	execs []recordedExec
}

type recordedExec struct {
	query string
	args  []interface{}
}

func (d *recordingExecDB) Exec(_ context.Context, query string, args ...any) (sql.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.execs = append(d.execs, recordedExec{query: query, args: args})
	return recordingResult{}, nil
}

type recordingResult struct{}

func (recordingResult) LastInsertId() (int64, error) { return 0, nil }
func (recordingResult) RowsAffected() (int64, error) { return 1, nil }

func TestBuildFunctionLogsInsert_shape(t *testing.T) {
	ts := time.Unix(1700000000, 0).UTC()
	entries := []LogData{
		{Level: "info", Message: "a", Timestamp: ts},
		{Level: "error", Message: "b", Timestamp: ts},
	}
	query, args := buildFunctionLogsInsert("fn-1", "inv-1", entries)

	wantPrefix := "INSERT INTO function_logs (id, function_id, invocation_id, level, message, timestamp) VALUES "
	if !strings.HasPrefix(query, wantPrefix) {
		t.Fatalf("unexpected query prefix: %q", query)
	}
	if got, want := strings.Count(query, "(?, ?, ?, ?, ?, ?)"), 2; got != want {
		t.Errorf("expected %d value tuples, got %d", want, got)
	}
	if got, want := len(args), 12; got != want {
		t.Fatalf("expected %d args, got %d", want, got)
	}
	if args[1] != "fn-1" || args[2] != "inv-1" || args[3] != "info" || args[4] != "a" || args[5] != ts {
		t.Errorf("row 0 args wrong: %#v", args[0:6])
	}
	if args[9] != "error" || args[10] != "b" {
		t.Errorf("row 1 args wrong: %#v", args[6:12])
	}
}

func TestChunkLogData(t *testing.T) {
	if got := chunkLogData(nil, 100); got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
	entries := make([]LogData, 250)
	chunks := chunkLogData(entries, 100)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if len(chunks[2]) != 50 {
		t.Errorf("expected last chunk of 50, got %d", len(chunks[2]))
	}
}

func TestInvocationLoggerLog_batches_logs(t *testing.T) {
	db := &recordingExecDB{}
	il := NewInvocationLogger(db, zap.NewNop())

	logs := make([]LogData, 5)
	for i := range logs {
		logs[i] = LogData{Level: "info", Message: "x", Timestamp: time.Now()}
	}
	inv := &InvocationRecordData{ID: "inv-1", FunctionID: "fn-1", Logs: logs}

	if err := il.Log(context.Background(), inv); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.execs) != 2 {
		t.Fatalf("expected 2 Exec calls (invocation + 1 batched logs), got %d", len(db.execs))
	}
	if !strings.Contains(db.execs[1].query, "INSERT INTO function_logs") {
		t.Errorf("second Exec should be batched logs, got %q", db.execs[1].query)
	}
	if got := strings.Count(db.execs[1].query, "(?, ?, ?, ?, ?, ?)"); got != 5 {
		t.Errorf("expected 5 value tuples, got %d", got)
	}
}

func TestInvocationLoggerLog_chunks_over_cap(t *testing.T) {
	db := &recordingExecDB{}
	il := NewInvocationLogger(db, zap.NewNop())

	logs := make([]LogData, maxLogRowsPerInsert+1)
	for i := range logs {
		logs[i] = LogData{Level: "info", Message: "x", Timestamp: time.Now()}
	}
	inv := &InvocationRecordData{ID: "inv-1", FunctionID: "fn-1", Logs: logs}

	if err := il.Log(context.Background(), inv); err != nil {
		t.Fatalf("Log returned error: %v", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.execs) != 3 {
		t.Fatalf("expected 3 Exec calls (invocation + 2 chunked logs), got %d", len(db.execs))
	}
}
