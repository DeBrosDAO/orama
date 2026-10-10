//go:build e2e_fleet

package chainwalletroutes

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// lightCall posts one JSON-RPC request to /v1/chain/light and decodes the answer.
func lightCall(t *testing.T, g *gw.Client, method string, params map[string]string) (result map[string]any, rpcErr map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 41, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	var answer struct {
		ID     int            `json:"id"`
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := postBody(t, g, "light", "application/json", body).Expect(t, http.StatusOK).Decode(&answer); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	if answer.ID != 41 {
		t.Fatalf("%s: the answer's id is %d, want the request's 41 (a light client refuses a mismatch)", method, answer.ID)
	}
	return answer.Result, answer.Error
}

// TestLightRoute_servesTheStateSyncLightClient: the four calls a joining node's state-sync makes
// (status for a trust height, then commit, validators and consensus_params at it) answer through the
// public gateway, and broadcast_evidence, which the light provider would send on a fork, does not.
func TestLightRoute_servesTheStateSyncLightClient(t *testing.T) {
	c := chain.New(t)
	g := gateway(t, c)

	status, rpcErr := lightCall(t, g, "status", nil)
	if rpcErr != nil {
		t.Fatalf("status: %v", rpcErr)
	}
	syncInfo, _ := status["sync_info"].(map[string]any)
	latest, err := strconv.ParseInt(stringField(syncInfo, "latest_block_height"), 10, 64)
	if err != nil || latest < 2 {
		t.Fatalf("status has no usable latest height: %v (%v)", syncInfo, err)
	}
	height := strconv.FormatInt(latest-1, 10)

	commit, rpcErr := lightCall(t, g, "commit", map[string]string{"height": height})
	header, _ := commit["signed_header"].(map[string]any)
	inner, _ := header["header"].(map[string]any)
	if rpcErr != nil || stringField(inner, "height") != height {
		t.Fatalf("commit at %s: %v %v", height, commit, rpcErr)
	}
	vals, rpcErr := lightCall(t, g, "validators", map[string]string{"height": height, "page": "1", "per_page": "100"})
	if list, _ := vals["validators"].([]any); rpcErr != nil || len(list) == 0 {
		t.Fatalf("validators at %s: %v %v", height, vals, rpcErr)
	}
	params, rpcErr := lightCall(t, g, "consensus_params", map[string]string{"height": height})
	if cp, _ := params["consensus_params"].(map[string]any); rpcErr != nil || cp["block"] == nil {
		t.Fatalf("consensus_params at %s: %v %v", height, params, rpcErr)
	}

	if _, rpcErr := lightCall(t, g, "broadcast_evidence", map[string]string{}); rpcErr == nil {
		t.Fatal("broadcast_evidence was answered on the light route")
	}
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}
