package chainread

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

// cosmos-sdk's query.Paginate turns count_total on whenever the limit is 0 or the pagination
// is absent (types/query/pagination.go initPageRequestDefaults), which walks the whole store
// prefix. queryUpstream refuses count_total=true but passes a request that leaves the limit out.
func TestQueryUpstream_refusesAPaginatedQueryThatLeavesTheLimitOut(t *testing.T) {
	for _, name := range []string{
		"cosmos.bank.v1beta1.Query/DenomOwners",
		"cosmwasm.wasm.v1.Query/AllContractState",
		"cosmos.bank.v1beta1.Query/TotalSupply",
	} {
		m, err := chainread.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		for label, q := range map[string]url.Values{
			"no request":     {},
			"limit left out": {"json": {`{"pagination":{"key":""}}`}},
			"explicit zero":  {"json": {`{"pagination":{"limit":"0"}}`}},
		} {
			r := httptest.NewRequest("GET", "/v1/chain/query/"+name+"?"+q.Encode(), nil)
			if _, ok := queryUpstream(r, m); ok {
				t.Errorf("%s (%s): the proxy forwards a request the SDK answers with count_total, a walk of the whole store", name, label)
			}
		}
	}
}
