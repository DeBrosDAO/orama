package gateway

import (
	"fmt"
	"net/http"

	backuphandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/backup"
)

// proxyBodyLimit is the largest request body the namespace gateway behind a
// path will accept, or 0 when the proxy sets no limit of its own.
func proxyBodyLimit(path string) int64 {
	if path == "/v1/namespace/restore" {
		return int64(backuphandlers.MaxRestoreBytes)
	}
	return 0
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
