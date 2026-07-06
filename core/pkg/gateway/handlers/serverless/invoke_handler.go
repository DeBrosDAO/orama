package serverless

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// classifyInvokeError maps a function-invoke error to the canonical
// (HTTP status, RPC code, retryable) triple. Shared by the HTTP and WS invoke
// paths so both transports report identical, machine-readable error semantics.
// A cold-WASM fetch timeout is a transient infra failure (retryable); a genuine
// error raised inside the function is FUNCTION_EXECUTION_FAILED (not retryable).
func classifyInvokeError(err error) (int, httputil.RPCErrorCode, bool) {
	switch {
	case errors.Is(err, serverless.ErrWASMFetchTimeout):
		return http.StatusServiceUnavailable, httputil.ErrCodeFunctionUnavailable, true
	case serverless.IsNotFound(err):
		return http.StatusNotFound, httputil.ErrCodeNotFound, false
	case serverless.IsResourceExhausted(err):
		return http.StatusTooManyRequests, httputil.ErrCodeRateLimited, true
	case serverless.IsUnauthorized(err):
		return http.StatusUnauthorized, httputil.ErrCodeUnauthorized, false
	default:
		return http.StatusInternalServerError, httputil.ErrCodeFunctionExecution, false
	}
}

// extractRemoteIP returns a best-effort source IP for the request.
// Trusts X-Real-IP / X-Forwarded-For only when the immediate peer is loopback
// or a private address (i.e. behind our own reverse proxy / SNI router).
func extractRemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	trustHeaders := peer != nil && (peer.IsLoopback() || peer.IsPrivate())
	if trustHeaders {
		if v := r.Header.Get("X-Real-IP"); v != "" {
			return strings.TrimSpace(v)
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			// First entry is the original client.
			if comma := strings.IndexByte(v, ','); comma >= 0 {
				v = v[:comma]
			}
			return strings.TrimSpace(v)
		}
	}
	return host
}

// InvokeFunction handles POST /v1/functions/{name}/invoke
// Invokes a function with the provided input.
func (h *ServerlessHandlers) InvokeFunction(w http.ResponseWriter, r *http.Request, nameWithNS string, version int) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse namespace and name
	var namespace, name string
	if idx := strings.Index(nameWithNS, "/"); idx > 0 {
		namespace = nameWithNS[:idx]
		name = nameWithNS[idx+1:]
	} else {
		name = nameWithNS
		namespace = r.URL.Query().Get("namespace")
		if namespace == "" {
			namespace = h.getNamespaceFromRequest(r)
		}
	}

	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	// Read input body
	input, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	// Get caller wallet from JWT
	callerWallet := h.getWalletFromRequest(r)

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	req := &serverless.InvokeRequest{
		Namespace:        namespace,
		FunctionName:     name,
		Version:          version,
		Input:            input,
		TriggerType:      serverless.TriggerTypeHTTP,
		CallerWallet:     callerWallet,
		CallerIsAdmin:    h.getCallerIsAdminFromRequest(r),
		CallerIP:         extractRemoteIP(r),
		CallerClaims:     h.getCallerClaimsFromRequest(r),
		CallerJWTSubject: h.getJWTSubjectFromRequest(r),
	}

	resp, err := h.invoker.Invoke(ctx, req)
	if err != nil {
		// Bug #212: every error path here emits the canonical RPC
		// envelope. error.message is always populated (falls back to
		// err.Error() then to a default per code).

		// Rate-limit errors carry a retry hint we surface as both the
		// HTTP Retry-After header and the envelope field.
		var rle *serverless.RateLimitedError
		if errors.As(err, &rle) {
			opts := []httputil.RPCErrorOption{}
			if rle.RetryAfter > 0 {
				opts = append(opts, httputil.WithRetryAfter(rle.RetryAfter.Seconds()))
			}
			if resp != nil && resp.RequestID != "" {
				opts = append(opts, httputil.WithRequestID(resp.RequestID))
			}
			writeRPCError(w, http.StatusTooManyRequests,
				httputil.ErrCodeRateLimited, err.Error(), opts...)
			return
		}

		// Map domain-typed errors to (status, RPC code). WriteRPCError seeds the
		// retryable bit from defaultRetryableFor(code), so a cold-WASM
		// FUNCTION_UNAVAILABLE comes back retryable automatically.
		statusCode, errCode, _ := classifyInvokeError(err)

		// Pick the most informative message: function-side resp.Error
		// (if set) is more actionable than the wrapping err.Error().
		msg := err.Error()
		if resp != nil && resp.Error != "" {
			msg = resp.Error
		}
		opts := []httputil.RPCErrorOption{}
		if resp != nil && resp.RequestID != "" {
			opts = append(opts, httputil.WithRequestID(resp.RequestID))
		}
		writeRPCError(w, statusCode, errCode, msg, opts...)
		return
	}

	// Return the function's output directly if it's JSON
	w.Header().Set("X-Request-ID", resp.RequestID)
	w.Header().Set("X-Duration-Ms", strconv.FormatInt(resp.DurationMS, 10))

	// Raw-HTTP-response mode (bugboard #835): when a function deployed with
	// raw_http_response actually set a response via set_http_response, replay
	// it verbatim (status + headers + body) and skip the sniff/wrap path. If
	// the function set nothing, RawHTTP is nil and we fall through to the
	// normal behavior unchanged.
	if resp.RawHTTP != nil {
		for k, v := range resp.RawHTTP.Headers {
			// A tenant function must not overwrite gateway-owned trace/auth
			// headers or framing-control (hop-by-hop) headers via its raw
			// response — that would let it forge request IDs, leak/spoof
			// internal-auth headers, or corrupt response framing.
			if isReservedResponseHeader(k) {
				continue
			}
			w.Header().Set(k, v)
		}
		w.WriteHeader(resp.RawHTTP.Status)
		w.Write(resp.RawHTTP.Body)
		return
	}

	// Try to detect if output is JSON
	if len(resp.Output) > 0 && (resp.Output[0] == '{' || resp.Output[0] == '[') {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(resp.Output)
	} else {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"request_id":  resp.RequestID,
			"output":      string(resp.Output),
			"status":      resp.Status,
			"duration_ms": resp.DurationMS,
		})
	}
}

