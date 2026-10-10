//go:build e2e_fleet

package chainwalletroutes

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	bank         = "cosmos.bank.v1beta1.Query"
	auth         = "cosmos.auth.v1beta1.Query"
	staking      = "cosmos.staking.v1beta1.Query"
	distribution = "cosmos.distribution.v1beta1.Query"
	wasm         = "cosmwasm.wasm.v1.Query"

	// The gateway's limits, docs/whitepaper/technical-reference/vol2/39-chain-architecture.md "Simulate and broadcast".
	maxTxBytes   = 1 << 20
	maxPageLimit = 100
	// txLogLeaks are what a refusal's log must not carry: a filesystem path, a source file, an IP address.
	txLogLeaks = `(/var/|/home/|/usr/|/src/|\.go:\d+|\b\d{1,3}(\.\d{1,3}){3}\b)`
)

var leaks = regexp.MustCompile(txLogLeaks)

// gateway is the public gateway pinned to the faucet node's address, so every request of the
// package reaches one gateway: the per-address buckets of simulate and broadcast are per gateway,
// and the test reads a chain the gateway's own node has to have caught up with.
func gateway(t *testing.T, c *chain.Chain) *gw.Client {
	t.Helper()
	return harness.GW(t).PinTo(c.FaucetNode(t).PublicIP)
}

// query runs one GET /v1/chain/query/<service>/<method> with a JSON request.
func query(t *testing.T, g *gw.Client, service, method string, request any) *gw.Response {
	t.Helper()
	q := url.Values{}
	if request != nil {
		doc, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		q.Set("json", string(doc))
	}
	return g.MustSend(t, gw.Req{Path: "/v1/chain/query/" + service + "/" + method, Query: q})
}

// queryOK is query that must answer 200, decoded.
func queryOK(t *testing.T, g *gw.Client, service, method string, request any) map[string]any {
	t.Helper()
	var out map[string]any
	if err := query(t, g, service, method, request).Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// postTx sends {"tx_bytes": base64(raw)} to POST /v1/chain/<route>.
func postTx(t *testing.T, g *gw.Client, route string, raw []byte) *gw.Response {
	t.Helper()
	return postBody(t, g, route, "application/json", []byte(`{"tx_bytes":"`+base64.StdEncoding.EncodeToString(raw)+`"}`))
}

func postBody(t *testing.T, g *gw.Client, route, contentType string, body []byte) *gw.Response {
	t.Helper()
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return g.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/chain/" + route, Header: h, Body: body})
}

// refusal is the 422 body of a transaction the chain refused.
type refusal struct {
	Code      uint32 `json:"code"`
	Codespace string `json:"codespace"`
	Log       string `json:"log"`
	TxHash    string `json:"tx_hash"`
}

func decodeRefusal(t *testing.T, resp *gw.Response) refusal {
	t.Helper()
	resp.Expect(t, http.StatusUnprocessableEntity)
	var r refusal
	if err := resp.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if r.Code == 0 {
		t.Fatalf("a 422 with code 0: %s", resp.Body)
	}
	if leaks.MatchString(r.Log) {
		t.Fatalf("the refusal's log names node internals: %q", r.Log)
	}
	return r
}

func coin(amount chain.Int) map[string]any {
	return map[string]any{"denom": chain.Denom, "amount": amount.String()}
}

func delegateMsg(delegator, valoper string, amount chain.Int) chain.Msg {
	return chain.NewMsg("/cosmos.staking.v1beta1.MsgDelegate", map[string]any{
		"delegator_address": delegator, "validator_address": valoper, "amount": coin(amount),
	})
}

func sendMsg(from, to string, amount chain.Int) chain.Msg {
	return chain.NewMsg("/cosmos.bank.v1beta1.MsgSend", map[string]any{
		"from_address": from, "to_address": to, "amount": []any{coin(amount)},
	})
}

// firstValoper is the operator address of a validator of the run's chain, from a node's own staking query.
func firstValoper(t *testing.T, c *chain.Chain) string {
	t.Helper()
	var r struct {
		Validators []struct {
			Operator string `json:"operator_address"`
		} `json:"validators"`
	}
	c.Query(t, c.Node(t, 0), &r, "staking", "validators")
	if len(r.Validators) == 0 || r.Validators[0].Operator == "" {
		t.Fatalf("the run's chain has no validator: %+v", r)
	}
	return r.Validators[0].Operator
}

// amountOf reads balance.amount (or amount) from a decoded answer.
func amountOf(t *testing.T, doc map[string]any, member string) chain.Int {
	t.Helper()
	m, _ := doc[member].(map[string]any)
	s, _ := m["amount"].(string)
	var i chain.Int
	if _, ok := i.SetString(s, 10); !ok {
		t.Fatalf("no %s.amount in %v", member, doc)
	}
	return i
}
