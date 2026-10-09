package gateway

import (
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// A wallet with no node of its own simulates and broadcasts through POST /v1/chain/simulate and
// /v1/chain/broadcast (handlers/chainread/tx.go). The chain makes a spam transaction cost its
// sender a fee; what the gateway has to bound is the load, so each route has two buckets of its
// own, apart from the general one and from the module-query bucket: one per client network, and
// one for the whole route on this gateway, so many addresses together cannot lift the load either.
// A broadcast is a write that reaches every validator's mempool, so its buckets are the tighter.
const (
	chainSimulatePath  = "/v1/chain/simulate"
	chainBroadcastPath = "/v1/chain/broadcast"

	chainSimulatePerAddressPerMinute = 30
	chainSimulatePerAddressBurst     = 10
	chainSimulateRoutePerMinute      = 1200
	chainSimulateRouteBurst          = 200

	chainBroadcastPerAddressPerMinute = 12
	chainBroadcastPerAddressBurst     = 4
	chainBroadcastRoutePerMinute      = 600
	chainBroadcastRouteBurst          = 100

	chainTxRetryAfterSeconds = 10

	// chainTxRouteKey is the single key the route-wide bucket is held under.
	chainTxRouteKey = "route"
)

// chainTxLimiter is the two buckets of one transaction route.
type chainTxLimiter struct {
	perAddress *RateLimiter
	route      *RateLimiter
}

func newChainTxLimiter(addrPerMinute, addrBurst, routePerMinute, routeBurst int) *chainTxLimiter {
	l := &chainTxLimiter{
		perAddress: NewRateLimiter(addrPerMinute, addrBurst),
		route:      NewRateLimiter(routePerMinute, routeBurst),
	}
	l.perAddress.StartCleanup(5*time.Minute, 10*time.Minute)
	return l
}

// allow takes a token from the client's bucket and then from the route's. A request refused by the
// client's bucket takes nothing from the route's, so one noisy address cannot use up the route.
func (l *chainTxLimiter) allow(client string) bool {
	return l.perAddress.Allow(client) && l.route.Allow(chainTxRouteKey)
}

// chainTxLimiterFor returns the limiter of the transaction route r is for, or nil when it is
// for neither. Only a POST draws on it: nothing else is served on these paths.
func (g *Gateway) chainTxLimiterFor(r *http.Request) *chainTxLimiter {
	if r.Method != http.MethodPost {
		return nil
	}
	switch r.URL.Path {
	case chainSimulatePath:
		return g.chainSimulateLimiter
	case chainBroadcastPath:
		return g.chainBroadcastLimiter
	}
	return nil
}

func writeChainTxRateLimited(w http.ResponseWriter) {
	httputil.WriteRPCError(w, http.StatusTooManyRequests,
		httputil.ErrCodeRateLimited,
		"too many chain transactions from this address — wait a moment and try again",
		httputil.WithRetryable(),
		httputil.WithRetryAfter(chainTxRetryAfterSeconds))
}
