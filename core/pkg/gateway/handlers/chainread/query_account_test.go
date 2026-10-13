package chainread

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/DeBrosOfficial/network/pkg/logging"
)

const accountQuery = "/v1/chain/query/cosmos.auth.v1beta1.Query/Account"

func pbBytes(b []byte, num protowire.Number, v []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(b, num, protowire.BytesType), v)
}

func pbVarint(b []byte, num protowire.Number, v uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(b, num, protowire.VarintType), v)
}

func pbAny(url string, value []byte) []byte { return pbBytes(pbBytes(nil, 1, []byte(url)), 2, value) }

// accountAnswer is QueryAccountResponse{account: Any{url, value}}.
func accountAnswer(url string, value []byte) []byte { return pbBytes(nil, 1, pbAny(url, value)) }

// baseAccountBytes is BaseAccount{address, pub_key, account_number, sequence}; a nil key leaves
// pub_key unset.
func baseAccountBytes(address string, secp256k1Key []byte, number, sequence uint64) []byte {
	b := pbBytes(nil, 1, []byte(address))
	if secp256k1Key != nil {
		b = pbBytes(b, 2, pbAny("/cosmos.crypto.secp256k1.PubKey", pbBytes(nil, 1, secp256k1Key)))
	}
	return pbVarint(pbVarint(b, 3, number), 4, sequence)
}

func accountBody(t *testing.T, answer []byte) map[string]any {
	t.Helper()
	p := queryProxy(t, &abciUpstream{value: answer})
	rr := getQuery(p, http.MethodGet, jsonTarget(accountQuery, `{"address":"`+walletAddr+`"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	var got struct {
		Account map[string]any `json:"account"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %s: %v", rr.Body, err)
	}
	return got.Account
}

// Bug #744: Account answered 502 "chain query failed" for every address, because the Any holding
// the BaseAccount could not be resolved to encode the answer as JSON.
func TestQuery_accountOfABaseAccountIsServed(t *testing.T) {
	acct := accountBody(t, accountAnswer("/cosmos.auth.v1beta1.BaseAccount", baseAccountBytes(walletAddr, nil, 16083108751219634649, 3)))
	if acct["@type"] != "/cosmos.auth.v1beta1.BaseAccount" || acct["address"] != walletAddr ||
		acct["account_number"] != "16083108751219634649" || acct["sequence"] != "3" {
		t.Fatalf("account %v", acct)
	}
}

func TestQuery_accountWithASecp256k1PubKeyIsServed(t *testing.T) {
	acct := accountBody(t, accountAnswer("/cosmos.auth.v1beta1.BaseAccount", baseAccountBytes(walletAddr, []byte{0x02, 0xaa, 0xbb}, 1, 0)))
	pub, _ := acct["pub_key"].(map[string]any)
	if pub["@type"] != "/cosmos.crypto.secp256k1.PubKey" || pub["key"] != "Aqq7" {
		t.Fatalf("pub_key %v", pub)
	}
}

func TestQuery_accountOfAModuleAccountIsServed(t *testing.T) {
	module := pbBytes(pbBytes(nil, 1, baseAccountBytes(walletAddr, nil, 4, 0)), 2, []byte("fee_collector"))
	acct := accountBody(t, accountAnswer("/cosmos.auth.v1beta1.ModuleAccount", module))
	if acct["@type"] != "/cosmos.auth.v1beta1.ModuleAccount" || acct["name"] != "fee_collector" {
		t.Fatalf("account %v", acct)
	}
}

// An answer the proxy cannot encode is a 502 with a generic body, and the error behind it is in
// the gateway's log with the method name, so the 502 can be diagnosed.
func TestQuery_aFailureToEncodeTheAnswerIsLoggedWithTheMethod(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	up := &abciUpstream{value: accountAnswer("/cosmos.nope.v1.Account", []byte{0x0a, 0x01, 'x'})}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(srv.Close)
	p, err := New(Config{RPCURL: srv.URL, RESTURL: srv.URL, IndexURL: srv.URL, Logger: &logging.ColoredLogger{Logger: zap.New(core)}})
	if err != nil {
		t.Fatal(err)
	}
	rr := getQuery(p, http.MethodGet, jsonTarget(accountQuery, `{"address":"`+walletAddr+`"}`))
	if rr.Code != http.StatusBadGateway || strings.TrimSpace(rr.Body.String()) != "chain query failed" {
		t.Fatalf("status %d body %q, want 502 chain query failed", rr.Code, rr.Body)
	}
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("%d error log entries, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["method"] != "cosmos.auth.v1beta1.Query/Account" || !strings.Contains(fields["error"].(string), "/cosmos.nope.v1.Account") {
		t.Fatalf("log fields %v", fields)
	}
}

// A key that is not on chain is the caller's answer, not a gateway fault, so it is not logged as an error.
func TestQuery_aMissingAccountIsNotLoggedAsAnError(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	up := &abciUpstream{code: 22, log: "account not found"}
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(srv.Close)
	p, err := New(Config{RPCURL: srv.URL, RESTURL: srv.URL, IndexURL: srv.URL, Logger: &logging.ColoredLogger{Logger: zap.New(core)}})
	if err != nil {
		t.Fatal(err)
	}
	rr := getQuery(p, http.MethodGet, jsonTarget(accountQuery, `{"address":"`+walletAddr+`"}`))
	if rr.Code != http.StatusNotFound || logs.Len() != 0 {
		t.Fatalf("status %d, %d error entries", rr.Code, logs.Len())
	}
}
