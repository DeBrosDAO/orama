// visits counts visits to a page. The count lives in the namespace's cache and
// every visit is one cache_incr_by host function call: an atomic add on the key's
// partition owner, so any number of visitors, on any of the namespace's
// gateways, are all counted and none is counted twice.
package main

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"orama-demo/host"
)

const (
	// defaultPage is counted when the request names none.
	defaultPage = "home"
	// keyPrefix keeps the counters of this function apart from any other key of
	// the namespace's cache.
	keyPrefix = "visits:"
	// actionHit counts a visit; actionPeek reads the count without counting.
	actionHit  = "hit"
	actionPeek = "peek"
	// peekDelta adds nothing, which is how cache_incr_by reads.
	peekDelta = 0
	// hitDelta is one visit.
	hitDelta = 1
)

// pagePattern is a page name: it becomes part of a cache key.
var pagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

type request struct {
	Page   string `json:"page"`
	Action string `json:"action"`
}

type response struct {
	Page   string `json:"page"`
	Visits int64  `json:"visits"`
}

type problem struct {
	Error string `json:"error"`
}

func main() {
	h := host.New()
	host.Run(func(input []byte) ([]byte, error) { return handle(h, input) })
}

// handle answers one invocation.
func handle(h host.Host, input []byte) ([]byte, error) {
	var req request
	if len(strings.TrimSpace(string(input))) > 0 {
		if err := json.Unmarshal(input, &req); err != nil {
			return json.Marshal(problem{Error: "the input is not JSON like {\"page\": \"home\"}"})
		}
	}
	page := req.Page
	if page == "" {
		page = defaultPage
	}
	if !pagePattern.MatchString(page) {
		return json.Marshal(problem{Error: "page must be 1 to 32 characters of a-z, 0-9, - and _"})
	}
	switch req.Action {
	case "", actionHit:
		return count(h, page)
	case actionPeek:
		return json.Marshal(response{Page: page, Visits: h.CacheIncrBy(keyPrefix+page, peekDelta)})
	default:
		return json.Marshal(problem{Error: "action must be \"hit\" or \"peek\""})
	}
}

// count adds a visit. The runtime answers 0 for a failed call and the first
// visit makes 1, so 0 here is a failure, not a count.
func count(h host.Host, page string) ([]byte, error) {
	n := h.CacheIncrBy(keyPrefix+page, hitDelta)
	if n == 0 {
		return nil, errors.New("the cache did not count the visit (cache_incr_by failed; the gateway log says why)")
	}
	h.LogInfo("visit " + page)
	return json.Marshal(response{Page: page, Visits: n})
}
