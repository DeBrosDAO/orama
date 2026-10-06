package indexer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// API limits. The gateway (core/pkg/gateway/handlers/chainread) applies the
// same bounds before it forwards a request.
const (
	APIPrefix    = "/index/v1/"
	DefaultLimit = 20
	MaxLimit     = 100
	MaxPage      = 1000
	defaultPage  = 1
)

// TipSource reports the node's earliest and latest heights. *node.Client
// implements it.
type TipSource interface {
	HeightRange(ctx context.Context) (int64, int64, error)
}

// API serves the index read-only. Any path, method, or query parameter
// outside the routes below is refused.
type API struct {
	store *Store
	chain TipSource
}

// NewAPI returns the HTTP handler over store.
func NewAPI(store *Store, chain TipSource) *API { return &API{store: store, chain: chain} }

var (
	positiveRE = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
	hash32RE   = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs, ok := routeSegments(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	switch {
	case len(segs) == 1 && segs[0] == "status":
		a.serveStatus(w, r)
	case len(segs) == 2 && segs[0] == "blocks":
		a.serveBlock(w, r, segs[1])
	case len(segs) == 2 && segs[0] == "txs":
		a.serveTx(w, r, segs[1])
	case len(segs) == 3 && segs[0] == "accounts" && segs[2] == "txs":
		a.serveAccountTxs(w, r, segs[1])
	case len(segs) == 3 && segs[0] == "cnft" && segs[1] == "assets":
		a.serveAsset(w, r, segs[2])
	case len(segs) == 4 && segs[0] == "cnft" && segs[1] == "owners" && segs[3] == "assets":
		a.serveOwnerAssets(w, r, segs[2])
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// routeSegments splits a clean, unescaped path under APIPrefix.
func routeSegments(r *http.Request) ([]string, bool) {
	p := r.URL.Path
	if r.URL.RawPath != "" || r.URL.Opaque != "" || p != path.Clean(p) || !strings.HasPrefix(p, APIPrefix) {
		return nil, false
	}
	rest := strings.TrimPrefix(p, APIPrefix)
	if rest == "" {
		return nil, false
	}
	return strings.Split(rest, "/"), true
}

func (a *API) serveStatus(w http.ResponseWriter, r *http.Request) {
	if !noQuery(w, r) {
		return
	}
	st, err := a.store.Status()
	if err != nil {
		internalError(w, err)
		return
	}
	earliest, tip, err := a.chain.HeightRange(r.Context())
	if err != nil {
		slog.Error("index status: chain unreachable", "err", err)
		writeError(w, http.StatusBadGateway, "chain unreachable")
		return
	}
	writeJSON(w, map[string]int64{
		"start_height": st.StartHeight, "cursor": st.Cursor, "earliest": earliest, "tip": tip,
	})
}

func (a *API) serveBlock(w http.ResponseWriter, r *http.Request, raw string) {
	if !noQuery(w, r) {
		return
	}
	if !positiveRE.MatchString(raw) {
		writeError(w, http.StatusBadRequest, "height must be a positive integer")
		return
	}
	h, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "height must be a positive integer")
		return
	}
	b, ok, err := a.store.Block(h)
	answer(w, b, ok, err)
}

func (a *API) serveTx(w http.ResponseWriter, r *http.Request, raw string) {
	if !noQuery(w, r) {
		return
	}
	hash, ok := parseHash32(raw)
	if !ok {
		writeError(w, http.StatusBadRequest, "hash must be 64 hex characters")
		return
	}
	t, found, err := a.store.Tx(hash)
	answer(w, t, found, err)
}

func (a *API) serveAccountTxs(w http.ResponseWriter, r *http.Request, addr string) {
	page, limit, ok := pageQuery(w, r)
	if !ok {
		return
	}
	if !IsAccountAddress(addr) {
		writeError(w, http.StatusBadRequest, "address must be a lowercase orama account address")
		return
	}
	txs, err := a.store.AccountTxs(addr, page, limit)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, map[string]any{"page": page, "limit": limit, "txs": txs})
}

func (a *API) serveAsset(w http.ResponseWriter, r *http.Request, raw string) {
	if !noQuery(w, r) {
		return
	}
	id, ok := parseHash32(raw)
	if !ok {
		writeError(w, http.StatusBadRequest, "asset id must be 64 hex characters")
		return
	}
	recs, err := a.store.Assets(id)
	if err != nil {
		internalError(w, err)
		return
	}
	answer(w, map[string]any{"id": hex.EncodeToString(id), "records": toDASList(recs)}, len(recs) > 0, nil)
}

func (a *API) serveOwnerAssets(w http.ResponseWriter, r *http.Request, addr string) {
	page, limit, ok := pageQuery(w, r)
	if !ok {
		return
	}
	if !IsAccountAddress(addr) {
		writeError(w, http.StatusBadRequest, "address must be a lowercase orama account address")
		return
	}
	assets, err := a.store.OwnerAssets(addr, page, limit)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, map[string]any{"page": page, "limit": limit, "assets": toDASList(assets)})
}

func parseHash32(raw string) ([]byte, bool) {
	if !hash32RE.MatchString(raw) {
		return nil, false
	}
	b, err := hex.DecodeString(raw)
	return b, err == nil
}

func noQuery(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "this route takes no query parameters")
		return false
	}
	return true
}

// pageQuery accepts only page and limit, each at most once. page is 1 to
// MaxPage, limit 1 to MaxLimit.
func pageQuery(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad query")
		return 0, 0, false
	}
	page, limit := defaultPage, DefaultLimit
	for k, vs := range q {
		max, known := pageBounds[k]
		if !known {
			writeError(w, http.StatusBadRequest, "unknown query parameter")
			return 0, 0, false
		}
		if len(vs) != 1 {
			writeError(w, http.StatusBadRequest, "repeated query parameter "+k)
			return 0, 0, false
		}
		n, good := boundedInt(vs[0], max)
		if !good {
			writeError(w, http.StatusBadRequest, k+" is out of range")
			return 0, 0, false
		}
		if k == "page" {
			page = n
		} else {
			limit = n
		}
	}
	return page, limit, true
}

var pageBounds = map[string]int{"page": MaxPage, "limit": MaxLimit}

func boundedInt(raw string, max int) (int, bool) {
	if !positiveRE.MatchString(raw) {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	return n, err == nil && n <= max
}

func answer(w http.ResponseWriter, v any, found bool, err error) {
	switch {
	case err != nil:
		internalError(w, err)
	case !found:
		writeError(w, http.StatusNotFound, "not indexed")
	default:
		writeJSON(w, v)
	}
}

func internalError(w http.ResponseWriter, err error) {
	slog.Error("index read failed", "err", err)
	writeError(w, http.StatusInternalServerError, "index read failed")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to write index response", "err", err)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		slog.Error("failed to write index error", "err", err)
	}
}
