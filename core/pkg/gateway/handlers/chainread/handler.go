// Package chainread is a read-only HTTP proxy for the website explorer.
//
// Register mounts it at /v1/chain/. The browser calls that prefix and never
// talks to CometBFT, the SDK REST API, or the chain indexer itself. Those
// listen on loopback (default http://127.0.0.1:31001, http://127.0.0.1:31003
// and http://127.0.0.1:31015; on a co-located machine the same ports on the
// global namespace's address, http://198.18.0.2:..., because there they are
// inside the orama-global network namespace; overridable with ORAMA_CHAIN_RPC_URL,
// ORAMA_CHAIN_REST_URL and ORAMA_CHAIN_INDEX_URL). The indexer routes are
// under /v1/chain/index/ (index.go). Orama module queries are under
// /v1/chain/query/ (query.go).
//
// The caller's path is not forwarded. Each allowlisted route builds one
// upstream URL. Any other path, method, or query is refused. The upstream
// body is copied unchanged. This package speaks HTTP only: it must not
// import the chain module or Cosmos, CometBFT, or gogoproto types. core
// and the chain are separate modules.
package chainread

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalnetns"
)

const (
	mountPrefix = "/v1/chain/"

	upstreamStatus     = "/status"
	upstreamABCIQuery  = "/abci_query"
	upstreamBlock      = "/block"
	upstreamBlockchain = "/blockchain"
	upstreamTx         = "/tx"
	upstreamValidators = "/validators"
	upstreamSupply     = "/cosmos/bank/v1beta1/supply/by_denom"
	upstreamPool       = "/cosmos/staking/v1beta1/pool"

	denomNorama = "norama"

	// CometBFT's /blockchain returns at most 20 blocks. The span is
	// inclusive, so max-min must be at most 19.
	maxHeightSpan = 19
	maxPage       = 100
	maxPerPage    = 100

	defaultPage    = 1
	defaultPerPage = 100

	// A block's transactions are base64 in JSON, so the body is larger than
	// the block. Anything past this is refused whole: a truncated body would
	// no longer be the upstream body. The explorer's largest read is one block;
	// blocks are far below this in practice, and every request holds a body of
	// this size in memory while it is copied.
	defaultMaxBody = 8 << 20

	// statusMaxBody bounds the /status answer read for the height window.
	statusMaxBody = 64 << 10

	upstreamTimeout = 10 * time.Second
)

// Config is the upstream set. Empty URLs are not filled in here;
// ConfigFromEnv applies the loopback defaults.
type Config struct {
	RPCURL   string
	RESTURL  string
	IndexURL string
	Client   *http.Client
}

// ConfigFromEnv reads ORAMA_CHAIN_RPC_URL, ORAMA_CHAIN_REST_URL and
// ORAMA_CHAIN_INDEX_URL. A missing or blank value is the default for this
// machine, not an open target: loopback, or on a co-located machine (the
// orama-global network namespace layout is installed) the namespace address
// the chain and indexer listen on there (constants.GlobalNetnsAddr).
func ConfigFromEnv() Config {
	return configFor(colocated())
}

// systemdUnitDir is where the co-located installer writes the namespace unit.
const systemdUnitDir = constants.SystemdUnitDir

// colocated reports whether this machine shares a cluster node with global
// services. It is a variable so tests can stand in for the machine.
var colocated = func() bool {
	return globalnetns.Installed(systemdUnitDir, func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	})
}

func configFor(colocated bool) Config {
	rpc, rest, index := constants.LocalChainRPCURL(), constants.LocalChainAPIURL(), constants.LocalGlobalIndexerURL()
	if colocated {
		rpc, rest, index = constants.ColocatedChainRPCURL(), constants.ColocatedChainAPIURL(), constants.ColocatedGlobalIndexerURL()
	}
	return Config{
		RPCURL:   envOr("ORAMA_CHAIN_RPC_URL", rpc),
		RESTURL:  envOr("ORAMA_CHAIN_REST_URL", rest),
		IndexURL: envOr("ORAMA_CHAIN_INDEX_URL", index),
	}
}

func envOr(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

// Proxy serves the allowlist. The zero value is not usable; call New.
type Proxy struct {
	rpc     *url.URL
	rest    *url.URL
	index   *url.URL
	client  *http.Client
	maxBody int64
	timeout time.Duration

	// querySlots bounds the module queries in flight (query.go).
	querySlots chan struct{}
	// heightMu guards the cached latest height the query window check reads.
	heightMu  sync.Mutex
	heightVal int64
	heightAt  time.Time
}

// New checks the upstream URLs. A URL with a path, query, fragment, user
// info, or a scheme other than http or https is rejected so the proxy
// cannot be pointed at an arbitrary request target by configuration that
// looks like a base URL.
func New(cfg Config) (*Proxy, error) {
	rpc, err := parseBase(cfg.RPCURL, "chain rpc url")
	if err != nil {
		return nil, err
	}
	rest, err := parseBase(cfg.RESTURL, "chain rest url")
	if err != nil {
		return nil, err
	}
	index, err := parseBase(cfg.IndexURL, "chain index url")
	if err != nil {
		return nil, err
	}
	return &Proxy{
		rpc:     rpc,
		rest:    rest,
		index:   index,
		client:  newClient(cfg.Client),
		maxBody: defaultMaxBody,
		timeout: upstreamTimeout,

		querySlots: make(chan struct{}, queryMaxConcurrent),
	}, nil
}

func parseBase(raw, what string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%s: must be an http or https URL with a host", what)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%s: must not include user info", what)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%s: must not include a query or fragment", what)
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("%s: must not include a path", what)
	}
	u.Path = ""
	u.RawPath = ""
	return u, nil
}

