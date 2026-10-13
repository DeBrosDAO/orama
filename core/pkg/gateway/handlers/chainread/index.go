package chainread

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Indexer routes: /v1/chain/index/<route> is forwarded to <index>/index/v1/<route>
// after its path segments and query are validated. The bounds are the
// indexer's own (chain/indexer/api.go); the indexer checks them again.
const (
	indexPrefix       = "index/"
	upstreamIndexBase = "/index/v1/"

	// msgNoIndexer answers an index route on a machine with no indexer installed: the route is real,
	// this node cannot serve it, and a later install or another node can.
	msgNoIndexer = "this node runs no chain indexer; install it with orama global install --services chain,indexer"
	// noIndexerRetryAfter is the Retry-After, in seconds, of that answer: a client asks another node,
	// or this one after an install.
	noIndexerRetryAfter = "60"

	indexMaxLimit = 100
	indexMaxPage  = 1000
	// indexMaxActionsLimit and indexMaxSubtreesLimit are the most actions or subtree roots one
	// page of the shielded scanning routes holds (chain/indexer ActionsMaxLimit, SubtreesMaxLimit).
	indexMaxActionsLimit  = 1000
	indexMaxSubtreesLimit = 1000
	// indexMaxListPage is the deepest numbered page of the long lists (blocks, transactions, an
	// account's transactions, an owner's assets, an account's rewards), chain/indexer MaxListPage;
	// past it they are read by cursor.
	indexMaxListPage = 10

	// indexMaxConcurrent bounds the index reads in flight on one gateway. A numbered page makes the
	// indexer skip the rows before it (up to 900 on the long lists, which go deeper by cursor, and up
	// to 99,900 on the short ones, which no account can grow), so the work of one read is not
	// constant; the reads beyond this get a 503 and ask again, as the module queries do.
	indexMaxConcurrent = 32
	// indexBusyRetryAfter is the Retry-After, in seconds, of that answer.
	indexBusyRetryAfter = "2"
)

var (
	// accountPattern is the shape of a lowercase bech32 orama account
	// address of 1 to 255 bytes, the SDK's range. The indexer verifies the
	// checksum; this module does not import the chain's bech32 code.
	accountPattern = regexp.MustCompile(`^orama1[02-9ac-hj-np-z]{8,414}$`)
	hash32Pattern  = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	// validatorPattern is the shape of a lowercase bech32 orama validator operator address, the
	// account range under the oramavaloper prefix. The indexer verifies the checksum.
	validatorPattern = regexp.MustCompile(`^oramavaloper1[02-9ac-hj-np-z]{8,414}$`)
)

// serveIndex matches one indexer route. Each route builds its own upstream
// path from values it has validated; the caller's path is never forwarded.
func (p *Proxy) serveIndex(w http.ResponseWriter, r *http.Request, rest string) {
	upstream, kind, ok := indexRoute(strings.Split(rest, "/"))
	if !ok {
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
	var q url.Values
	switch kind {
	case queryPaged:
		q, ok = pageQuery(r)
	case queryBlockList:
		q, ok = cursorListQuery(r, cursorPattern)
	case queryAssetList:
		q, ok = cursorListQuery(r, assetCursorPattern)
	case queryRewardList:
		q, ok = cursorListQuery(r, rewardCursorPattern)
	case queryTxList:
		q, ok = txListQuery(r)
	case queryShieldedActions:
		q, ok = shieldedActionsQuery(r)
	case queryShieldedSubtrees:
		q, ok = shieldedSubtreesQuery(r)
	default:
		ok = r.URL.RawQuery == ""
	}
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	if !p.indexInstalled() {
		w.Header().Set("Retry-After", noIndexerRetryAfter)
		writeErr(w, http.StatusServiceUnavailable, msgNoIndexer)
		return
	}
	select {
	case p.indexSlots <- struct{}{}:
		defer func() { <-p.indexSlots }()
	default:
		w.Header().Set("Retry-After", indexBusyRetryAfter)
		writeErr(w, http.StatusServiceUnavailable, "the chain index is busy; try again")
		return
	}
	p.forwardIndex(w, r, upstreamIndexBase+upstream, q)
}

// cacheNoStore is the Cache-Control of every chain answer the proxy does not know to be cacheable.
const cacheNoStore = "no-store"

// indexMaxCacheAge is the longest, in seconds, the proxy lets a copy of an indexer answer be kept: an hour,
// whatever the indexer writes.
const indexMaxCacheAge = 3600

// cacheAgePattern is the one form of Cache-Control the indexer writes on a successful answer
// (chain/indexer/cache.go).
var cacheAgePattern = regexp.MustCompile(`^public, max-age=([1-9][0-9]{0,3})$`)

// indexCacheControl is what the indexer said about how long its answer may be kept, if it said it in
// the form it writes and within indexMaxCacheAge; otherwise the answer is not cacheable. A header a
// compromised or misconfigured upstream could send in another form is not passed on.
func indexCacheControl(resp *http.Response) string {
	if resp.StatusCode != http.StatusOK {
		return cacheNoStore
	}
	m := cacheAgePattern.FindStringSubmatch(resp.Header.Get("Cache-Control"))
	if m == nil {
		return cacheNoStore
	}
	if n, err := strconv.Atoi(m[1]); err != nil || n > indexMaxCacheAge {
		return cacheNoStore
	}
	return m[0]
}

// forwardIndex relays an indexer answer and, unlike the other routes, lets a successful one be cached for
// as long as the indexer says: a page of a closed epoch is the same for everyone who asks.
func (p *Proxy) forwardIndex(w http.ResponseWriter, r *http.Request, path string, q url.Values) {
	p.relay(w, r, p.index, path, q, indexCacheControl)
}

// What query an index route takes: none; page and limit; page or cursor and limit (the block list,
// an owner's assets, an account's rewards, each with its own cursor); or the transaction lists'
// page or cursor, limit and a filter.
const (
	queryNone = iota
	queryPaged
	queryBlockList
	queryAssetList
	queryRewardList
	queryTxList
	queryShieldedActions
	queryShieldedSubtrees
)

// indexRoute returns the upstream path after /index/v1/ and the query the route
// takes. A segment that fails validation is no route.
func indexRoute(segs []string) (string, int, bool) {
	switch {
	case len(segs) == 1 && segs[0] == "status":
		return "status", queryNone, true
	case len(segs) == 1 && segs[0] == "stats":
		return "stats", queryNone, true
	case len(segs) == 1 && segs[0] == "home":
		return "home", queryNone, true
	case len(segs) == 1 && segs[0] == "blocks":
		return "blocks", queryBlockList, true
	case len(segs) == 1 && segs[0] == "txs":
		return "txs", queryTxList, true
	case len(segs) == 2 && segs[0] == "blocks":
		h, ok := parsePositive(segs[1])
		return "blocks/" + strconv.FormatInt(h, 10), queryNone, ok
	case len(segs) == 2 && segs[0] == "txs":
		hash, ok := parseTxHash(segs[1])
		return "txs/" + hash, queryNone, ok
	case len(segs) == 2 && segs[0] == "accounts":
		return "accounts/" + segs[1], queryNone, accountPattern.MatchString(segs[1])
	case len(segs) == 3 && segs[0] == "accounts" && segs[2] == "txs":
		return "accounts/" + segs[1] + "/txs", queryTxList, accountPattern.MatchString(segs[1])
	case len(segs) == 3 && segs[0] == "accounts" && segs[2] == "rewards":
		return "accounts/" + segs[1] + "/rewards", queryRewardList, accountPattern.MatchString(segs[1])
	case len(segs) == 2 && segs[0] == "shielded" && segs[1] == "actions":
		return "shielded/actions", queryShieldedActions, true
	case len(segs) == 2 && segs[0] == "shielded" && segs[1] == "subtree-roots":
		return "shielded/subtree-roots", queryShieldedSubtrees, true
	case len(segs) == 3 && segs[0] == "cnft" && segs[1] == "assets":
		return "cnft/assets/" + strings.ToLower(segs[2]), queryNone, hash32Pattern.MatchString(segs[2])
	case len(segs) == 4 && segs[0] == "cnft" && segs[1] == "owners" && segs[3] == "assets":
		return "cnft/owners/" + segs[2] + "/assets", queryAssetList, accountPattern.MatchString(segs[2])
	default:
		return aggregateRoute(segs)
	}
}

// validatorLists are the per-validator lists the indexer pages through, and seriesNames the
// statistics series it serves beyond the 48 hourly buckets of /stats.
var (
	validatorLists = map[string]bool{"epochs": true, "slashes": true, "jails": true, "rewards": true}
	seriesNames    = map[string]bool{"hourly": true, "daily": true, "weekly": true}
)

// aggregateRoute matches the routes over the aggregates the indexer computes as it follows the
// chain: epochs, the supply breakdown, validators and the long statistics series. Every list takes
// page and limit; a single epoch or validator takes none.
func aggregateRoute(segs []string) (string, int, bool) {
	switch {
	case len(segs) == 1 && (segs[0] == "epochs" || segs[0] == "supply" || segs[0] == "validators"):
		return segs[0], queryPaged, true
	case len(segs) == 2 && segs[0] == "epochs":
		n, ok := parsePositive(segs[1])
		return "epochs/" + strconv.FormatInt(n, 10), queryNone, ok
	case len(segs) == 3 && segs[0] == "epochs" && (segs[2] == "validators" || segs[2] == "rewards"):
		n, ok := parsePositive(segs[1])
		return "epochs/" + strconv.FormatInt(n, 10) + "/" + segs[2], queryPaged, ok
	case len(segs) == 2 && segs[0] == "validators":
		return "validators/" + segs[1], queryNone, validatorPattern.MatchString(segs[1])
	case len(segs) == 3 && segs[0] == "validators" && validatorLists[segs[2]]:
		return "validators/" + segs[1] + "/" + segs[2], queryPaged, validatorPattern.MatchString(segs[1])
	case len(segs) == 2 && segs[0] == "stats" && seriesNames[segs[1]]:
		return "stats/" + segs[1], queryPaged, true
	default:
		return "", queryNone, false
	}
}

// txListFilters are the values of the filter parameter of the transaction lists (chain/indexer/kinds.go).
var txListFilters = map[string]bool{"all": true, "failed": true, "transfers": true, "staking": true, "storage": true}

// cursorPattern is the form of a list cursor: unpadded base64url of the 12 bytes of a height and a
// position in its block (chain/indexer/cursor.go). The indexer decodes and checks it again.
var cursorPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16}$`)

// assetCursorPattern is the form of an owner-assets cursor: unpadded base64url of the 44 bytes of an
// asset's location, the asset id, the tree and the leaf (chain/indexer/cursor_keys.go).
var assetCursorPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{59}$`)

// rewardCursorPattern is the form of an account-rewards cursor: unpadded base64url of the epoch, 8
// bytes, and a validator operator address of 21 to 427 characters, so 39 to 580 characters
// (chain/indexer/cursor_keys.go). The indexer decodes and checks it again.
var rewardCursorPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{39,580}$`)

// shieldedActionsQuery accepts from_height (a positive height) and limit (1 to indexMaxActionsLimit),
// each at most once, and nothing else. The indexer checks them again.
func shieldedActionsQuery(r *http.Request) (url.Values, bool) {
	q, ok := singleQuery(r, "from_height", "limit")
	if !ok {
		return nil, false
	}
	out := url.Values{}
	if raw := q.Get("from_height"); raw != "" {
		n, good := parsePositive(raw)
		if !good {
			return nil, false
		}
		out.Set("from_height", strconv.FormatInt(n, 10))
	}
	if raw := q.Get("limit"); raw != "" {
		n, good := parsePositive(raw)
		if !good || n > indexMaxActionsLimit {
			return nil, false
		}
		out.Set("limit", strconv.FormatInt(n, 10))
	}
	return out, true
}

// subtreeIndexPattern is a subtree index: a non-negative integer of at most 18 digits.
var subtreeIndexPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})$`)

// shieldedSubtreesQuery accepts start_index (from 0) and limit (1 to indexMaxSubtreesLimit), each at
// most once, and nothing else.
func shieldedSubtreesQuery(r *http.Request) (url.Values, bool) {
	q, ok := singleQuery(r, "start_index", "limit")
	if !ok {
		return nil, false
	}
	out := url.Values{}
	if raw := q.Get("start_index"); raw != "" {
		if !subtreeIndexPattern.MatchString(raw) {
			return nil, false
		}
		out.Set("start_index", raw)
	}
	if raw := q.Get("limit"); raw != "" {
		n, good := parsePositive(raw)
		if !good || n > indexMaxSubtreesLimit {
			return nil, false
		}
		out.Set("limit", strconv.FormatInt(n, 10))
	}
	return out, true
}

