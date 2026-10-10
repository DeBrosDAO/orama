package chainread

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

const (
	walletAddr = "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqmg3rhc"

	balanceQuery      = "/v1/chain/query/cosmos.bank.v1beta1.Query/Balance"
	allBalancesQuery  = "/v1/chain/query/cosmos.bank.v1beta1.Query/AllBalances"
	contractInfoQuery = "/v1/chain/query/cosmwasm.wasm.v1.Query/ContractInfo"
)

func jsonTarget(path, doc string) string { return path + "?json=" + url.QueryEscape(doc) }

// QueryBalanceResponse{balance: Coin{denom: "norama", amount: "100"}}.
var balanceAnswer = []byte{0x0a, 0x0d, 0x0a, 0x06, 'n', 'o', 'r', 'a', 'm', 'a', 0x12, 0x03, '1', '0', '0'}

func TestQuery_walletBalanceIsServedAndDecoded(t *testing.T) {
	up := &abciUpstream{value: balanceAnswer}
	p := queryProxy(t, up)
	rr := getQuery(p, http.MethodGet, jsonTarget(balanceQuery, `{"address":"`+walletAddr+`","denom":"norama"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), `"amount":"100"`) || !strings.Contains(rr.Body.String(), `"denom":"norama"`) {
		t.Fatalf("body %s", rr.Body)
	}
	if len(up.calls) != 1 || up.calls[0].Get("path") != `"/cosmos.bank.v1beta1.Query/Balance"` {
		t.Fatalf("upstream calls %v", up.calls)
	}
}

// Every name on the wallet list must be a method the embedded descriptors carry; a typo would
// otherwise be a route that is silently never served.
func TestQuery_everyWalletMethodIsEmbeddedAndServed(t *testing.T) {
	embedded, err := chainread.SDKMethods()
	if err != nil {
		t.Fatal(err)
	}
	have := names(embedded...)
	allowed, err := queryAllowed()
	if err != nil {
		t.Fatal(err)
	}
	for n := range walletQuery {
		if _, ok := have[n]; !ok {
			t.Errorf("%s is on the wallet list but not in the embedded descriptors", n)
		}
		if _, ok := allowed[n]; !ok {
			t.Errorf("%s is on the wallet list but not served", n)
		}
	}
	if len(walletQuery) != 15 {
		t.Errorf("the wallet list has %d methods; changing it is a decision, update this count and docs/whitepaper/technical-reference/vol2/39-chain-architecture.md", len(walletQuery))
	}
}

// A contract query for an address that is no contract is a typed 404. wasmd's error loses its wasm
// code on the way out of baseapp, so the chain answers code 6 of codespace sdk with the message.
func TestQuery_contractInfoOfANonContractIs404(t *testing.T) {
	up := &abciUpstream{code: 6, log: "orama1qq: no such contract: address orama1qq: unknown request"}
	p := queryProxy(t, up)
	rr := getQuery(p, http.MethodGet, jsonTarget(contractInfoQuery, `{"address":"`+walletAddr+`"}`))
	if rr.Code != http.StatusNotFound || strings.TrimSpace(rr.Body.String()) != "not found on chain" {
		t.Fatalf("status %d body %q, want 404 not found on chain", rr.Code, rr.Body)
	}
}

func TestQuery_aChainRefusalIsOnlyANotFoundWhenItIsTheNoSuchContractOne(t *testing.T) {
	for name, up := range map[string]*abciUpstream{
		"another code-6 refusal":               {code: 6, log: "unknown query path"},
		"no such contract with the wrong code": {code: 5, log: "no such contract"},
	} {
		p := queryProxy(t, up)
		rr := getQuery(p, http.MethodGet, jsonTarget(contractInfoQuery, `{"address":"`+walletAddr+`"}`))
		if rr.Code != http.StatusBadGateway {
			t.Errorf("%s: status %d, want 502", name, rr.Code)
		}
	}
}

func TestQuery_aMalformedAddressIsA400WithoutTheNodesMessage(t *testing.T) {
	up := &abciUpstream{code: 18, log: "decoding bech32 failed: invalid checksum at /src/x/bank/keeper/grpc_query.go:45"}
	p := queryProxy(t, up)
	rr := getQuery(p, http.MethodGet, jsonTarget(balanceQuery, `{"address":"nope","denom":"norama"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "bech32") || strings.Contains(rr.Body.String(), "grpc_query") {
		t.Fatalf("the node's message was repeated: %q", rr.Body)
	}
}

func TestQuery_aPageOverTheCapIsRefusedBeforeTheChainIsAsked(t *testing.T) {
	up := &abciUpstream{value: []byte{}}
	p := queryProxy(t, up)
	over := `{"address":"` + walletAddr + `","pagination":{"limit":101}}`
	if rr := getQuery(p, http.MethodGet, jsonTarget(allBalancesQuery, over)); rr.Code != http.StatusBadRequest {
		t.Fatalf("limit 101: status %d, want 400", rr.Code)
	}
	huge := `{"address":"` + walletAddr + `","pagination":{"limit":18446744073709551615}}`
	if rr := getQuery(p, http.MethodGet, jsonTarget(allBalancesQuery, huge)); rr.Code != http.StatusBadRequest {
		t.Fatalf("limit max uint64: status %d, want 400", rr.Code)
	}
	if len(up.calls) != 0 {
		t.Fatalf("a refused page reached the chain: %v", up.calls)
	}
	for name, doc := range map[string]string{
		"limit at the cap": `{"address":"` + walletAddr + `","pagination":{"limit":100}}`,
		"no pagination":    `{"address":"` + walletAddr + `"}`,
		"limit unset":      `{"address":"` + walletAddr + `","pagination":{"count_total":true}}`,
	} {
		if rr := getQuery(p, http.MethodGet, jsonTarget(allBalancesQuery, doc)); rr.Code != http.StatusOK {
			t.Errorf("%s: status %d: %s", name, rr.Code, rr.Body)
		}
	}
}

func TestQuery_aMethodWithoutPaginationIgnoresTheCap(t *testing.T) {
	up := &abciUpstream{value: nodeAnswer}
	p := queryProxy(t, up)
	if rr := getQuery(p, http.MethodGet, nodeQuery); rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
}
