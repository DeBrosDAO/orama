package serverless

import (
	"context"
	"fmt"
	"sync"
)

// Raw-HTTP-response mode (bugboard #835).
//
// A function deployed with RawHTTPResponse=true can emit a verbatim HTTP
// response (status + headers + body) instead of the JSON/Ack-wrapped output
// the stateless invoke handler normally produces. This lets a namespace app
// proxy an upstream RPC (Helius / Alchemy) transparently — the function reads
// the request, calls the upstream, and replays the upstream's status, headers,
// and body byte-for-byte back to its own caller.
//
// The primitive provided here is ONLY the response carrier + the host-call
// validation. Per-user-JWT quota gating (which the ticket mentions) is the
// APP's responsibility: the function can call oh.GetCallerJwtSubject() and
// decide whether to serve. The gateway does not implement quota here.

const (
	// rawHTTPMaxHeaders caps how many response headers a function may set.
	// Generous for a proxy use-case (upstream RPCs return well under this)
	// while bounding the per-invocation allocation a hostile function could
	// force.
	rawHTTPMaxHeaders = 64

	// rawHTTPMaxBodyBytes caps the verbatim response body a function may set.
	// 8 MiB comfortably covers JSON-RPC responses (even large getBlock /
	// getProgramAccounts payloads) without letting a function buffer an
	// unbounded body in gateway memory.
	rawHTTPMaxBodyBytes = 8 << 20

	// rawHTTPMinStatus / rawHTTPMaxStatus bound a valid HTTP status code.
	rawHTTPMinStatus = 100
	rawHTTPMaxStatus = 599
)

// RawHTTPResult is a verbatim HTTP response set by a RawHTTPResponse function.
// Set is true once the function has called set_http_response at least once;
// the invoke handler only takes the raw path when Set is true (otherwise it
// falls back to the normal JSON/Ack-wrapped behavior).
type RawHTTPResult struct {
	Status  int
	Headers map[string]string
	Body    []byte
	Set     bool
}

// rawHTTPCollector is the mutable per-invocation sink the set_http_response
// host function writes to. It rides the invocation's context (same per-call
// propagation model as the publish counter and log buffer) so concurrent
// invocations never cross-write each other's response.
type rawHTTPCollector struct {
	mu     sync.Mutex
	result RawHTTPResult
}

// rawHTTPKey is the unexported context-value key for the raw-HTTP collector.
type rawHTTPKey struct{}

// WithRawHTTPCollector returns a derived ctx carrying a FRESH per-invocation
// raw-HTTP response collector. The engine attaches this before executing a
// RawHTTPResponse function so the set_http_response host call has somewhere to
// write; for non-raw functions the collector is absent and the host call is a
// validated no-op.
func WithRawHTTPCollector(ctx context.Context) context.Context {
	return context.WithValue(ctx, rawHTTPKey{}, &rawHTTPCollector{})
}

// rawHTTPCollectorFromCtx extracts the collector attached via
// WithRawHTTPCollector, or nil if none is present (non-raw function, or an
// untracked code path).
func rawHTTPCollectorFromCtx(ctx context.Context) *rawHTTPCollector {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(rawHTTPKey{}).(*rawHTTPCollector)
	return c
}

// SetRawHTTPResponse records a verbatim HTTP response on the invocation's
// collector. Returns an error if no collector is attached (the function was
// not deployed with RawHTTPResponse), or if the status / header count / body
// size fail validation. Headers may be nil. The body is copied so the caller
// (which reads it out of guest WASM memory) may reuse its buffer.
func SetRawHTTPResponse(ctx context.Context, status int, headers map[string]string, body []byte) error {
	c := rawHTTPCollectorFromCtx(ctx)
	if c == nil {
		return fmt.Errorf("set_http_response: function is not deployed with raw_http_response enabled")
	}
	if status < rawHTTPMinStatus || status > rawHTTPMaxStatus {
		return fmt.Errorf("set_http_response: status %d out of range [%d,%d]", status, rawHTTPMinStatus, rawHTTPMaxStatus)
	}
	if len(headers) > rawHTTPMaxHeaders {
		return fmt.Errorf("set_http_response: too many headers (%d > %d)", len(headers), rawHTTPMaxHeaders)
	}
	if len(body) > rawHTTPMaxBodyBytes {
		return fmt.Errorf("set_http_response: body too large (%d bytes > %d)", len(body), rawHTTPMaxBodyBytes)
	}

	bodyCopy := make([]byte, len(body))
	copy(bodyCopy, body)

	var hdrCopy map[string]string
	if len(headers) > 0 {
		hdrCopy = make(map[string]string, len(headers))
		for k, v := range headers {
			hdrCopy[k] = v
		}
	}

	c.mu.Lock()
	c.result = RawHTTPResult{
		Status:  status,
		Headers: hdrCopy,
		Body:    bodyCopy,
		Set:     true,
	}
	c.mu.Unlock()
	return nil
}

// TakeRawHTTPResponse returns the raw HTTP response recorded on the ctx's
// collector and whether one was set. Returns (zero, false) when no collector
// is attached or the function never called set_http_response. The engine calls
// this after Execute to surface the response on the InvokeResponse.
func TakeRawHTTPResponse(ctx context.Context) (RawHTTPResult, bool) {
	c := rawHTTPCollectorFromCtx(ctx)
	if c == nil {
		return RawHTTPResult{}, false
	}
	c.mu.Lock()
	res := c.result
	c.mu.Unlock()
	if !res.Set {
		return RawHTTPResult{}, false
	}
	return res, true
}
