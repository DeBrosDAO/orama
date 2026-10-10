package chainread

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
)

// The light-client route, /v1/chain/light: the CometBFT JSON-RPC methods a joining node's
// state-sync light client calls (commit and validators through its light provider,
// consensus_params through its state provider), plus status to pick a trust height. A joiner's
// [statesync] rpc_servers name two nodes' https://<host>/v1/chain/light.
//
// The CometBFT RPC itself is reachable only from its host, and it serves broadcast and unsafe
// methods. This route forwards nothing a caller wrote: it reads one JSON-RPC request, checks the
// method and each parameter, and asks the node with a request of its own. broadcast_evidence, which
// the light provider sends when it sees a fork, is refused like every other method.
const (
	lightPath = "light"
	// lightMaxRequest bounds a light-client request; the largest is a validators call.
	lightMaxRequest = 1 << 10
	// lightMaxConcurrent bounds the light-client calls in flight on one gateway.
	lightMaxConcurrent = 16
	// lightMaxIDLen bounds a request id, which the answer repeats.
	lightMaxIDLen = 64
	// lightMaxPerPage is CometBFT's own ceiling for validators per_page.
	lightMaxPerPage = 100

	jsonRPCInvalidRequest = -32600
	jsonRPCMethodNotFound = -32601
	jsonRPCInvalidParams  = -32602
	jsonRPCInternal       = -32603
)

// lightParams are the parameters each method may carry; every one is optional.
var lightParams = map[string][]string{
	"status":           nil,
	"commit":           {"height"},
	"consensus_params": {"height"},
	"validators":       {"height", "page", "per_page"},
}

type lightRequest struct {
	JSONRPC string                     `json:"jsonrpc"`
	ID      json.RawMessage            `json:"id"`
	Method  string                     `json:"method"`
	Params  map[string]json.RawMessage `json:"params"`
}

type lightError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

type lightAnswer struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *lightError     `json:"error,omitempty"`
}

func (p *Proxy) serveLight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	req, id, ok := readLightRequest(w, r)
	if !ok {
		return
	}
	params, lerr := lightUpstreamParams(req)
	if lerr != nil {
		writeJSON(w, http.StatusOK, lightAnswer{JSONRPC: "2.0", ID: id, Error: lerr})
		return
	}
	select {
	case p.lightSlots <- struct{}{}:
	default:
		w.Header().Set("Retry-After", txBusyRetryAfter)
		writeErr(w, http.StatusServiceUnavailable, "too many light-client calls in flight")
		return
	}
	defer func() { <-p.lightSlots }()
	ctx, cancel := context.WithTimeout(r.Context(), p.timeout)
	defer cancel()
	result, rpcErr, err := p.rpcPost(ctx, req.Method, params)
	switch {
	case err != nil:
		writeErr(w, http.StatusBadGateway, "chain unreachable")
	case rpcErr != nil:
		// The node's message tells a light client a height is not available yet; it is cleaned of
		// paths and addresses like every log this proxy repeats.
		writeJSON(w, http.StatusOK, lightAnswer{JSONRPC: "2.0", ID: id, Error: &lightError{
			Code: jsonRPCInternal, Message: sanitizeLog(rpcErr.Message), Data: sanitizeLog(rpcErr.Data),
		}})
	default:
		writeJSON(w, http.StatusOK, lightAnswer{JSONRPC: "2.0", ID: id, Result: result})
	}
}

// readLightRequest reads one JSON-RPC request. A body that is not one, or whose id is not a short
// number or string, is refused before anything else; the id it returns is the one to answer with.
func readLightRequest(w http.ResponseWriter, r *http.Request) (lightRequest, json.RawMessage, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, lightMaxRequest+1))
	if err != nil || len(raw) > lightMaxRequest {
		writeErr(w, http.StatusRequestEntityTooLarge, "request too large")
		return lightRequest{}, nil, false
	}
	var req lightRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.More() {
		writeErr(w, http.StatusBadRequest, "not a JSON-RPC request")
		return lightRequest{}, nil, false
	}
	if !validLightID(req.ID) {
		writeErr(w, http.StatusBadRequest, "bad JSON-RPC id")
		return lightRequest{}, nil, false
	}
	if req.JSONRPC != "2.0" {
		writeJSON(w, http.StatusOK, lightAnswer{JSONRPC: "2.0", ID: req.ID,
			Error: &lightError{Code: jsonRPCInvalidRequest, Message: "jsonrpc must be 2.0"}})
		return lightRequest{}, nil, false
	}
	return req, req.ID, true
}

// validLightID is a JSON number or string of at most lightMaxIDLen bytes.
func validLightID(id json.RawMessage) bool {
	if len(id) == 0 || len(id) > lightMaxIDLen {
		return false
	}
	var v any
	if err := json.Unmarshal(id, &v); err != nil {
		return false
	}
	switch v.(type) {
	case float64, string:
		return true
	}
	return false
}

// lightUpstreamParams checks req's method and parameters and returns the parameters to send the
// node, each a positive integer in CometBFT's string form.
func lightUpstreamParams(req lightRequest) (map[string]any, *lightError) {
	allowed, ok := lightParams[req.Method]
	if !ok {
		return nil, &lightError{Code: jsonRPCMethodNotFound, Message: "method not available on this route"}
	}
	out := map[string]any{}
	for name, raw := range req.Params {
		if !slices.Contains(allowed, name) {
			return nil, &lightError{Code: jsonRPCInvalidParams, Message: "unknown parameter " + strconv.Quote(name)}
		}
		n, ok := positiveParam(raw)
		if !ok || (name == "per_page" && n > lightMaxPerPage) {
			return nil, &lightError{Code: jsonRPCInvalidParams, Message: "parameter " + strconv.Quote(name) + " must be a positive integer"}
		}
		out[name] = strconv.FormatInt(n, 10)
	}
	return out, nil
}

// positiveParam reads a positive integer given as a JSON number or a decimal string (CometBFT's
// client sends 64-bit integers as strings). null, which the client sends for an absent height, is
// not accepted: an absent parameter is left out.
func positiveParam(raw json.RawMessage) (int64, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		var n json.Number
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&n); err != nil {
			return 0, false
		}
		s = n.String()
	}
	return parsePositive(s)
}