// HandleInvoke handles POST /v1/invoke/{namespace}/{name}[@version]
// Direct invocation endpoint with namespace in path.
func (h *ServerlessHandlers) HandleInvoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse path: /v1/invoke/{namespace}/{name}[@version]
	path := strings.TrimPrefix(r.URL.Path, "/v1/invoke/")
	parts := strings.SplitN(path, "/", 2)

	if len(parts) < 2 {
		http.Error(w, "Path must be /v1/invoke/{namespace}/{name}", http.StatusBadRequest)
		return
	}

	namespace := parts[0]
	name := parts[1]

	// Parse version if present
	version := 0
	if idx := strings.Index(name, "@"); idx > 0 {
		vStr := name[idx+1:]
		name = name[:idx]
		if v, err := strconv.Atoi(vStr); err == nil {
			version = v
		}
	}

	h.InvokeFunction(w, r, namespace+"/"+name, version)
}

// GetFunctionInfo handles GET /v1/functions/{name}
// Returns detailed information about a specific function.
func (h *ServerlessHandlers) GetFunctionInfo(w http.ResponseWriter, r *http.Request, name string, version int) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = h.getNamespaceFromRequest(r)
	}

	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	fn, err := h.registry.Get(ctx, namespace, name, version)
	if err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to get function")
		}
		return
	}

	writeJSON(w, http.StatusOK, fn)
}

// ListVersions handles GET /v1/functions/{name}/versions
// Lists all versions of a specific function.
func (h *ServerlessHandlers) ListVersions(w http.ResponseWriter, r *http.Request, name string) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = h.getNamespaceFromRequest(r)
	}

	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Get registry with extended methods
	reg, ok := h.registry.(*serverless.Registry)
	if !ok {
		writeError(w, http.StatusNotImplemented, "Version listing not supported")
		return
	}

	versions, err := reg.ListVersions(ctx, namespace, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list versions")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"versions": versions,
		"count":    len(versions),
	})
}

// reservedResponseHeaders are response headers a raw-HTTP-response tenant
// function (bugboard #835) must not be able to set or overwrite: gateway-owned
// trace/auth headers and hop-by-hop / framing-control headers. Compared
// case-insensitively; the X-Internal- prefix is matched separately.
var reservedResponseHeaders = map[string]struct{}{
	"x-request-id":        {},
	"x-duration-ms":       {},
	"content-length":      {},
	"transfer-encoding":   {},
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"upgrade":             {},
}

// isReservedResponseHeader reports whether a tenant-supplied response header key
// is reserved for the gateway and must be ignored in raw-HTTP-response mode.
func isReservedResponseHeader(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if _, ok := reservedResponseHeaders[k]; ok {
		return true
	}
	// Any internal-auth header the gateway uses for inter-service trust.
	return strings.HasPrefix(k, "x-internal-")
}
