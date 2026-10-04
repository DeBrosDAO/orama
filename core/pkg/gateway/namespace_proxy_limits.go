package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"

	backuphandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/backup"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// longProxyTimeout is the proxy's whole-request budget for the routes
// isLongRunningProxyPath names.
const longProxyTimeout = 300 * time.Second

// proxyBodyLimit is the largest request body the namespace gateway behind a
// path will accept, or 0 when the proxy sets no limit of its own.
func proxyBodyLimit(path string) int64 {
	switch path {
	case "/v1/namespace/restore":
		return int64(backuphandlers.MaxRestoreBytes)
	case "/v1/rqlite/import":
		return rqliteImportMaxBytes
	}
	return 0
}

// isWholeDatabasePath reports whether a path moves a whole database: a
// namespace backup or restore, an RQLite export or import. They are the routes
// that get the long proxy budget and the extended read and write deadlines.
func isWholeDatabasePath(p string) bool {
	switch p {
	case "/v1/namespace/backup", "/v1/namespace/restore", "/v1/rqlite/export", "/v1/rqlite/import":
		return true
	}
	return false
}

// transferBudget is the time a long-running request is given on this
// gateway's server. A variable so a test can shorten it.
var transferBudget = httputil.TransferBudget

// renewTransferDeadlines gives the response of a whole-database request its own
// budget once the work is done: a long check, load and scrub may have spent
// the one the request started with, and a client must not be told a success
// failed because the write that reports it was cut off.
func (g *Gateway) renewTransferDeadlines(w http.ResponseWriter) {
	if err := httputil.ExtendIO(w, transferBudget); err != nil {
		g.logger.ComponentWarn(logging.ComponentGeneral,
			"could not renew the response deadline; the client may not see the answer", zap.Error(err))
	}
}

// extendTransferDeadlines gives a long-running request (isLongRunningProxyPath:
// a whole database, a storage upload or pin, a function deploy or invocation)
// its time budget on this gateway's server, whose own read and write timeouts
// (60s and 120s, cmd/gateway/main.go) would cut it off however long the proxy
// would wait: a 20 MiB upload from a client on a 2 Mbit/s uplink had its body
// cut off at 60s and was answered 504 "did not answer within the proxy budget
// (5m0s)". Any other route keeps the server's. It writes the refusal and
// returns false if the deadlines cannot be moved.
func extendTransferDeadlines(w http.ResponseWriter, r *http.Request) bool {
	if !isLongRunningProxyPath(r.URL.Path) {
		return true
	}
	if err := httputil.ExtendIO(w, transferBudget); err != nil {
		writeError(w, http.StatusInternalServerError, "the transfer could not be given its time budget: "+err.Error())
		return false
	}
	return true
}

// refuseOversizedProxyBody answers 413 when the request announces a body over
// the limit of its path, before a byte of it is sent on, and reports whether it
// did.
//
// Without it a 256 MiB restore was streamed to the namespace gateway until the
// proxy's whole-request budget ran out and the caller was told 504, a timeout
// for a request that was never going to be accepted. A request without a
// Content-Length is still bounded by the namespace gateway's own reader.
func refuseOversizedProxyBody(w http.ResponseWriter, r *http.Request) bool {
	limit := proxyBodyLimit(r.URL.Path)
	if limit == 0 || r.ContentLength <= limit {
		return false
	}
	writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the request is %d bytes; %s accepts at most %d", r.ContentLength, r.URL.Path, limit))
	return true
}

// longRequestDeadlines gives the long-running routes this gateway serves
// itself their time budget (extendTransferDeadlines), for an authenticated
// caller only. It sits innermost, after auth, and an anonymous call to a
// public route (a function invocation) keeps the server's 60s and 120s: a
// client with no credential must not hold a connection five times longer. A
// request this gateway proxies to a namespace gateway gets it in the proxy,
// on the same condition.
func longRequestDeadlines(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if callerAuthenticated(r) && !extendTransferDeadlines(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// callerAuthenticated reports whether auth validated a credential for r: a
// JWT, an API key, or a namespace another gateway of this cluster vouched for
// (internalAuthMiddleware deleted the header unless its MAC verified).
func callerAuthenticated(r *http.Request) bool {
	ctx := r.Context()
	if claims, _ := ctx.Value(ctxKeyJWT).(*auth.JWTClaims); claims != nil {
		return true
	}
	if ctx.Value(ctxKeyAPIKey) != nil {
		return true
	}
	return r.Header.Get(HeaderInternalAuthValidated) == "true" &&
		strings.TrimSpace(r.Header.Get(HeaderInternalAuthNamespace)) != ""
}