// txListQuery accepts the block list's query and filter, each at most once, and nothing else.
func txListQuery(r *http.Request) (url.Values, bool) {
	q, ok := singleQuery(r, "page", "cursor", "limit", "filter")
	if !ok {
		return nil, false
	}
	filter := q.Get("filter")
	if filter != "" && !txListFilters[filter] {
		return nil, false
	}
	out, ok := listValues(q, cursorPattern)
	if !ok {
		return nil, false
	}
	if filter != "" {
		out.Set("filter", filter)
	}
	return out, true
}

// cursorListQuery accepts page (1 to indexMaxListPage) or a cursor of the given shape, and limit,
// each at most once, and nothing else.
func cursorListQuery(r *http.Request, shape *regexp.Regexp) (url.Values, bool) {
	q, ok := singleQuery(r, "page", "cursor", "limit")
	if !ok {
		return nil, false
	}
	return listValues(q, shape)
}

// listValues checks a long list's page, cursor and limit: a page up to indexMaxListPage or a
// cursor, never both, so no read makes the indexer skip more than indexMaxListPage pages of rows.
func listValues(q url.Values, shape *regexp.Regexp) (url.Values, bool) {
	cursor := q.Get("cursor")
	if cursor != "" && (q.Get("page") != "" || !shape.MatchString(cursor)) {
		return nil, false
	}
	out, ok := boundedValues(q, indexMaxListPage)
	if !ok {
		return nil, false
	}
	if cursor != "" {
		out.Set("cursor", cursor)
	}
	return out, true
}

// pageQuery accepts page (1 to indexMaxPage) and limit (1 to indexMaxLimit),
// each at most once, and nothing else. Absent values are left to the indexer.
func pageQuery(r *http.Request) (url.Values, bool) {
	q, ok := singleQuery(r, "page", "limit")
	if !ok {
		return nil, false
	}
	return pageValues(q)
}

// pageValues checks the page and limit among the values of a query.
func pageValues(q url.Values) (url.Values, bool) { return boundedValues(q, indexMaxPage) }

// boundedValues checks the page (1 to maxPage) and limit among the values of a query.
func boundedValues(q url.Values, maxPage int64) (url.Values, bool) {
	out := url.Values{}
	for key, max := range map[string]int64{"page": maxPage, "limit": indexMaxLimit} {
		raw := q.Get(key)
		if raw == "" {
			continue
		}
		n, good := parsePositive(raw)
		if !good || n > max {
			return nil, false
		}
		out.Set(key, strconv.FormatInt(n, 10))
	}
	return out, true
}
