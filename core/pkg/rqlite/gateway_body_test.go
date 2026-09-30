package rqlite

import (
	"net/http"
	"strings"
	"testing"
)

var bodyLimitPaths = []string{"query", "exec", "find", "find-one", "select", "transaction", "create-table", "drop-table"}

// An 8 MiB statement answered 200 on stagenet: nothing bounded the body.
func TestGatewayBody_overLimitIs413OnEveryRoute(t *testing.T) {
	g, c := guardedGateway(nil)
	huge := `{"sql":"SELECT '` + strings.Repeat("a", 8<<20) + `'","table":"t","schema":"x"}`
	for _, p := range bodyLimitPaths {
		status, _ := post(t, g, "/v1/rqlite/"+p, huge)
		if status != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: status %d, want 413", p, status)
		}
	}
	if len(c.reached) != 0 {
		t.Errorf("an over-limit body reached the database: %d statements", len(c.reached))
	}
}

func TestGatewayBody_atTheLimitIsStillRead(t *testing.T) {
	g, c := guardedGateway(nil)
	pad := strings.Repeat("a", MaxRequestBodyBytes-len(`{"sql":"SELECT ''"}`))
	status, _ := post(t, g, "/v1/rqlite/query", `{"sql":"SELECT '`+pad+`'"}`)
	if status == http.StatusRequestEntityTooLarge || len(c.reached) != 1 {
		t.Errorf("a body of exactly the limit: status %d, reached %d; want it read and run", status, len(c.reached))
	}
}

func TestGatewayBody_malformedAndBlankStayBadRequest(t *testing.T) {
	g, _ := guardedGateway(nil)
	for _, body := range []string{"", "sql=SELECT 1", `{"sql":"   "}`, `{"sql":1}`} {
		if status, _ := post(t, g, "/v1/rqlite/query", body); status != http.StatusBadRequest {
			t.Errorf("body %q: status %d, want 400", body, status)
		}
	}
}
