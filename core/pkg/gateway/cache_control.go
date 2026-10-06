package gateway

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
)

const (
	// apiPathPrefix is the path every platform API route lives under. Anything
	// outside it is an app's own content (deployments, custom domains) and is
	// never touched here.
	apiPathPrefix = "/v1/"

	headerCacheControl = "Cache-Control"
	headerPragma       = "Pragma"

	// cacheControlNoStore keeps a response out of every cache, disk caches of
	// browsers and WebViews included: gateway responses carry user data that
	// must not outlive the session on a shared or seized device (bugboard #735).
	cacheControlNoStore = "no-store"
	// pragmaNoCache is the HTTP/1.0 spelling of the same instruction.
	pragmaNoCache = "no-cache"
)

// noStoreDefaultWriter adds Cache-Control: no-store to a response that reached
// its status line without choosing a Cache-Control of its own. The decision is
// taken when the headers are sent rather than before the handler runs, so it
// sees what a handler set with Set and what a reverse-proxy path copied from
// the upstream with Add: a response that already carries a Cache-Control keeps
// exactly that value and gains no second one.
type noStoreDefaultWriter struct {
	http.ResponseWriter
	headerSent bool
}

func (w *noStoreDefaultWriter) applyDefault() {
	if w.headerSent {
		return
	}
	w.headerSent = true
	h := w.Header()
	if len(h.Values(headerCacheControl)) == 0 {
		h.Set(headerCacheControl, cacheControlNoStore)
		h.Set(headerPragma, pragmaNoCache)
	}
}

func (w *noStoreDefaultWriter) WriteHeader(code int) {
	// 1xx are interim responses; the final status line follows.
	if code >= http.StatusOK {
		w.applyDefault()
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *noStoreDefaultWriter) Write(b []byte) (int, error) {
	w.applyDefault()
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the real connection.
func (w *noStoreDefaultWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *noStoreDefaultWriter) Flush() {
	w.applyDefault()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *noStoreDefaultWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("hijacker not supported")
}

// noStoreAPIResponses gives every /v1/* response Cache-Control: no-store (and
// Pragma: no-cache) unless the handler chose its own, as the public status
// endpoints do. Paths outside /v1/ pass through untouched.
func noStoreAPIResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, apiPathPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(&noStoreDefaultWriter{ResponseWriter: w}, r)
	})
}
