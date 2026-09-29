package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	gatewayName    = "gateway"
	gatewayTimeout = 30 * time.Second
	gatewayLimit   = 1 << 20
)

// parseGatewayNode reads the gateway's decoded x/nodes Node answer and requires it to be node id.
func parseGatewayNode(body []byte, id string) error {
	var ans struct {
		Node struct {
			NodeID   string `json:"node_id"`
			Operator string `json:"operator"`
			Status   string `json:"status"`
		} `json:"node"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		return fmt.Errorf("the answer is not JSON: %w", err)
	}
	if ans.Node.NodeID != id {
		return fmt.Errorf("the answer names node %q, want %q", ans.Node.NodeID, id)
	}
	if ans.Node.Operator == "" {
		return fmt.Errorf("node %s has no operator in the answer", id)
	}
	return nil
}

// gatewayClient trusts only the CA bundle in caFile: stagenet's certificates come from the Let's
// Encrypt staging CA, which no system store carries. An empty caFile uses the system store.
func gatewayClient(caFile string) (*http.Client, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read the CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no PEM certificate", caFile)
		}
		cfg.RootCAs = pool
	}
	return &http.Client{Timeout: gatewayTimeout, Transport: &http.Transport{TLSClientConfig: cfg}}, nil
}

// gatewayNodeURL is GET <base>/v1/chain/query/orama.nodes.v1.Query/Node?json={"node_id":"<id>"}.
func gatewayNodeURL(base, id string) string {
	q := url.Values{"json": {fmt.Sprintf(`{"node_id":%q}`, id)}}
	return strings.TrimRight(base, "/") + "/v1/chain/query/orama.nodes.v1.Query/Node?" + q.Encode()
}

// checkGateway reads every registered node through the public gateway.
func checkGateway(ctx context.Context, e *env) Result {
	client, err := gatewayClient(e.caFile)
	if err != nil {
		return fail(gatewayName, "%v", err)
	}
	for _, n := range e.nodes {
		id := nodeID(n.Name)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayNodeURL(e.gateway, id), nil)
		if err != nil {
			return fail(gatewayName, "%v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return fail(gatewayName, "GET %s: %v", id, err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, gatewayLimit))
		resp.Body.Close()
		if err != nil {
			return fail(gatewayName, "read the answer for %s: %v", id, err)
		}
		if resp.StatusCode != http.StatusOK {
			return fail(gatewayName, "%s answered HTTP %d for %s: %s", e.gateway, resp.StatusCode, id, strings.TrimSpace(string(body[:min(len(body), 200)])))
		}
		if err := parseGatewayNode(body, id); err != nil {
			return fail(gatewayName, "%v", err)
		}
	}
	return pass(gatewayName, "%s /v1/chain/query returned the x/nodes record of all %d nodes", e.gateway, len(e.nodes))
}
