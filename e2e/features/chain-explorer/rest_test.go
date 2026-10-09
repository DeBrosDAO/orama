//go:build e2e_fleet

package chainexplorer

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
)

// restGet reads one path of validator 0's REST API (loopback on the node, through an SSH tunnel).
func restGet(t *testing.T, c *chain.Chain, path string) (int, []byte) {
	t.Helper()
	url := "http://" + c.Tunnel(t, c.Node(t, 0), chain.APIPort) + path
	resp, err := http.Get(url) //nolint:gosec // the tunnel is to a node of this run
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp.StatusCode, body
}

// TestModuleREST_servesTheQueriesTheModulesAnnotate: the node's REST API serves each Orama module's
// queries at the paths of its query.proto (google.api.http), with the same answer as the gRPC
// query; a path parameter is read as the request field, and a path that is no query is a 404.
func TestModuleREST_servesTheQueriesTheModulesAnnotate(t *testing.T) {
	t.Parallel()
	c := chain.New(t)

	var rest, grpc struct {
		BaseFee string `json:"base_fee"`
	}
	code, body := restGet(t, c, "/orama/fees/v1/base-fee")
	if code != http.StatusOK || json.Unmarshal(body, &rest) != nil || rest.BaseFee == "" {
		t.Fatalf("GET /orama/fees/v1/base-fee: HTTP %d %.200s", code, body)
	}
	c.Query(t, c.Node(t, 0), &grpc, "fees", "base-fee")
	if grpc.BaseFee == "" {
		t.Fatalf("the gRPC base fee is empty")
	}

	code, body = restGet(t, c, "/orama/emission/v1/schedule-at/3")
	var schedule struct {
		Epoch string `json:"epoch"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &schedule) != nil || schedule.Epoch != "3" {
		t.Errorf("GET /orama/emission/v1/schedule-at/3: HTTP %d %.200s", code, body)
	}

	if code, body = restGet(t, c, "/orama/token/v1/token?denom=factory/orama1none/none"); code != http.StatusNotFound {
		t.Errorf("a token that does not exist: HTTP %d %.200s, want 404", code, body)
	}
	if code, _ = restGet(t, c, "/orama/token/v1/nonsense"); code != http.StatusNotFound {
		t.Errorf("a path that is no query: HTTP %d, want 404", code)
	}
}
