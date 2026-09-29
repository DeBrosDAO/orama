package chainread

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

// Module queries: GET /v1/chain/query/<package.Service>/<Method> runs one gRPC query of an Orama
// module through the local node's CometBFT abci_query and answers the decoded response as JSON,
// with the proto field names. Orama modules carry no REST annotations, so this is the only HTTP
// route to x/nodes, x/storage, x/fees and the rest.
//
// Only the Query services embedded in core/pkg/chainread are served, so a Msg, a transaction or
// any other ABCI path is unreachable. The request is one of
//
//	data=<base64 protobuf request>   (standard or URL-safe alphabet, padding optional)
//	json=<JSON request>              (proto field names, snake_case or lowerCamel)
//
// or neither, for the empty request. height=<n> reads at that height; absent or 0 is the latest.
// The query never asks for a proof and never writes.
const (
	queryPrefix = "query/"

	// queryMaxRequest bounds the request as protobuf bytes and as JSON text.
	queryMaxRequest = 4 << 10
	// queryMaxResponse bounds the upstream answer, as chainread's own reader does.
	queryMaxResponse = 4 << 20
)

var (
	queryOnce    sync.Once
	queryMethods map[string]struct{}
	queryErr     error
)

// queryAllowed is the set of "<service>/<method>" names the embedded descriptors carry whose
// service is a Query service.
func queryAllowed() (map[string]struct{}, error) {
	queryOnce.Do(func() {
		names, err := chainread.Methods()
		if err != nil {
			queryErr = err
			return
		}
		queryMethods = make(map[string]struct{}, len(names))
		for _, n := range names {
			if service, _, ok := strings.Cut(n, "/"); ok && strings.HasSuffix(service, ".Query") {
				queryMethods[n] = struct{}{}
			}
		}
	})
	return queryMethods, queryErr
}

func (p *Proxy) serveQuery(w http.ResponseWriter, r *http.Request, name string) {
	allowed, err := queryAllowed()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "chain queries are unavailable")
		return
	}
	if _, ok := allowed[name]; !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if strings.Contains(r.URL.RawQuery, ";") {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	m, err := chainread.Lookup(name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	up, ok := queryUpstream(r, m)
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	_, body, ok := p.fetch(w, r, p.rpc, upstreamABCIQuery, up, queryMaxResponse)
	if !ok {
		return
	}
	out, err := m.DecodeRPC(body)
	if err != nil {
		writeQueryFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// queryUpstream validates the caller's query and builds the abci_query one. It reports false for
// an unknown key, a repeated key, both request forms, an oversized or malformed request, or a
// height that is not a positive integer or 0.
func queryUpstream(r *http.Request, m *chainread.Method) (url.Values, bool) {
	q, ok := singleQuery(r, "data", "json", "height")
	if !ok || (q.Get("data") != "" && q.Get("json") != "") {
		return nil, false
	}
	req, ok := queryRequestBytes(q, m)
	if !ok {
		return nil, false
	}
	up := url.Values{}
	up.Set("path", strconv.Quote(m.Path))
	up.Set("prove", "false")
	if len(req) > 0 {
		up.Set("data", "0x"+hex.EncodeToString(req))
	}
	if h := q.Get("height"); h != "" && h != "0" {
		if _, good := parsePositive(h); !good {
			return nil, false
		}
		up.Set("height", h)
	}
	return up, true
}

func queryRequestBytes(q url.Values, m *chainread.Method) ([]byte, bool) {
	if raw := q.Get("json"); raw != "" {
		if len(raw) > queryMaxRequest {
			return nil, false
		}
		req, err := m.EncodeRequest(raw)
		return req, err == nil
	}
	raw := strings.TrimRight(q.Get("data"), "=")
	if raw == "" {
		return nil, true
	}
	if len(raw) > base64.RawURLEncoding.EncodedLen(queryMaxRequest) {
		return nil, false
	}
	req, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil {
		if req, err = base64.RawURLEncoding.DecodeString(raw); err != nil {
			return nil, false
		}
	}
	return req, m.CheckRequest(req) == nil
}

// writeQueryFailure answers a query the node refused. A key that is not on chain is a 404. The
// node's own message is not repeated: it can carry paths and store details.
func writeQueryFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, chainread.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found on chain")
		return
	}
	writeErr(w, http.StatusBadGateway, "chain query failed")
}
