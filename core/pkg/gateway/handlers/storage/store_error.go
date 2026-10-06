package storage

import (
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// writeStoreError answers a failed read of the namespace's own database (the
// ownership table). One that is not answering right now — no leader, a timeout,
// a refused connection — is a retryable 503: the content is there, and the next
// request can reach the database. It used to be a 500 like any other fault,
// which a client cannot tell from a broken gateway, and a namespace cluster in
// the middle of an election answered every storage call with it. Anything else
// stays a 500 that names what failed and never the driver's text; the cause is
// in the log.
func writeStoreError(w http.ResponseWriter, what string, err error) {
	switch rqlite.ClassifyBatchError(err) {
	case rqlite.BatchCodeUnavailable, rqlite.BatchCodeDeadlineExceeded:
		httputil.WriteRPCError(w, http.StatusServiceUnavailable, httputil.ErrCodeServiceUnavailable,
			what+": the namespace's database is not answering right now; retry shortly", httputil.WithRetryable())
		return
	}
	httputil.WriteRPCError(w, http.StatusInternalServerError, httputil.ErrCodeInternal, what)
}
