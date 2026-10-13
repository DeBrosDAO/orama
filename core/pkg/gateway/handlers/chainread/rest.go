package chainread

import (
	"net/http"
	"net/url"
)

// The staking validator list from the node's SDK REST API. The wallet queries (query_wallet.go) serve one
// validator, and no query serves the list the explorer's validator page needs, so this one route is
// REST: a fixed upstream path and page size, and no query from the caller.
const (
	upstreamStakingValidators = "/cosmos/staking/v1beta1/validators"
	// restValidatorPageSize is above the largest active set the chain plans (150).
	restValidatorPageSize = "200"

	pathValidators = "staking/validators"
)

// serveREST answers a REST-backed route, and reports whether rest named one.
func (p *Proxy) serveREST(w http.ResponseWriter, r *http.Request, rest string) bool {
	if rest != pathValidators {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return true
	}
	if r.URL.RawQuery != "" {
		writeErr(w, http.StatusBadRequest, "bad query")
		return true
	}
	q := url.Values{}
	q.Set("pagination.limit", restValidatorPageSize)
	p.forward(w, r, p.rest, upstreamStakingValidators, q)
	return true
}
