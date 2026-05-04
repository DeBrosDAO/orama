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

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

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
		Namespace:    namespace,
		FunctionName: name,
		Version:      version,
		Input:        input,
		TriggerType:  serverless.TriggerTypeHTTP,
		CallerWallet: callerWallet,
		CallerIP:     extractRemoteIP(r),
		CallerClaims: h.getCallerClaimsFromRequest(r),
	}

	resp, err := h.invoker.Invoke(ctx, req)
	if err != nil {
		statusCode := http.StatusInternalServerError
		// Tiered rate limiter returns *RateLimitedError with retry-after.
		var rle *serverless.RateLimitedError
		if errors.As(err, &rle) {
			if rle.RetryAfter > 0 {
				w.Header().Set("Retry-After",
					strconv.FormatFloat(rle.RetryAfter.Seconds(), 'f', 1, 64))
			}
			writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"error":       err.Error(),
				"scope":       rle.Scope,
				"retry_after": rle.RetryAfter.Seconds(),
			})
			return
		}
		if serverless.IsNotFound(err) {
			statusCode = http.StatusNotFound
		} else if serverless.IsResourceExhausted(err) {
			statusCode = http.StatusTooManyRequests
		} else if serverless.IsUnauthorized(err) {
			statusCode = http.StatusUnauthorized
		}

		if resp == nil {
			writeJSON(w, statusCode, map[string]interface{}{
				"error": err.Error(),
			})
			return
		}

		writeJSON(w, statusCode, map[string]interface{}{
			"request_id":  resp.RequestID,
			"status":      resp.Status,
			"error":       resp.Error,
			"duration_ms": resp.DurationMS,
		})
		return
	}

	// Return the function's output directly if it's JSON
	w.Header().Set("X-Request-ID", resp.RequestID)
	w.Header().Set("X-Duration-Ms", strconv.FormatInt(resp.DurationMS, 10))

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
