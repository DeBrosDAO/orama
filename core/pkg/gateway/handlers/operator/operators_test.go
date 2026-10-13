package operator

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// memOperators is the operator list the handlers read and write.
type memOperators struct {
	rqlite.Client
	rows map[string]Operator
	fail bool
}

func newMem(wallets ...string) *memOperators {
	m := &memOperators{rows: map[string]Operator{}}
	for _, w := range wallets {
		m.rows[normaliseWallet(w)] = Operator{Wallet: normaliseWallet(w), AddedBy: "genesis:n1", AddedAt: "2026-01-01"}
	}
	return m
}

type counted struct{ n int64 }

func (c counted) LastInsertId() (int64, error) { return 0, nil }
func (c counted) RowsAffected() (int64, error) { return c.n, nil }

func (m *memOperators) Query(_ context.Context, dest any, query string, args ...any) error {
	if m.fail {
		return errString("registry down")
	}
	switch rows := dest.(type) {
	case *[]Operator:
		for _, op := range m.rows {
			*rows = append(*rows, op)
		}
	case *[]struct {
		Wallet string `db:"wallet"`
	}:
		w := normaliseWallet(args[0].(string))
		if _, ok := m.rows[w]; ok {
			*rows = append(*rows, struct {
				Wallet string `db:"wallet"`
			}{Wallet: w})
		}
	default:
		return errString("unexpected query: " + query)
	}
	return nil
}

func (m *memOperators) Exec(_ context.Context, query string, args ...any) (sql.Result, error) {
	if m.fail {
		return nil, errString("registry down")
	}
	if strings.Contains(query, "INSERT") {
		wallet := args[0].(string)
		if _, exists := m.rows[wallet]; exists {
			return counted{0}, nil
		}
		m.rows[wallet] = Operator{Wallet: wallet, AddedBy: args[1].(string)}
		return counted{1}, nil
	}
	wallet := args[0].(string)
	if _, ok := m.rows[wallet]; !ok || len(m.rows) <= 1 {
		return counted{0}, nil
	}
	delete(m.rows, wallet)
	return counted{1}, nil
}

func (m *memOperators) handler() *Handler {
	return NewHandler(zap.NewNop(), m)
}

func bodyWallet(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Wallet string `json:"wallet"`
		Code   string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
	if resp.Code != "" {
		return resp.Code
	}
	return resp.Wallet
}

func TestOperators_aNonOperatorIsRefused(t *testing.T) {
	h := newMem("0xoperator").handler()
	w := httptest.NewRecorder()
	h.HandleOperators(w, walletRequest(http.MethodGet, "/v1/operator/operators", "0xstranger"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body %s", w.Code, w.Body.String())
	}
}

func TestOperators_addIsIdempotentAndListed(t *testing.T) {
	mem := newMem("0xoperator")
	h := mem.handler()
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := walletRequest(http.MethodPost, "/v1/operator/operators", "0xOperator")
		req.Body = jsonBody(`{"wallet":" 0x1111111111111111111111111111111111111111 "}`)
		h.HandleOperators(w, req)
		if w.Code != http.StatusOK || bodyWallet(t, w) != "0x1111111111111111111111111111111111111111" {
			t.Fatalf("add %d: status %d body %s", i, w.Code, w.Body.String())
		}
	}
	if len(mem.rows) != 2 {
		t.Fatalf("rows = %d, want one new wallet", len(mem.rows))
	}
	if mem.rows["0x1111111111111111111111111111111111111111"].AddedBy != "operator:0xoperator" {
		t.Fatalf("added_by = %q", mem.rows["0x1111111111111111111111111111111111111111"].AddedBy)
	}
}

func TestOperators_aMistypedAddressIsRefused(t *testing.T) {
	mem := newMem("0xoperator")
	w := httptest.NewRecorder()
	req := walletRequest(http.MethodPost, "/v1/operator/operators", "0xoperator")
	req.Body = jsonBody(`{"wallet":"0xnot-an-address"}`)
	mem.handler().HandleOperators(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if len(mem.rows) != 1 {
		t.Fatalf("a rejected wallet was stored: %#v", mem.rows)
	}
}

func TestOperators_removeLastIsConflict(t *testing.T) {
	h := newMem("0xonly").handler()
	w := httptest.NewRecorder()
	h.HandleOperators(w, walletRequest(http.MethodDelete, "/v1/operator/operators/0xOnly", "0xonly"))
	if w.Code != http.StatusConflict || bodyWallet(t, w) != "LAST_OPERATOR" {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestOperators_removeUnknownIsNotFound(t *testing.T) {
	h := newMem("0xoperator", "0xother").handler()
	w := httptest.NewRecorder()
	h.HandleOperators(w, walletRequest(http.MethodDelete, "/v1/operator/operators/0xmissing", "0xoperator"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestOperators_removeOneOfTwo(t *testing.T) {
	mem := newMem("0xoperator", "0xother")
	w := httptest.NewRecorder()
	mem.handler().HandleOperators(w, walletRequest(http.MethodDelete, "/v1/operator/operators/0xOther", "0xoperator"))
	if w.Code != http.StatusOK || bodyWallet(t, w) != "0xother" {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if _, still := mem.rows["0xother"]; still || len(mem.rows) != 1 {
		t.Fatalf("rows left: %#v", mem.rows)
	}
}

func jsonBody(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }
