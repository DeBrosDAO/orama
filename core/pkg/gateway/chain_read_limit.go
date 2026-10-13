package gateway

import (
	"net/http"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// The limits of the two chain reads that cost more than their size: a head stream, which holds a
// connection, and a read of the chain indexer, which can cost a scan. Each has a bucket for how fast a
// client network makes them and a count of how many it has open at once. The constants are in
// rate_limit_key.go.

// writeChainReadLimited answers a 429 in the RPC error envelope.
func writeChainReadLimited(w http.ResponseWriter, retryAfter int, message string) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	httputil.WriteRPCError(w, http.StatusTooManyRequests,
		httputil.ErrCodeRateLimited, message,
		httputil.WithRetryable(), httputil.WithRetryAfter(float64(retryAfter)))
}

// chainReadBucketRefused takes a token from the bucket of the chain read r is, if it is one, and
// answers 429 and reports true when the bucket is empty.
func (g *Gateway) chainReadBucketRefused(w http.ResponseWriter, r *http.Request, ip string) bool {
	switch {
	case g.chainHeadRateLimiter != nil && r.URL.Path == chainHeadStreamPath:
		if !g.chainHeadRateLimiter.Allow(ip) {
			writeChainReadLimited(w, 10, "too many chain streams from this address — wait a moment and try again")
			return true
		}
	case g.chainIndexRateLimiter != nil && isChainIndexPath(r.URL.Path):
		if !g.chainIndexRateLimiter.Allow(ip) {
			writeChainReadLimited(w, 10, "too many chain index reads from this address — wait a moment and try again")
			return true
		}
	}
	return false
}

// holdChainRead takes a hold for the client on the chain read r is, if it is one. It returns the
// function that gives the hold back, which the caller defers, and false after answering 429 when the
// client already holds its share.
func (g *Gateway) holdChainRead(w http.ResponseWriter, r *http.Request, ip string) (func(), bool) {
	var holds *holdLimiter
	var retryAfter int
	var message string
	switch {
	case g.chainHeadHolds != nil && r.URL.Path == chainHeadStreamPath:
		holds, retryAfter, message = g.chainHeadHolds, 10, "too many chain streams are open from this address — close one and try again"
	case g.chainIndexHolds != nil && isChainIndexPath(r.URL.Path):
		holds, retryAfter, message = g.chainIndexHolds, 2, "too many chain index reads are in flight from this address — wait a moment and try again"
	default:
		return func() {}, true
	}
	if !holds.acquire(ip) {
		writeChainReadLimited(w, retryAfter, message)
		return nil, false
	}
	return func() { holds.release(ip) }, true
}
