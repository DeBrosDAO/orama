package chainread

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

const (
	contractsByCodeQuery = "/v1/chain/query/cosmwasm.wasm.v1.Query/ContractsByCode"
	codeInfoQuery        = "/v1/chain/query/cosmwasm.wasm.v1.Query/CodeInfo"
	allStateQuery        = "/v1/chain/query/cosmwasm.wasm.v1.Query/AllContractState"
)

// explorerPointLookups are the explorer methods that answer one record and take no page.
var explorerPointLookups = names("cosmwasm.wasm.v1.Query/CodeInfo")

// explorerRequests is one valid request document for each paged explorer method, with a placeholder
// for its pagination.
var explorerRequests = map[string]string{
	"cosmos.bank.v1beta1.Query/TotalSupply":     `{%s}`,
	"cosmos.bank.v1beta1.Query/DenomOwners":     `{"denom":"norama",%s}`,
	"cosmwasm.wasm.v1.Query/Codes":              `{%s}`,
	"cosmwasm.wasm.v1.Query/ContractsByCode":    `{"code_id":1,%s}`,
	"cosmwasm.wasm.v1.Query/ContractsByCreator": `{"creator_address":"` + walletAddr + `",%s}`,
	"cosmwasm.wasm.v1.Query/ContractHistory":    `{"address":"` + walletAddr + `",%s}`,
	"cosmwasm.wasm.v1.Query/AllContractState":   `{"address":"` + walletAddr + `",%s}`,
}

// Every name on the explorer list must be a method the embedded descriptors carry, must be served,
// and must not also be on the wallet list: one list decides each method.
func TestQuery_everyExplorerMethodIsEmbeddedAndServedOnce(t *testing.T) {
	embedded, err := chainread.SDKMethods()
	if err != nil {
		t.Fatal(err)
	}
	have := names(embedded...)
	allowed, err := queryAllowed()
	if err != nil {
		t.Fatal(err)
	}
	for n := range explorerQuery {
		if _, ok := have[n]; !ok {
			t.Errorf("%s is on the explorer list but not in the embedded descriptors", n)
		}
		if _, ok := allowed[n]; !ok {
			t.Errorf("%s is on the explorer list but not served", n)
		}
		if _, both := walletQuery[n]; both {
			t.Errorf("%s is on both the wallet and the explorer list", n)
		}
		_, paged := explorerRequests[n]
		_, point := explorerPointLookups[n]
		if paged == point {
			t.Errorf("%s must be either a paged method with a request in this test or a point lookup", n)
		}
	}
	if len(explorerQuery) != len(explorerRequests)+len(explorerPointLookups) {
		t.Errorf("the explorer list has %d methods, the test knows %d", len(explorerQuery), len(explorerRequests)+len(explorerPointLookups))
	}
}

// A paged explorer method takes a page of at most queryMaxPageLimit, as the wallet ones do, and a
// request over the cap never reaches the chain.
func TestQuery_aPageOverTheCapIsRefusedOnEveryExplorerMethod(t *testing.T) {
	up := &abciUpstream{value: []byte{}}
	p := queryProxy(t, up)
	for name, template := range explorerRequests {
		target := "/v1/chain/query/" + name
		over := strings.Replace(template, "%s", `"pagination":{"limit":101}`, 1)
		if rr := getQuery(p, http.MethodGet, jsonTarget(target, over)); rr.Code != http.StatusBadRequest {
			t.Errorf("%s limit 101: status %d, want 400", name, rr.Code)
		}
	}
	if len(up.calls) != 0 {
		t.Fatalf("a refused page reached the chain: %v", up.calls)
	}
	for name, template := range explorerRequests {
		target := "/v1/chain/query/" + name
		atCap := strings.Replace(template, "%s", `"pagination":{"limit":100}`, 1)
		if rr := getQuery(p, http.MethodGet, jsonTarget(target, atCap)); rr.Code != http.StatusOK {
			t.Errorf("%s limit 100: status %d: %s", name, rr.Code, rr.Body)
		}
	}
}

// An offset (the node steps over every entry before it) and a total count (it walks the whole
// store) are refused on every paged explorer method before the chain is asked.
func TestQuery_anOffsetOrATotalCountIsRefusedOnEveryExplorerMethod(t *testing.T) {
	up := &abciUpstream{value: []byte{}}
	p := queryProxy(t, up)
	for name, template := range explorerRequests {
		target := "/v1/chain/query/" + name
		for _, page := range []string{`"pagination":{"offset":1000,"limit":10}`, `"pagination":{"count_total":true}`} {
			req := strings.Replace(template, "%s", page, 1)
			if rr := getQuery(p, http.MethodGet, jsonTarget(target, req)); rr.Code != http.StatusBadRequest {
				t.Errorf("%s %s: status %d, want 400", name, page, rr.Code)
			}
		}
	}
	if len(up.calls) != 0 {
		t.Fatalf("a refused page reached the chain: %v", up.calls)
	}
}

// QueryContractsByCodeResponse{contracts: ["orama1abc"]}.
var contractsAnswer = []byte{0x0a, 0x09, 'o', 'r', 'a', 'm', 'a', '1', 'a', 'b', 'c'}

func TestQuery_contractsOfACodeAreServedAndDecoded(t *testing.T) {
	up := &abciUpstream{value: contractsAnswer}
	p := queryProxy(t, up)
	rr := getQuery(p, http.MethodGet, jsonTarget(contractsByCodeQuery, `{"code_id":7,"pagination":{"limit":20}}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), `"contracts":["orama1abc"]`) {
		t.Fatalf("body %s", rr.Body)
	}
	if len(up.calls) != 1 || up.calls[0].Get("path") != `"/cosmwasm.wasm.v1.Query/ContractsByCode"` {
		t.Fatalf("upstream calls %v", up.calls)
	}
}

// A code id that is not stored, and an address that is no contract, are 404s as ContractInfo's is,
// whatever the page asked for; another refusal of the same code is not.
func TestQuery_aMissingCodeOrContractIsA404OnTheExplorerMethods(t *testing.T) {
	for name, tc := range map[string]struct {
		target, doc, log string
		want             int
	}{
		"code info of a missing code": {codeInfoQuery, `{"code_id":9}`, "no such code: code id 9: unknown request", http.StatusNotFound},
		"state of a non-contract":     {allStateQuery, `{"address":"` + walletAddr + `","pagination":{"limit":10}}`, "no such contract: address x: unknown request", http.StatusNotFound},
		"another refusal":             {codeInfoQuery, `{"code_id":9}`, "out of gas", http.StatusBadGateway},
	} {
		p := queryProxy(t, &abciUpstream{code: 6, log: tc.log})
		rr := getQuery(p, http.MethodGet, jsonTarget(tc.target, tc.doc))
		if rr.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%q)", name, rr.Code, tc.want, rr.Body)
		}
	}
}

func TestQuery_theExplorerListsRefuseWhatTheyDoNotServe(t *testing.T) {
	up := &abciUpstream{value: []byte{}}
	p := queryProxy(t, up)
	for _, target := range []string{
		"/v1/chain/query/cosmwasm.wasm.v1.Query/SmartContractState",
		"/v1/chain/query/cosmwasm.wasm.v1.Query/RawContractState",
		"/v1/chain/query/cosmwasm.wasm.v1.Query/Code",
		"/v1/chain/query/cosmos.bank.v1beta1.Query/DenomsMetadata",
	} {
		if rr := getQuery(p, http.MethodGet, target); rr.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", target, rr.Code)
		}
	}
	if len(up.calls) != 0 {
		t.Fatalf("a refused query reached the chain: %v", up.calls)
	}
}
