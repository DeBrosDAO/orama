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
	"time"

	gogoproto "github.com/cosmos/gogoproto/proto"
)

// API limits. The gateway (core/pkg/gateway/handlers/chainread) applies the
// same bounds before it forwards a request.
const (
	APIPrefix    = "/index/v1/"
	DefaultLimit = 20
	MaxLimit     = 100
	// MaxPage bounds page on the lists that stay short (epochs, supply, validators, the statistics,
	// the rewards of an epoch and of a validator). The lists that grow with the chain or that one
	// account can grow (the transactions, the blocks, an owner's assets, an account's rewards) take
	// MaxListPage and a cursor (cursor.go, cursor_keys.go).
	MaxPage     = 1000
	defaultPage = 1
)

// TipSource reports the node's earliest and latest heights. *node.Client
// implements it.
type TipSource interface {
	HeightRange(ctx context.Context) (int64, int64, error)
}

// StateReader runs a query against the state committed at a height (0 is the latest).
type StateReader interface {
	QueryAt(ctx context.Context, height int64, method string, req, resp gogoproto.Message) error
}

// ChainReader is what the API reads from the node: its heights, and, for the home snapshot, its
// state. *node.Client implements it.
type ChainReader interface {
	TipSource
	StateReader
}

// API serves the index read-only. Any path, method, or query parameter
// outside the routes below is refused.
type API struct {
	store *Store
	chain ChainReader
	home  homeCache
	now   func() time.Time
}

// NewAPI returns the HTTP handler over store.
func NewAPI(store *Store, chain ChainReader) *API {
	return &API{store: store, chain: chain, now: time.Now}
}

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
	w.Header().Set("Cache-Control", cacheControl(segs))
	switch {
	case len(segs) == 1 && segs[0] == "status":
		a.serveStatus(w, r, false)
	case len(segs) == 2 && segs[0] == operatorSegment && segs[1] == "status":
		a.serveStatus(w, r, true)
	case len(segs) == 1 && segs[0] == "home":
		a.serveHome(w, r)
	case len(segs) == 1 && segs[0] == "blocks":
		a.serveBlocks(w, r)
	case len(segs) == 2 && segs[0] == "blocks":
		a.serveBlock(w, r, segs[1])
	case len(segs) == 1 && segs[0] == "txs":
		a.serveLatestTxs(w, r)
	case len(segs) == 1 && segs[0] == "stats":
		a.serveStats(w, r)
	case len(segs) == 2 && segs[0] == "txs":
		a.serveTx(w, r, segs[1])
	case len(segs) == 2 && segs[0] == "accounts":
		a.serveAccount(w, r, segs[1])
	case len(segs) == 3 && segs[0] == "accounts" && segs[2] == "txs":
		a.serveAccountTxs(w, r, segs[1])
	case len(segs) == 2 && segs[0] == "shielded" && segs[1] == "actions":
		a.serveShieldedActions(w, r)
	case len(segs) == 2 && segs[0] == "shielded" && segs[1] == "subtree-roots":
		a.serveSubtreeRoots(w, r)
	case len(segs) == 3 && segs[0] == "cnft" && segs[1] == "assets":
		a.serveAsset(w, r, segs[2])
	case len(segs) == 4 && segs[0] == "cnft" && segs[1] == "owners" && segs[3] == "assets":
		a.serveOwnerAssets(w, r, segs[2])
	default:
		if !a.serveAggregates(w, r, segs) {
			writeError(w, http.StatusNotFound, "not found")
		}
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

// operatorSegment prefixes the routes for the node's own operator. The gateway proxies only the
// routes it allowlists (core/pkg/gateway/handlers/chainread/index.go) and lists none under it, so
// they are answered to the processes of the node and never to the public.
const operatorSegment = "operator"

// serveStatus answers the index's heights and, while the follower is stuck, the block it is stuck
// at. The error of the failing pass is the operator's: it names the chain's addresses and the
// node's state, so the public status (the explorer reads it through the gateway) carries only
// whether the state was pruned, and operator/status carries the error as well.
func (a *API) serveStatus(w http.ResponseWriter, r *http.Request, withReason bool) {
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
	out := map[string]any{
		"start_height": st.StartHeight, "cursor": st.Cursor, "earliest": earliest, "tip": tip,
		"aggregates_from": st.AggregatesFrom,
	}
	if st.Stuck != nil {
		stuck := *st.Stuck
		if !withReason {
			stuck.Reason = ""
		}
		out["stuck"] = stuck
	}
	writeJSON(w, out)
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

func (a *API) serveStats(w http.ResponseWriter, r *http.Request) {
	if !noQuery(w, r) {
		return
	}
	hours, err := a.store.Stats()
	if err != nil {
		internalError(w, err)
		return
	}
	st, err := a.store.Status()
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, map[string]any{"start_height": st.StartHeight, "hours": hours})
}

func (a *API) serveAccount(w http.ResponseWriter, r *http.Request, addr string) {
	if !noQuery(w, r) {
		return
	}
	if !IsAccountAddress(addr) {
		writeError(w, http.StatusBadRequest, "address must be a lowercase orama account address")
		return
	}
	sum, found, err := a.store.Account(addr)
	answer(w, sum, found, err)
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

// serveOwnerAssets answers a page of the assets an owner holds, by asset id. The list is an owner's
// to grow, so it is read by page number only up to MaxListPage and past that by cursor (AssetCursor).
func (a *API) serveOwnerAssets(w http.ResponseWriter, r *http.Request, addr string) {
	page, limit, after, ok := keyedQuery(w, r, ParseAssetCursor)
	if !ok {
		return
	}
	if !IsAccountAddress(addr) {
		writeError(w, http.StatusBadRequest, "address must be a lowercase orama account address")
		return
	}
	var assets []Asset
	var err error
	if after != nil {
		assets, err = a.store.OwnerAssetsAfter(addr, *after, limit)
	} else {
		assets, err = a.store.OwnerAssets(addr, page, limit)
	}
	if err != nil {
		internalError(w, err)
		return
	}
	next, err := assetNext(assets, limit)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, keyedAnswer(map[string]any{}, "assets", toDASList(assets), page, limit, after != nil, next))
}

// assetNext is the cursor of the page after assets, or "" when the page is short and so the last.
func assetNext(assets []Asset, limit int) (string, error) {
	if len(assets) == 0 || len(assets) != limit {
		return "", nil
	}
	c, err := assetCursorOf(assets[len(assets)-1])
	return c.String(), err
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
	w.Header().Set("Cache-Control", cacheNoStore)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		slog.Error("failed to write index error", "err", err)
	}
}
