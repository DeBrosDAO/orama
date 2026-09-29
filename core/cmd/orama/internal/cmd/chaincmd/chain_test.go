package chaincmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

const testAddr = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"

// run executes `orama chain <args>` with every flag back at its default, so
// one test's --rpc never reaches the next.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	for _, set := range []*pflag.FlagSet{Cmd.PersistentFlags(), queryCmd.Flags()} {
		set.VisitAll(func(f *pflag.Flag) { _ = f.Value.Set(f.DefValue); f.Changed = false })
	}
	t.Setenv("ORAMA_GATEWAY_URL", "")
	var out bytes.Buffer
	Cmd.SetOut(&out)
	Cmd.SetErr(io.Discard)
	Cmd.SetArgs(args)
	err := Cmd.Execute()
	return out.String(), err
}

func server(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestStatus_readsTheGatewayProxy(t *testing.T) {
	url := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chain/status" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"result":{"sync_info":{"latest_block_height":"42"}}}`))
	})
	out, err := run(t, "status", "--gateway", url)
	if err != nil || !strings.Contains(out, `"latest_block_height": "42"`) {
		t.Fatalf("out %q err %v", out, err)
	}
}

func TestStatus_rpcFlagReadsCometBFT(t *testing.T) {
	url := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"jsonrpc":"2.0","id":-1,"result":{"node_info":{"network":"orama-test"}}}`))
	})
	out, err := run(t, "status", "--rpc", url)
	if err != nil || !strings.Contains(out, "orama-test") {
		t.Fatalf("out %q err %v", out, err)
	}
}

func TestStatus_noGatewayIsAUsageError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := run(t, "status")
	if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("err = %v code %d, want a usage error", err, clierr.CodeOf(err))
	}
}

func TestBalance_printsAmountsAndDenoms(t *testing.T) {
	url := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cosmos/bank/v1beta1/balances/"+testAddr {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"balances":[{"denom":"norama","amount":"1500"},{"denom":"factory/x/y","amount":"2"}]}`))
	})
	out, err := run(t, "balance", testAddr, "--node", url)
	if err != nil || out != "1500 norama\n2 factory/x/y\n" {
		t.Fatalf("out %q err %v", out, err)
	}
}

func TestBalance_emptyAccountAndBadAddress(t *testing.T) {
	url := server(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"balances":[]}`)) })
	out, err := run(t, "balance", testAddr, "--node", url)
	if err != nil || out != "no balance\n" {
		t.Fatalf("out %q err %v", out, err)
	}
	for _, bad := range []string{"cosmos1abcdefgh", "orama1", "orama1abc/../x", "0xabc"} {
		if _, err := run(t, "balance", bad, "--node", url); clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("address %q: err = %v, want a usage error", bad, err)
		}
	}
	if _, err := run(t, "balance", testAddr); err == nil || !strings.Contains(err.Error(), "--node") {
		t.Fatalf("err = %v, want the flag to pass", err)
	}
}

// abciServer answers abci_query for one path and records the request data.
func abciServer(t *testing.T, path string, value []byte, data *string) string {
	return server(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct{ Path, Data string } `json:"params"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if req.Params.Path != path {
			t.Errorf("abci path = %q, want %q", req.Params.Path, path)
		}
		*data = req.Params.Data
		out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
			"response": map[string]any{"code": 0, "value": base64.StdEncoding.EncodeToString(value)},
		}})
		w.Write(out)
	})
}

func TestNode_readsX_nodesThroughAbciQuery(t *testing.T) {
	var data string
	url := abciServer(t, "/orama.nodes.v1.Query/Node", []byte{0x0a, 0x05, 0x0a, 0x03, 'n', '-', '1'}, &data)
	out, err := run(t, "node", "n-1", "--rpc", url)
	if err != nil || !strings.Contains(out, `"node_id": "n-1"`) {
		t.Fatalf("out %q err %v", out, err)
	}
	if data != "0a036e2d31" {
		t.Fatalf("request data = %q", data)
	}
}

func TestDeal_requiresANumber(t *testing.T) {
	var data string
	url := abciServer(t, "/orama.storage.v1.Query/Deal", []byte{0x0a, 0x02, 0x08, 0x07}, &data)
	out, err := run(t, "deal", "7", "--rpc", url)
	if err != nil || !strings.Contains(out, `"deal"`) || data != "0807" {
		t.Fatalf("out %q err %v data %q", out, err, data)
	}
	if _, err := run(t, "deal", "seven", "--rpc", url); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
	if _, err := run(t, "deal", "7"); err == nil || !strings.Contains(err.Error(), "--rpc") {
		t.Fatalf("err = %v, want the flag to pass", err)
	}
}

func TestEarnings_readsX_fees(t *testing.T) {
	var data string
	url := abciServer(t, "/orama.fees.v1.Query/Earnings", []byte{0x0a, 0x03, '9', '0', '0'}, &data)
	out, err := run(t, "earnings", testAddr, "--rpc", url)
	if err != nil || !strings.Contains(out, `"balance": "900"`) {
		t.Fatalf("out %q err %v", out, err)
	}
}

func TestValidator_listAndOne(t *testing.T) {
	url := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chain/validators":
			w.Write([]byte(`{"result":{"validators":[{"voting_power":"10"}]}}`))
		case "/cosmos/staking/v1beta1/validators/oramavaloper1abcdefg":
			w.Write([]byte(`{"validator":{"status":"BOND_STATUS_BONDED"}}`))
		default:
			http.NotFound(w, r)
		}
	})
	out, err := run(t, "validator", "--gateway", url)
	if err != nil || !strings.Contains(out, `"voting_power": "10"`) {
		t.Fatalf("out %q err %v", out, err)
	}
	out, err = run(t, "validator", "oramavaloper1abcdefg", "--node", url)
	if err != nil || !strings.Contains(out, "BOND_STATUS_BONDED") {
		t.Fatalf("out %q err %v", out, err)
	}
	if _, err := run(t, "validator", "orama1abcdefg", "--node", url); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

func TestQuery_listAndUnknown(t *testing.T) {
	out, err := run(t, "query", "--list")
	if err != nil || !strings.Contains(out, "orama.nodes.v1.Query/Node\n") {
		t.Fatalf("out %q err %v", out, err)
	}
	if _, err := run(t, "query"); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
	if _, err := run(t, "query", "orama.nodes.v1.Query/Nope", "--rpc", "http://127.0.0.1:1"); err == nil {
		t.Fatal("an unknown query was sent")
	}
}

func TestChainErrorsSurface(t *testing.T) {
	url := server(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) })
	_, err := run(t, "status", "--gateway", url)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("err = %v", err)
	}
}
