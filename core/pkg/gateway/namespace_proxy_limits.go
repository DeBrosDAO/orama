package gateway

import (
	"fmt"
	"net/http"
	"time"

	backuphandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/backup"
	"github.com/DeBrosOfficial/network/pkg/httputil"
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

// extendTransferDeadlines gives a whole-database request its time budget on
// this gateway's server, whose own read and write timeouts (60s and 120s,
// cmd/gateway/main.go) would cut a large database off however long the proxy
// would wait. Any other route keeps the server's. It writes the refusal and
// returns false if the deadlines cannot be moved.
func extendTransferDeadlines(w http.ResponseWriter, r *http.Request) bool {
	if !isWholeDatabasePath(r.URL.Path) {
		return true
	}
	if err := httputil.ExtendIO(w, httputil.TransferBudget); err != nil {
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
