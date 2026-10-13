// Package host is what the demo functions use of the Orama runtime: the caller,
// the cache, the namespace's database and the invocation log. A function takes a
// Host, so its logic runs under `go test` with an in-memory one (Fake) and in the
// gateway with the real one (New, built for WASI).
package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Host is the part of the runtime the demo functions call. The real
// implementation passes each call to a host function the gateway exports to
// every function (core/pkg/serverless/engine.go, registerHostModule).
type Host interface {
	// CallerWallet is get_caller_wallet: the JWT subject of a signed-in caller, a
	// namespace pseudo-id for an API key, empty for no credential.
	CallerWallet() string
	// CacheIncrBy is cache_incr_by: an atomic add to a counter in the namespace's
	// cache, returning the new value. A counter that does not exist starts at 0, so
	// a delta of 0 reads it. The runtime answers 0 for a failure too, which is why
	// an increment by a positive delta treats 0 as one.
	CacheIncrBy(key string, delta int64) int64
	// DBQuery is db_query_v2: a SELECT on the namespace's database.
	DBQuery(sql string, args ...any) ([]map[string]any, error)
	// DBExec is db_execute_v2: one INSERT, UPDATE, DELETE or DDL statement.
	DBExec(sql string, args ...any) (Result, error)
	// LogInfo is log_info: a line in the function's invocation log.
	LogInfo(msg string)
}

// Result is what db_execute_v2 reports.
type Result struct {
	RowsAffected int64 `json:"rows_affected"`
	LastInsertID int64 `json:"last_insert_id"`
}

// ErrNoDatabase is what a Host without a database answers.
var ErrNoDatabase = errors.New("no database in this host")

// Run reads the invocation input from stdin, calls handler, and writes its
// output to stdout. A handler error is written as {"error": ...} and exits 1:
// it is a failure of the function, not an answer. A request the caller got wrong
// is an answer, and the handler returns it as output.
func Run(handler func(input []byte) ([]byte, error)) {
	if err := run(os.Stdin, os.Stdout, handler); err != nil {
		os.Exit(1)
	}
}

// run is Run over the given streams. It returns the handler's error after
// writing it.
func run(in io.Reader, out io.Writer, handler func([]byte) ([]byte, error)) error {
	input, err := io.ReadAll(in)
	if err == nil {
		var output []byte
		if output, err = handler(input); err == nil {
			_, err = out.Write(output)
			return err
		}
	}
	body, mErr := json.Marshal(map[string]string{"error": err.Error()})
	if mErr != nil {
		body = []byte(fmt.Sprintf(`{"error":%q}`, "the function failed"))
	}
	_, _ = out.Write(body)
	return err
}
