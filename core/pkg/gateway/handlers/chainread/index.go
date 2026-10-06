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

	indexMaxLimit = 100
	indexMaxPage  = 1000
)

var (
	// accountPattern is the shape of a lowercase bech32 orama account
	// address of 1 to 255 bytes, the SDK's range. The indexer verifies the
	// checksum; this module does not import the chain's bech32 code.
	accountPattern = regexp.MustCompile(`^orama1[02-9ac-hj-np-z]{8,414}$`)
	hash32Pattern  = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

// serveIndex matches one indexer route. Each route builds its own upstream
// path from values it has validated; the caller's path is never forwarded.
func (p *Proxy) serveIndex(w http.ResponseWriter, r *http.Request, rest string) {
	upstream, paged, ok := indexRoute(strings.Split(rest, "/"))
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
	if paged {
		if q, ok = pageQuery(r); !ok {
			writeErr(w, http.StatusBadRequest, "bad query")
			return
		}
	} else if r.URL.RawQuery != "" {
		writeErr(w, http.StatusBadRequest, "bad query")
		return
	}
	p.forward(w, r, p.index, upstreamIndexBase+upstream, q)
}

// indexRoute returns the upstream path after /index/v1/ and whether the
// route takes page and limit. A segment that fails validation is no route.
func indexRoute(segs []string) (string, bool, bool) {
	switch {
	case len(segs) == 1 && segs[0] == "status":
		return "status", false, true
	case len(segs) == 2 && segs[0] == "blocks":
		h, ok := parsePositive(segs[1])
		return "blocks/" + strconv.FormatInt(h, 10), false, ok
	case len(segs) == 2 && segs[0] == "txs":
		hash, ok := parseTxHash(segs[1])
		return "txs/" + hash, false, ok
	case len(segs) == 3 && segs[0] == "accounts" && segs[2] == "txs":
		return "accounts/" + segs[1] + "/txs", true, accountPattern.MatchString(segs[1])
	case len(segs) == 3 && segs[0] == "cnft" && segs[1] == "assets":
		return "cnft/assets/" + strings.ToLower(segs[2]), false, hash32Pattern.MatchString(segs[2])
	case len(segs) == 4 && segs[0] == "cnft" && segs[1] == "owners" && segs[3] == "assets":
		return "cnft/owners/" + segs[2] + "/assets", true, accountPattern.MatchString(segs[2])
	default:
		return "", false, false
	}
}

// pageQuery accepts page (1 to indexMaxPage) and limit (1 to indexMaxLimit),
// each at most once, and nothing else. Absent values are left to the indexer.
func pageQuery(r *http.Request) (url.Values, bool) {
	q, ok := singleQuery(r, "page", "limit")
	if !ok {
		return nil, false
	}
	out := url.Values{}
	for key, max := range map[string]int64{"page": indexMaxPage, "limit": indexMaxLimit} {
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