func newClient(given *http.Client) *http.Client {
	base := given
	if base == nil {
		base = &http.Client{Timeout: upstreamTimeout}
	}
	c := *base
	// A 3xx from the node must not be followed. Following it would turn the
	// proxy into a client of whatever Location the upstream names.
	c.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if c.Timeout == 0 {
		c.Timeout = upstreamTimeout
	}
	if c.Transport == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		// Asking for gzip would make the body we copy a decode of the
		// upstream body. Comet does not compress unless asked.
		tr.DisableCompression = true
		c.Transport = tr
	}
	return &c
}

// Mux is anything that can take the mounted handler. *http.ServeMux does.
type Mux interface {
	Handle(pattern string, handler http.Handler)
}

// Register mounts the proxy at /v1/chain/. On a bad configuration it still
// mounts a handler that refuses every request, and returns the error so the
// caller can log it. The request path is the full path; do not strip the
// prefix before it reaches the handler.
func Register(mux Mux) error {
	p, err := New(ConfigFromEnv())
	if err != nil {
		mux.Handle(mountPrefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, http.StatusServiceUnavailable, "chain proxy is misconfigured")
		}))
		return err
	}
	mux.Handle(mountPrefix, p)
	return nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !acceptablePath(r) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, mountPrefix)
	if idx, ok := strings.CutPrefix(rest, indexPrefix); ok {
		p.serveIndex(w, r, idx)
		return
	}
	if name, ok := strings.CutPrefix(rest, queryPrefix); ok {
		p.serveQuery(w, r, name)
		return
	}
	if !knownRoute(rest) {
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
	switch rest {
	case "status":
		p.serveStatus(w, r)
	case "block":
		p.serveBlock(w, r)
	case "blocks":
		p.serveBlocks(w, r)
	case "tx":
		p.serveTx(w, r)
	case "validators":
		p.serveValidators(w, r)
	case "supply/norama":
		p.serveSupply(w, r)
	case "staking/pool":
		p.servePool(w, r)
	default:
		writeErr(w, http.StatusNotFound, "not found")
	}
}

