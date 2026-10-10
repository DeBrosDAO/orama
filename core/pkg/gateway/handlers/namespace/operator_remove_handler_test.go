package namespace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

const testOperator = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func removeRequest(wallet, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/operator/namespaces/remove", strings.NewReader(body))
	if wallet != "" {
		r = r.WithContext(context.WithValue(r.Context(), ctxkeys.JWT, &auth.JWTClaims{Sub: wallet}))
	}
	return r
}

func operatorRemoveFixture(t *testing.T) (*OperatorRemoveHandler, *countingDeprov, func(string) int) {
	t.Helper()
	db := migratedDB(t)
	dp := &countingDeprov{}
	deletes := NewDeleteHandler(dp, rqlite.NewClient(db), &unpinRecorder{}, nil, zap.NewNop())
	seedNamespaces(t, deletes, "orphan")
	if err := deletes.refs.Register(context.Background(), "", "orphan", "backfilled"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO operators (wallet, added_by) VALUES (?, 'test')`, testOperator); err != nil {
		t.Fatal(err)
	}
	count := func(name string) int {
		return countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE name = ?`, name)
	}
	return NewOperatorRemoveHandler(deletes, zap.NewNop()), dp, count
}

// A namespace whose owner's wallet is gone could never be deleted and kept its
// cluster for ever (stagenet, 2026-09-30: six e2e namespaces of throwaway
// wallets). An operator removes it.
func TestOperatorRemove_anOperatorRemovesANamespaceItDoesNotOwn(t *testing.T) {
	h, dp, count := operatorRemoveFixture(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, removeRequest(testOperator, `{"namespace":"Orphan","reason":"owner wallet lost"}`))
	if rec.Code != http.StatusOK || dp.calls != 1 {
		t.Fatalf("status %d (%s), deprovisions %d", rec.Code, rec.Body.String(), dp.calls)
	}
	if count("orphan") != 0 {
		t.Fatal("the namespace is still registered")
	}
}

func TestOperatorRemove_refusals(t *testing.T) {
	for name, tc := range map[string]struct {
		wallet, body string
		want         int
	}{
		"no wallet":         {"", `{"namespace":"orphan","reason":"x"}`, http.StatusUnauthorized},
		"not an operator":   {"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", `{"namespace":"orphan","reason":"x"}`, http.StatusForbidden},
		"no reason":         {testOperator, `{"namespace":"orphan"}`, http.StatusBadRequest},
		"reason too long":   {testOperator, `{"namespace":"orphan","reason":"` + strings.Repeat("r", maxRemoveReason+1) + `"}`, http.StatusBadRequest},
		"the lobby":         {testOperator, `{"namespace":"default","reason":"x"}`, http.StatusBadRequest},
		"reserved":          {testOperator, `{"namespace":"index","reason":"x"}`, http.StatusBadRequest},
		"not a name":        {testOperator, `{"namespace":"../etc","reason":"x"}`, http.StatusBadRequest},
		"not json":          {testOperator, `nope`, http.StatusBadRequest},
		"unknown namespace": {testOperator, `{"namespace":"nobody-here","reason":"x"}`, http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			h, dp, count := operatorRemoveFixture(t)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, removeRequest(tc.wallet, tc.body))
			if rec.Code != tc.want {
				t.Fatalf("status %d (%s), want %d", rec.Code, rec.Body.String(), tc.want)
			}
			if dp.calls != 0 || count("orphan") != 1 {
				t.Fatal("a refused removal touched the namespace")
			}
		})
	}
}

func TestOperatorRemove_onlyPost(t *testing.T) {
	h, _, _ := operatorRemoveFixture(t)
	rec := httptest.NewRecorder()
	r := removeRequest(testOperator, "")
	r.Method = http.MethodGet
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", rec.Code)
	}
}
