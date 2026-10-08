package chainread

import (
	"net/http"
	"net/url"
	"strings"
)

// Account and validator reads from the node's SDK REST API. Each route has one
// fixed upstream path and a fixed page size, and takes no query: the explorer
// asks for what fits on a page, and a caller cannot ask the node to walk more.
const (
	upstreamBalances          = "/cosmos/bank/v1beta1/balances/"
	upstreamStakingValidators = "/cosmos/staking/v1beta1/validators"
	upstreamDelegations       = "/cosmos/staking/v1beta1/delegations/"
	upstreamUnbonding         = "/cosmos/staking/v1beta1/delegators/"
	unbondingSuffix           = "/unbonding_delegations"

	// restAccountPageSize bounds the entries of one account read: a balance has one entry per
	// denom the account holds and a delegation list one per validator.
	restAccountPageSize = "100"
	// restValidatorPageSize is above the largest active set the chain plans (150).
	restValidatorPageSize = "200"

	pathBalances   = "bank/balances/"
	pathValidators = "staking/validators"
	pathDelegation = "staking/delegations/"
	pathUnbonding  = "staking/unbonding/"
)

// serveREST answers a REST-backed account or validator route, and reports whether rest named one.
func (p *Proxy) serveREST(w http.ResponseWriter, r *http.Request, rest string) bool {
	upstream, q, known := restRoute(rest)
	if !known {
		return false
	}
	if upstream == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return true
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
	p.forward(w, r, p.rest, upstream, q)
	return true
}

// restRoute returns the upstream path and query of a REST route. known is false for a path that is
// no REST route at all; a known route whose address is malformed returns an empty upstream.
func restRoute(rest string) (upstream string, q url.Values, known bool) {
	page := func(size string) url.Values {
		v := url.Values{}
		v.Set("pagination.limit", size)
		return v
	}
	account := func(prefix string) (string, bool) {
		addr, ok := strings.CutPrefix(rest, prefix)
		if !ok {
			return "", false
		}
		if !accountPattern.MatchString(addr) {
			return "", true
		}
		return addr, true
	}
	if rest == pathValidators {
		return upstreamStakingValidators, page(restValidatorPageSize), true
	}
	if addr, ok := account(pathBalances); ok {
		if addr == "" {
			return "", nil, true
		}
		return upstreamBalances + addr, page(restAccountPageSize), true
	}
	if addr, ok := account(pathDelegation); ok {
		if addr == "" {
			return "", nil, true
		}
		return upstreamDelegations + addr, page(restAccountPageSize), true
	}
	if addr, ok := account(pathUnbonding); ok {
		if addr == "" {
			return "", nil, true
		}
		return upstreamUnbonding + addr + unbondingSuffix, page(restAccountPageSize), true
	}
	return "", nil, false
}
