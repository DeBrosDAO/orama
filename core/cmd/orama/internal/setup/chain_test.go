package setup

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

// chainServer answers the REST paths a session reads.
func chainServer(t *testing.T, routes map[string]func(w http.ResponseWriter)) *restSession {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := routes[r.URL.RequestURI()]; ok {
			h(w)
			return
		}
		http.Error(w, `{"code":5,"message":"not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return &restSession{base: srv.URL, http: srv.Client(), stop: func() {}}
}

func jsonBody(body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { fmt.Fprint(w, body) }
}

func TestRestSession_params(t *testing.T) {
	s := chainServer(t, map[string]func(http.ResponseWriter){
		"/orama/nodes/v1/params": jsonBody(`{"params":{"min_bond":[{"role":"ROLE_STORAGE","amount":"1000000000"},{"role":"ROLE_RELAY","amount":"2000000000"}],"bond_per_gib":"1000000000"}}`),
	})
	p, err := s.Params(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.MinBond[clusterreg.RoleRelay].String() != "2000000000" || p.BondPerGiB.String() != "1000000000" {
		t.Fatalf("%+v", p)
	}
}

func TestRestSession_paramsRefusals(t *testing.T) {
	for name, body := range map[string]string{
		"an unknown role":    `{"params":{"min_bond":[{"role":"ROLE_KING","amount":"1"}],"bond_per_gib":"1"}}`,
		"a bad amount":       `{"params":{"min_bond":[{"role":"ROLE_STORAGE","amount":"lots"}],"bond_per_gib":"1"}}`,
		"no bond per GiB":    `{"params":{"min_bond":[]}}`,
		"something not json": `<html>`,
	} {
		s := chainServer(t, map[string]func(http.ResponseWriter){"/orama/nodes/v1/params": jsonBody(body)})
		if _, err := s.Params(context.Background()); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := chainServer(t, nil).Params(context.Background()); err == nil || !strings.Contains(err.Error(), "the chain has no x/nodes parameters") {
		t.Errorf("a chain without the module: %v", err)
	}
}

func TestRestSession_balance(t *testing.T) {
	path := "/cosmos/bank/v1beta1/balances/" + testOperator + "/by_denom?denom=norama"
	s := chainServer(t, map[string]func(http.ResponseWriter){path: jsonBody(`{"balance":{"denom":"norama","amount":"1500000000"}}`)})
	got, err := s.Balance(context.Background(), testOperator)
	if err != nil || got.String() != "1500000000" {
		t.Fatalf("%v, %v", got, err)
	}
	empty, err := chainServer(t, nil).Balance(context.Background(), testOperator)
	if err != nil || empty.Sign() != 0 {
		t.Fatalf("an account the chain has not seen holds nothing: %v, %v", empty, err)
	}
	bad := chainServer(t, map[string]func(http.ResponseWriter){path: jsonBody(`{"balance":{"amount":"x"}}`)})
	if _, err := bad.Balance(context.Background(), testOperator); err == nil {
		t.Error("a balance that is not a number is an error")
	}
}

func TestRestSession_operatorAndValidatorExistence(t *testing.T) {
	valoper, err := clusterreg.ValidatorAddress(testOperator)
	if err != nil {
		t.Fatal(err)
	}
	s := chainServer(t, map[string]func(http.ResponseWriter){
		"/orama/nodes/v1/operator/" + testOperator:      jsonBody(`{"operator":{"address":"` + testOperator + `"}}`),
		"/cosmos/staking/v1beta1/validators/" + valoper: jsonBody(`{"validator":{}}`),
	})
	if ok, err := s.OperatorRegistered(context.Background(), testOperator); err != nil || !ok {
		t.Errorf("operator: %v, %v", ok, err)
	}
	if ok, err := s.ValidatorExists(context.Background(), testOperator); err != nil || !ok {
		t.Errorf("validator: %v, %v", ok, err)
	}
	none := chainServer(t, nil)
	if ok, err := none.OperatorRegistered(context.Background(), testOperator); err != nil || ok {
		t.Errorf("a 404 is absence, not an error: %v, %v", ok, err)
	}
	if ok, err := none.ValidatorExists(context.Background(), testOperator); err != nil || ok {
		t.Errorf("validator absent: %v, %v", ok, err)
	}
}

func TestRestSession_aNodeIsReadWithItsBindings(t *testing.T) {
	pub := bytes.Repeat([]byte{7}, 32)
	sig := bytes.Repeat([]byte{9}, 64)
	doc := `{"node":{"bindings":[{"service":"consensus","key_type":"KEY_TYPE_ED25519","pubkey":"` + base64.StdEncoding.EncodeToString(pub) +
		`","signature":"` + base64.StdEncoding.EncodeToString(sig) + `"}]}}`
	s := chainServer(t, map[string]func(http.ResponseWriter){
		"/orama/nodes/v1/node/alice": jsonBody(doc),
		"/orama/nodes/v1/node/bad":   jsonBody(`{"node":{"bindings":[{"service":"consensus","key_type":"KEY_TYPE_UNSPECIFIED"}]}}`),
		"/orama/nodes/v1/node/b64":   jsonBody(`{"node":{"bindings":[{"service":"tor","key_type":"KEY_TYPE_ED25519","pubkey":"***"}]}}`),
	})
	n, err := s.Node(context.Background(), "alice")
	if err != nil || n == nil || len(n.Bindings) != 1 {
		t.Fatalf("%+v, %v", n, err)
	}
	b := n.Bindings[0]
	if b.Service != "consensus" || b.KeyType != "ed25519" || !bytes.Equal(b.Pubkey, pub) || !bytes.Equal(b.Signature, sig) {
		t.Errorf("binding %+v", b)
	}
	for _, id := range []string{"bad", "b64"} {
		if _, err := s.Node(context.Background(), id); err == nil {
			t.Errorf("node %s: a binding that is not a service key is an error", id)
		}
	}
}

func TestRestSession_aNodeIsReadWithItsBondsAndCapacity(t *testing.T) {
	s := chainServer(t, map[string]func(http.ResponseWriter){
		"/orama/nodes/v1/node/alice": jsonBody(`{"node":{"roles":["ROLE_STORAGE","ROLE_RELAY"],"bonds":[{"role":"ROLE_STORAGE","amount":"10000000000"}],"declared_capacity_bytes":"10000000000"}}`),
		"/orama/nodes/v1/node/bad":   jsonBody(`{"node":{"bonds":[{"role":"ROLE_STORAGE","amount":"x"}]}}`),
		"/orama/nodes/v1/node/cap":   jsonBody(`{"node":{"declared_capacity_bytes":"-5"}}`),
	})
	n, err := s.Node(context.Background(), "alice")
	if err != nil || n == nil {
		t.Fatal(err)
	}
	if len(n.Roles) != 2 || n.Bonds[clusterreg.RoleStorage].String() != "10000000000" || n.CapacityBytes != 10_000_000_000 {
		t.Fatalf("%+v", n)
	}
	if none, err := s.Node(context.Background(), "ghost"); err != nil || none != nil {
		t.Errorf("an unregistered node is nil: %v, %v", none, err)
	}
	for _, id := range []string{"bad", "cap"} {
		if _, err := s.Node(context.Background(), id); err == nil {
			t.Errorf("node %s: want an error", id)
		}
	}
}

func TestRestSession_aServerErrorIsNotAbsence(t *testing.T) {
	s := chainServer(t, map[string]func(http.ResponseWriter){
		"/orama/nodes/v1/node/alice": func(w http.ResponseWriter) { http.Error(w, "database is locked", http.StatusInternalServerError) },
	})
	if _, err := s.Node(context.Background(), "alice"); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("got %v: a failing node must not read as an unregistered one", err)
	}
}

func TestIsNotFound(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   bool
	}{
		{404, "", true}, {500, `{"message":"rpc error: code = NotFound desc = node not found"}`, true},
		{500, "internal", false}, {400, "bad request", false}, {200, "", false},
	}
	for _, c := range cases {
		if got := isNotFound(c.status, []byte(c.body)); got != c.want {
			t.Errorf("isNotFound(%d, %q) = %v", c.status, c.body, got)
		}
	}
}

func TestRoleNumber(t *testing.T) {
	if n, ok := roleNumber("ROLE_EXIT"); !ok || n != clusterreg.RoleExit {
		t.Errorf("got %d, %v", n, ok)
	}
	if _, ok := roleNumber("ROLE_UNSPECIFIED"); ok {
		t.Error("unspecified is not a role")
	}
}

func TestRestSession_aSlowChainTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	s := &restSession{base: srv.URL, http: &http.Client{Timeout: 20 * time.Millisecond}, stop: func() {}}
	if _, err := s.Balance(context.Background(), testOperator); err == nil {
		t.Fatal("a chain that never answers must be an error")
	}
}

func TestParseAmount(t *testing.T) {
	for in, ok := range map[string]bool{
		"0": true, "1000000000": true, "": false, "-5": false, "+5": false, "1e9": false, "12 ": false,
		strings.Repeat("9", maxAmountDigits): true, strings.Repeat("9", maxAmountDigits+1): false,
	} {
		if _, got := parseAmount(in); got != ok {
			t.Errorf("parseAmount(%q) ok = %v, want %v", in, got, ok)
		}
	}
}

func TestRestSession_aNegativeBondIsRefused(t *testing.T) {
	s := chainServer(t, map[string]func(http.ResponseWriter){
		"/orama/nodes/v1/node/alice": jsonBody(`{"node":{"bonds":[{"role":"ROLE_STORAGE","amount":"-1000000000000"}]}}`),
		"/orama/nodes/v1/params":     jsonBody(`{"params":{"min_bond":[{"role":"ROLE_STORAGE","amount":"-1"}],"bond_per_gib":"1"}}`),
	})
	if _, err := s.Node(context.Background(), "alice"); err == nil {
		t.Error("a node reporting a negative bond would make setup sign an inflated one")
	}
	if _, err := s.Params(context.Background()); err == nil {
		t.Error("a negative minimum bond is not a parameter")
	}
}

func TestChainHTTPClient_doesNotFollowARedirect(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the redirect was followed")
	}))
	defer elsewhere.Close()
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	defer node.Close()
	s := &restSession{base: node.URL, http: chainHTTPClient(), stop: func() {}}
	_, err := s.Balance(context.Background(), testOperator)
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("got %v: a node that redirects is an error, never a place to go", err)
	}
}
