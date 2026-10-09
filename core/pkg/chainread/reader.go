// Package chainread reads the Orama chain over HTTP JSON only.
//
// core/ links no chain or Cosmos code, so nothing here imports a chain
// package. Three read paths exist, and each command names the one it uses:
//
//   - the gateway's /v1/chain/ proxy: status, blocks, transactions, the
//     validator set, supply, the staking pool and the indexer;
//   - a node's Cosmos REST API (--node): accounts, bank balances and every
//     standard SDK module;
//   - a node's CometBFT RPC (--rpc): abci_query, the route the gateway proxies to
//     x/nodes, x/storage, x/fees and the other Orama modules. GRPC encodes the request and decodes the
//     response with descriptors embedded from chain/proto.
package chainread

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	requestTimeout = 15 * time.Second
	responseLimit  = 4 << 20
)

// Reader holds the base URLs a command was given. An empty base means the
// read path is not configured, and reading through it is an error that names
// the flag to pass.
type Reader struct {
	// Gateway is the gateway root, for /v1/chain/….
	Gateway string
	// REST is a node's Cosmos REST API root, for example http://127.0.0.1:31003.
	REST string
	// RPC is a node's CometBFT RPC root, for example http://127.0.0.1:31001.
	RPC string
	// HTTP is the client every request uses. Nil is a client with a request timeout.
	HTTP *http.Client
}

func (r *Reader) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: requestTimeout}
}

func base(root, flag string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("this read needs %s", flag)
	}
	return strings.TrimRight(root, "/"), nil
}

// GatewayGet reads path (for example "status" or "index/blocks/12") under
// /v1/chain/ on the gateway.
func (r *Reader) GatewayGet(ctx context.Context, path string) (json.RawMessage, error) {
	root, err := base(r.Gateway, "--gateway")
	if err != nil {
		return nil, err
	}
	return r.get(ctx, root+"/v1/chain/"+path)
}

// RESTGet reads path (for example "/cosmos/bank/v1beta1/balances/orama1…")
// from the node's REST API.
func (r *Reader) RESTGet(ctx context.Context, path string) (json.RawMessage, error) {
	root, err := base(r.REST, "--node")
	if err != nil {
		return nil, err
	}
	return r.get(ctx, root+path)
}

// RPCGet reads path (for example "/status") from the node's CometBFT RPC and
// returns its "result".
func (r *Reader) RPCGet(ctx context.Context, path string) (json.RawMessage, error) {
	root, err := base(r.RPC, "--rpc")
	if err != nil {
		return nil, err
	}
	body, err := r.get(ctx, root+path)
	if err != nil {
		return nil, err
	}
	return rpcResult(body)
}

// Escape makes one value safe as a URL path segment.
func Escape(segment string) string { return url.PathEscape(segment) }

func (r *Reader) get(ctx context.Context, target string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	return r.do(req)
}

func (r *Reader) do(req *http.Request) (json.RawMessage, error) {
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", req.URL.Redacted(), err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", req.URL.Redacted(), err)
	}
	if len(body) > responseLimit {
		return nil, fmt.Errorf("response from %s is over %d bytes", req.URL.Host, responseLimit)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s answered HTTP %d: %s", req.URL.Host, resp.StatusCode, truncate(string(body)))
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("%s did not answer JSON", req.URL.Host)
	}
	return body, nil
}

func truncate(s string) string {
	const max = 200
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

// rpcResult unwraps a JSON-RPC answer: its result, or its error.
func rpcResult(body []byte) (json.RawMessage, error) {
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
			Data    string `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("RPC answer is not JSON-RPC: %w", err)
	}
	if env.Error != nil {
		return nil, fmt.Errorf("RPC error: %s %s", env.Error.Message, env.Error.Data)
	}
	if len(env.Result) == 0 {
		return nil, fmt.Errorf("RPC answer has no result")
	}
	return env.Result, nil
}