func acceptablePath(r *http.Request) bool {
	reqPath := r.URL.Path
	if reqPath == "" || strings.Contains(reqPath, `\`) || strings.Contains(reqPath, "..") {
		return false
	}
	// path.Clean rewrites duplicate slashes, dot segments, and a trailing
	// slash. None of those are allowlisted routes, so a path Clean would
	// change is refused rather than served under the rewritten name.
	if reqPath != path.Clean(reqPath) {
		return false
	}
	if !strings.HasPrefix(reqPath, mountPrefix) {
		return false
	}
	if strings.Contains(r.RequestURI, "://") || r.URL.Opaque != "" {
		return false
	}
	// An encoded dot or slash is a different path than the one we allow,
	// even when the decoded form looks like /status.
	if strings.ContainsAny(r.URL.RawPath, "%") {
		return false
	}
	return true
}

func knownRoute(rest string) bool {
	switch rest {
	case "status", "block", "blocks", "tx", "validators", "supply/norama", "staking/pool":
		return true
	default:
		return false
	}
}

func (p *Proxy) serveStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	p.forward(w, r, p.rpc, upstreamStatus, nil)
}

func (p *Proxy) serveBlock(w http.ResponseWriter, r *http.Request) {
	q, ok := singleQuery(r, "height")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	if _, good := parsePositive(q.Get("height")); !good {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	up := url.Values{}
	up.Set("height", q.Get("height"))
	p.forward(w, r, p.rpc, upstreamBlock, up)
}

func (p *Proxy) serveBlocks(w http.ResponseWriter, r *http.Request) {
	q, ok := singleQuery(r, "min_height", "max_height")
	if !ok || q.Get("min_height") == "" || q.Get("max_height") == "" {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	min, minOK := parsePositive(q.Get("min_height"))
	max, maxOK := parsePositive(q.Get("max_height"))
	if !minOK || !maxOK || max < min || max-min > maxHeightSpan {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	up := url.Values{}
	up.Set("minHeight", q.Get("min_height"))
	up.Set("maxHeight", q.Get("max_height"))
	p.forward(w, r, p.rpc, upstreamBlockchain, up)
}

func (p *Proxy) serveTx(w http.ResponseWriter, r *http.Request) {
	q, ok := singleQuery(r, "hash")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	hash, good := parseTxHash(q.Get("hash"))
	if !good {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	up := url.Values{}
	// Comet decodes a hash only when it has a 0x prefix.
	up.Set("hash", "0x"+hash)
	p.forward(w, r, p.rpc, upstreamTx, up)
}

func (p *Proxy) serveValidators(w http.ResponseWriter, r *http.Request) {
	q, ok := singleQuery(r, "page", "per_page")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	page := int64(defaultPage)
	perPage := int64(defaultPerPage)
	if raw := q.Get("page"); raw != "" {
		n, good := parsePositive(raw)
		if !good || n > maxPage {
			writeErr(w, http.StatusBadRequest, "bad query")
			return
		}
		page = n
	}
	if raw := q.Get("per_page"); raw != "" {
		n, good := parsePositive(raw)
		if !good || n > maxPerPage {
			writeErr(w, http.StatusBadRequest, "bad query")
			return
		}
		perPage = n
	}
	up := url.Values{}
	up.Set("page", strconv.FormatInt(page, 10))
	up.Set("per_page", strconv.FormatInt(perPage, 10))
	p.forward(w, r, p.rpc, upstreamValidators, up)
}

func (p *Proxy) serveSupply(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	up := url.Values{}
	up.Set("denom", denomNorama)
	p.forward(w, r, p.rest, upstreamSupply, up)
}

func (p *Proxy) servePool(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	p.forward(w, r, p.rest, upstreamPool, nil)
}

// singleQuery accepts only the named keys, each with one non-empty value.
// Keys that are absent are left empty. Any other key, or a repeated key, is refused.
func singleQuery(r *http.Request, keys ...string) (url.Values, bool) {
	allowed := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		allowed[k] = struct{}{}
	}
	out := url.Values{}
	for k, vs := range r.URL.Query() {
		if _, ok := allowed[k]; !ok || len(vs) != 1 || vs[0] == "" {
			return nil, false
		}
		out.Set(k, vs[0])
	}
	return out, true
}

var positiveInt = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

func parsePositive(s string) (int64, bool) {
	if !positiveInt.MatchString(s) {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

var txHashPattern = regexp.MustCompile(`^(?:0x|0X)?([0-9a-fA-F]{64})$`)

func parseTxHash(s string) (string, bool) {
	m := txHashPattern.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return strings.ToLower(m[1]), true
}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, base *url.URL, path string, q url.Values) {
	resp, body, ok := p.fetch(w, r, base, path, q, p.maxBody)
	if !ok {
		return
	}
	// An upstream failure is answered with a fixed body of this proxy's own, never the
	// upstream's: the node's message can carry paths, store details and internal addresses.
	if code, failed := upstreamFailure(resp.StatusCode, base == p.rpc, body); failed {
		writeErr(w, code, failureBody(code))
		return
	}
	w.Header().Set("Content-Type", passContentType(resp.Header.Get("Content-Type")))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// fetch reads one upstream body of at most maxBody bytes. It writes the error
// response itself and reports false when the upstream is unreachable, answers
// a redirect or a status outside 200-599, or answers more than maxBody.
func (p *Proxy) fetch(w http.ResponseWriter, r *http.Request, base *url.URL, path string, q url.Values, maxBody int64) (*http.Response, []byte, bool) {
	u := *base
	u.Path = path
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithTimeout(r.Context(), p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "chain unreachable")
		return nil, nil, false
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "chain unreachable")
		return nil, nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		writeErr(w, http.StatusBadGateway, "upstream redirect refused")
		return nil, nil, false
	}
	if resp.StatusCode < 200 || resp.StatusCode > 599 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		writeErr(w, http.StatusBadGateway, "upstream status refused")
		return nil, nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "chain unreachable")
		return nil, nil, false
	}
	if int64(len(body)) > maxBody {
		writeErr(w, http.StatusBadGateway, "upstream response too large")
		return nil, nil, false
	}
	return resp, body, true
}

func passContentType(ct string) string {
	if ct == "" || len(ct) > 128 || strings.ContainsAny(ct, "\r\n") {
		return "application/json"
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "application/json") {
		return "application/json"
	}
	return ct
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, msg)
	_, _ = io.WriteString(w, "\n")
}

const (
	msgNotFound     = "not found on chain"
	msgChainFailure = "chain request failed"
)

func failureBody(code int) string {
	if code == http.StatusNotFound {
		return msgNotFound
	}
	return msgChainFailure
}

// upstreamFailure reports whether an upstream answer is an error, and the status to answer with:
// 404 when the thing asked for does not exist, 502 for everything else. CometBFT reports an error
// as a JSON-RPC error object, sometimes under a 200, so an RPC body is checked as well as the
// status.
func upstreamFailure(status int, rpc bool, body []byte) (int, bool) {
	if status == http.StatusNotFound {
		return http.StatusNotFound, true
	}
	if status >= 400 {
		return http.StatusBadGateway, true
	}
	if !rpc || !bytes.Contains(body, []byte(`"error"`)) {
		return 0, false
	}
	var env struct {
		Error *struct {
			Message string `json:"message"`
			Data    string `json:"data"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil || env.Error == nil {
		return 0, false
	}
	if strings.Contains(strings.ToLower(env.Error.Message+" "+env.Error.Data), "not found") {
		return http.StatusNotFound, true
	}
	return http.StatusBadGateway, true
}
