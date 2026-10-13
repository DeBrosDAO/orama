package rqlite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// batchOnlyClient answers Batch with a fixed outcome; anything else panics.
type batchOnlyClient struct {
	Client
	res *BatchResult
	err error
}

func (c batchOnlyClient) Batch(context.Context, []BatchOp) (*BatchResult, error) {
	return c.res, c.err
}

func postTransaction(t *testing.T, c Client) (int, map[string]any) {
	t.Helper()
	g := NewHTTPGateway(c, "/v1/rqlite")
	req := httptest.NewRequest(http.MethodPost, "/v1/rqlite/transaction",
		strings.NewReader(`{"ops":[{"kind":"exec","sql":"INSERT INTO t VALUES (1)"}]}`))
	rec := httptest.NewRecorder()
	g.handleTransaction(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

// bugboard #267: a batch that never reached a statement is a server-side
// failure, not "op 0 rolled back" with an empty error.
func TestHandleTransaction_batchLevelFailureIsNotARollback(t *testing.T) {
	transport := errors.New("rqlite.Batch: dial tcp 127.0.0.1:10001: connect: connection refused")
	res := &BatchResult{Results: []OpResult{{}}}
	res.setBatchError(transport)

	status, body := postTransaction(t, batchOnlyClient{res: res, err: transport})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for a transport failure (body %v)", status, body)
	}
	if body["code"] != BatchCodeUnavailable || body["error"] == "" {
		t.Errorf("body = %v, want the batch error and code %s", body, BatchCodeUnavailable)
	}
}

func TestHandleTransaction_statementRollbackCarriesItsCode(t *testing.T) {
	stmt := &StatementError{Err: errors.New("UNIQUE constraint failed: t.id")}
	res := &BatchResult{Results: []OpResult{failedOp(BatchOpExec, stmt)}}

	status, body := postTransaction(t, batchOnlyClient{res: res, err: stmt})
	if status != http.StatusConflict || body["status"] != "rollback" {
		t.Fatalf("status = %d body = %v, want a 409 rollback", status, body)
	}
	if body["code"] != BatchCodeConstraintViolation {
		t.Errorf("code = %v, want %s", body["code"], BatchCodeConstraintViolation)
	}
}

func TestBatchFailureStatus(t *testing.T) {
	for code, want := range map[string]int{
		BatchCodeTooManyStatements:   http.StatusBadRequest,
		BatchCodeInvalidArgument:     http.StatusBadRequest,
		BatchCodeUnavailable:         http.StatusServiceUnavailable,
		BatchCodeDeadlineExceeded:    http.StatusServiceUnavailable,
		BatchCodeInternal:            http.StatusInternalServerError,
		BatchCodeConstraintViolation: http.StatusInternalServerError,
	} {
		if got := batchFailureStatus(code); got != want {
			t.Errorf("batchFailureStatus(%s) = %d, want %d", code, got, want)
		}
	}
}

func TestHandleTransaction_refusedBatchIsCoded(t *testing.T) {
	refused := errors.New("rqlite.Batch: too many ops (105 > max 100)")
	status, body := postTransaction(t, batchOnlyClient{err: refused})
	if status != http.StatusBadRequest || body["code"] != BatchCodeTooManyStatements {
		t.Fatalf("status = %d body = %v, want 400 coded %s", status, body, BatchCodeTooManyStatements)
	}
}
