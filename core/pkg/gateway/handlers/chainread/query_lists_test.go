package chainread

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The paged list queries the explorer's nodes, storage and Tor pages read. Each answer carries one
// entry and a next page key ("k"), so the decoded body shows the entry and the key.
var listQueries = []struct {
	name   string
	method string
	list   string
	field  string
	value  []byte
	want   string
}{
	// QueryNodesResponse{nodes: [Node{node_id: "n-1"}], pagination: {next_key: "k"}}
	{"nodes", "orama.nodes.v1.Query/Nodes", "nodes", "node_id", []byte{0x0a, 0x05, 0x0a, 0x03, 'n', '-', '1', 0x12, 0x03, 0x0a, 0x01, 'k'}, "n-1"},
	// QueryRelaysResponse{relays: [Relay{node_id: "n-1"}], pagination: {next_key: "k"}}
	{"relays", "orama.relay.v1.Query/Relays", "relays", "node_id", []byte{0x0a, 0x05, 0x0a, 0x03, 'n', '-', '1', 0x12, 0x03, 0x0a, 0x01, 'k'}, "n-1"},
	// QueryOperatorRelaysResponse{relays: [RelayStanding{last_payout_epoch: 7}], pagination: {next_key: "k"}}
	{"operator relays", "orama.relay.v1.Query/OperatorRelays", "relays", "last_payout_epoch", []byte{0x0a, 0x02, 0x10, 0x07, 0x12, 0x03, 0x0a, 0x01, 'k'}, "7"},
	// QueryRelayPayoutsResponse{payouts: [RelayPayout{epoch: 7}], pagination: {next_key: "k"}}
	{"relay payouts", "orama.relay.v1.Query/RelayPayouts", "payouts", "epoch", []byte{0x0a, 0x02, 0x08, 0x07, 0x12, 0x03, 0x0a, 0x01, 'k'}, "7"},
	// QueryDealsResponse{deals: [Deal{id: 7}], pagination: {next_key: "k"}}
	{"deals", "orama.storage.v1.Query/Deals", "deals", "id", []byte{0x0a, 0x02, 0x08, 0x07, 0x12, 0x03, 0x0a, 0x01, 'k'}, "7"},
}

func TestQuery_listQueriesAreServedWithTheirPageKey(t *testing.T) {
	for _, c := range listQueries {
		t.Run(c.name, func(t *testing.T) {
			up := &abciUpstream{value: c.value}
			p := queryProxy(t, up)
			rr := getQuery(p, http.MethodGet, jsonTarget("/v1/chain/query/"+c.method, `{"pagination":{"limit":100,"reverse":true}}`))
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			var entries []map[string]any
			if err := json.Unmarshal(got[c.list], &entries); err != nil || len(entries) != 1 {
				t.Fatalf("%s: %s (%v)", c.list, got[c.list], err)
			}
			if v, _ := entries[0][c.field].(string); v != c.want {
				t.Errorf("%s = %v, want %s", c.field, entries[0][c.field], c.want)
			}
			var page struct {
				NextKey string `json:"next_key"`
			}
			if err := json.Unmarshal(got["pagination"], &page); err != nil || page.NextKey != "aw==" {
				t.Errorf("pagination %s (%v), want the base64 key of k", got["pagination"], err)
			}
		})
	}
}

func TestQuery_listQueriesRefuseAPageOverTheCapBeforeTheChainIsAsked(t *testing.T) {
	for _, c := range listQueries {
		t.Run(c.name, func(t *testing.T) {
			up := &abciUpstream{value: c.value}
			p := queryProxy(t, up)
			if rr := getQuery(p, http.MethodGet, jsonTarget("/v1/chain/query/"+c.method, `{"pagination":{"limit":101}}`)); rr.Code != http.StatusBadRequest {
				t.Fatalf("limit 101: status %d, want 400", rr.Code)
			}
			if len(up.calls) != 0 {
				t.Fatalf("a refused page reached the chain: %v", up.calls)
			}
		})
	}
}

func TestQuery_listQueriesNeedALimitEvenForTheFirstPage(t *testing.T) {
	for _, c := range listQueries {
		t.Run(c.name, func(t *testing.T) {
			up := &abciUpstream{value: c.value}
			p := queryProxy(t, up)
			if rr := getQuery(p, http.MethodGet, "/v1/chain/query/"+c.method); rr.Code != http.StatusBadRequest {
				t.Fatalf("no pagination: status %d, want 400", rr.Code)
			}
			if len(up.calls) != 0 {
				t.Fatalf("a refused page reached the chain: %v", up.calls)
			}
			if rr := getQuery(p, http.MethodGet, jsonTarget("/v1/chain/query/"+c.method, `{"pagination":{"limit":10}}`)); rr.Code != http.StatusOK {
				t.Fatalf("limit 10: status %d: %s", rr.Code, rr.Body)
			}
		})
	}
}

func TestQuery_listQueriesAreGetOnly(t *testing.T) {
	for _, c := range listQueries {
		t.Run(c.name, func(t *testing.T) {
			p := queryProxy(t, &abciUpstream{value: c.value})
			if rr := getQuery(p, http.MethodPost, "/v1/chain/query/"+c.method); rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("POST: status %d, want 405", rr.Code)
			}
		})
	}
}
