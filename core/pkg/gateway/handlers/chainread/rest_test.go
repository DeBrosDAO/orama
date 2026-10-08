package chainread

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// restProxy is a proxy whose REST upstream records what it is asked.
func restProxy(t *testing.T) (*Proxy, *indexUpstream) {
	t.Helper()
	up := &indexUpstream{}
	srv := up.serve(t)
	p, err := New(Config{RPCURL: "http://127.0.0.1:9", RESTURL: srv.URL, IndexURL: "http://127.0.0.1:9"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, up
}

func TestRESTRoutes_buildEachUpstreamURL(t *testing.T) {
	cases := []struct{ path, want string }{
		{"/v1/chain/bank/balances/" + testAccount, "/cosmos/bank/v1beta1/balances/" + testAccount + "?pagination.limit=100"},
		{"/v1/chain/staking/validators", "/cosmos/staking/v1beta1/validators?pagination.limit=200"},
		{"/v1/chain/staking/delegations/" + testAccount, "/cosmos/staking/v1beta1/delegations/" + testAccount + "?pagination.limit=100"},
		{"/v1/chain/staking/unbonding/" + testAccount, "/cosmos/staking/v1beta1/delegators/" + testAccount + "/unbonding_delegations?pagination.limit=100"},
	}
	for _, tc := range cases {
		p, up := restProxy(t)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d body %q", tc.path, rec.Code, rec.Body.String())
			continue
		}
		if got := up.seen(); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: upstream saw %v, want %s", tc.path, got, tc.want)
		}
	}
}

func TestRESTRoutes_refuseBadRequestsWithoutCallingTheNode(t *testing.T) {
	cases := []struct {
		method, path string
		code         int
	}{
		{http.MethodGet, "/v1/chain/bank/balances/", http.StatusNotFound},
		{http.MethodGet, "/v1/chain/bank/balances/notanaddress", http.StatusNotFound},
		{http.MethodGet, "/v1/chain/bank/balances/" + strings.ToUpper(testAccount), http.StatusNotFound},
		{http.MethodGet, "/v1/chain/bank/balances/cosmos1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqnrql8a", http.StatusNotFound},
		{http.MethodGet, "/v1/chain/bank/balances/" + testAccount + "/extra", http.StatusNotFound},
		{http.MethodGet, "/v1/chain/staking/delegations/x", http.StatusNotFound},
		{http.MethodGet, "/v1/chain/staking/unbonding/x", http.StatusNotFound},
		{http.MethodGet, "/v1/chain/bank/balances/" + testAccount + "?pagination.limit=1000", http.StatusBadRequest},
		{http.MethodGet, "/v1/chain/staking/validators?pagination.limit=1000", http.StatusBadRequest},
		{http.MethodGet, "/v1/chain/staking/validators?status=x", http.StatusBadRequest},
		{http.MethodPost, "/v1/chain/staking/validators", http.StatusMethodNotAllowed},
		{http.MethodPost, "/v1/chain/bank/balances/" + testAccount, http.StatusMethodNotAllowed},
		{http.MethodGet, "/v1/chain/bank/supply", http.StatusNotFound},
	}
	for _, tc := range cases {
		p, up := restProxy(t)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.code {
			t.Errorf("%s %s: status %d, want %d", tc.method, tc.path, rec.Code, tc.code)
		}
		if got := up.seen(); len(got) != 0 {
			t.Errorf("%s %s: the node was asked %v", tc.method, tc.path, got)
		}
	}
}

func TestRESTRoutes_aNodeFailureGetsAFixedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"open /var/lib/oramad/data: leveldb corrupted"}`))
	}))
	defer srv.Close()
	p, err := New(Config{RPCURL: "http://127.0.0.1:9", RESTURL: srv.URL, IndexURL: "http://127.0.0.1:9"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chain/bank/balances/"+testAccount, nil))
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "leveldb") {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
}
