package gateway

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/anyoneproxy"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// anonProxyRequest represents the JSON payload for proxy requests
type anonProxyRequest struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// anonProxyResponse represents the JSON response from proxy requests
type anonProxyResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Error      string            `json:"error,omitempty"`
}

const (
	maxProxyRequestSize = 10 * 1024 * 1024 // 10MB
	maxProxyTimeout     = 60 * time.Second
)

// anonProxyHandler handles proxied HTTP requests through the Anyone network
func (g *Gateway) anonProxyHandler(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST method is allowed")
		return
	}

	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, maxProxyRequestSize)

	// Parse request payload
	var req anonProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON payload: %v", err))
		return
	}

	// Validate URL
	targetURL, err := url.Parse(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid URL: %v", err))
		return
	}

	// Only allow HTTPS for external requests
	if targetURL.Scheme != "https" && targetURL.Scheme != "http" {
		writeError(w, http.StatusBadRequest, "only http/https schemes are allowed")
		return
	}

	// Block requests to private/local addresses
	if isPrivateOrLocalHost(targetURL.Host) {
		writeError(w, http.StatusForbidden, "requests to private/local addresses are not allowed")
		return
	}

	// Validate HTTP method
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = "GET"
	}
	allowedMethods := map[string]bool{
		"GET":    true,
		"POST":   true,
		"PUT":    true,
		"DELETE": true,
		"PATCH":  true,
		"HEAD":   true,
	}
	if !allowedMethods[method] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("method %s not allowed", method))
		return
	}

	// Check if Anyone proxy is running (after all validation)
	if !anyoneproxy.Running() {
		g.logger.ComponentWarn(logging.ComponentGeneral, "Anyone proxy not available",
			zap.String("socks_addr", anyoneproxy.Address()))
		writeJSON(w, http.StatusServiceUnavailable, anonProxyResponse{
			Error: fmt.Sprintf("Anyone proxy not available at %s", anyoneproxy.Address()),
		})
		return
	}

	// Create HTTP client with Anyone proxy
	client := anyoneproxy.NewHTTPClient()
	client.Timeout = maxProxyTimeout

	// Create the proxied request
	var bodyReader io.Reader
	if req.Body != "" {
		bodyReader = strings.NewReader(req.Body)
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), method, req.URL, bodyReader)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create request: %v", err))
		return
	}

	// Copy headers, excluding hop-by-hop headers
	for key, value := range req.Headers {
		if !isHopByHopHeader(key) {
			proxyReq.Header.Set(key, value)
		}
	}

	// Set default User-Agent if not provided
	if proxyReq.Header.Get("User-Agent") == "" {
		proxyReq.Header.Set("User-Agent", "DeBros-Gateway/1.0")
	}

	// Log the proxy request
	g.logger.ComponentInfo(logging.ComponentGeneral, "proxying request through Anyone",
		zap.String("method", method),
		zap.String("url", req.URL),
		zap.String("socks_addr", anyoneproxy.Address()))

	// Execute the request
	start := time.Now()
	resp, err := client.Do(proxyReq)
	duration := time.Since(start)

	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "proxy request failed",
			zap.Error(err),
			zap.String("url", req.URL),
			zap.Duration("duration", duration))
		writeJSON(w, http.StatusBadGateway, anonProxyResponse{
			Error: fmt.Sprintf("proxy request failed: %v", err),
		})
		return
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxProxyRequestSize))
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "failed to read proxy response",
			zap.Error(err))
		writeJSON(w, http.StatusBadGateway, anonProxyResponse{
			Error: fmt.Sprintf("failed to read response: %v", err),
		})
		return
	}

	// Extract response headers (excluding hop-by-hop)
	respHeaders := make(map[string]string)
	for key, values := range resp.Header {
		if !isHopByHopHeader(key) && len(values) > 0 {
			respHeaders[key] = values[0]
		}
	}

	g.logger.ComponentInfo(logging.ComponentGeneral, "proxy request completed",
		zap.String("url", req.URL),
		zap.Int("status", resp.StatusCode),
		zap.Int("bytes", len(respBody)),
		zap.Duration("duration", duration))

	// Base64-encode the body to safely handle binary data in JSON
	// This prevents corruption when binary data is converted to a string
	bodyBase64 := base64.StdEncoding.EncodeToString(respBody)

	// Return the proxied response
	writeJSON(w, http.StatusOK, anonProxyResponse{
		StatusCode: resp.StatusCode,
		Headers:    respHeaders,
		Body:       bodyBase64,
	})
}

// isHopByHopHeader returns true for HTTP hop-by-hop headers that should not be forwarded
func isHopByHopHeader(header string) bool {
	hopByHop := map[string]bool{
		"Connection":          true,
		"Keep-Alive":          true,
		"Proxy-Authenticate":  true,
		"Proxy-Authorization": true,
		"Te":                  true,
		"Trailers":            true,
		"Transfer-Encoding":   true,
		"Upgrade":             true,
	}
	return hopByHop[http.CanonicalHeaderKey(header)]
}

// isPrivateOrLocalHost checks if a host is private, local, or loopback
func isPrivateOrLocalHost(host string) bool {
	// Strip port if present, handling IPv6 addresses properly
	// IPv6 addresses in URLs are bracketed: [::1]:8080
	if strings.HasPrefix(host, "[") {
		// IPv6 address with brackets
		if idx := strings.LastIndex(host, "]"); idx != -1 {
			if idx+1 < len(host) && host[idx+1] == ':' {
				// Port present, strip it
				host = host[1:idx] // Remove brackets and port
			} else {
				// No port, just remove brackets
				host = host[1:idx]
			}
		}
	} else {
		// IPv4 or hostname, check for port
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			// Check if it's an IPv6 address without brackets (contains multiple colons)
			colonCount := strings.Count(host, ":")
			if colonCount == 1 {
				// Single colon, likely IPv4 with port
				host = host[:idx]
			}
			// If multiple colons, it's IPv6 without brackets and no port
			// Leave host as-is
		}
	}

	// Check for localhost variants
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}

	// Check common private ranges (basic check)
	if strings.HasPrefix(host, "10.") ||
		strings.HasPrefix(host, "192.168.") ||
		strings.HasPrefix(host, "172.16.") ||
		strings.HasPrefix(host, "172.17.") ||
		strings.HasPrefix(host, "172.18.") ||
		strings.HasPrefix(host, "172.19.") ||
		strings.HasPrefix(host, "172.20.") ||
		strings.HasPrefix(host, "172.21.") ||
		strings.HasPrefix(host, "172.22.") ||
		strings.HasPrefix(host, "172.23.") ||
		strings.HasPrefix(host, "172.24.") ||
		strings.HasPrefix(host, "172.25.") ||
		strings.HasPrefix(host, "172.26.") ||
		strings.HasPrefix(host, "172.27.") ||
		strings.HasPrefix(host, "172.28.") ||
		strings.HasPrefix(host, "172.29.") ||
		strings.HasPrefix(host, "172.30.") ||
		strings.HasPrefix(host, "172.31.") {
		return true
	}

	return false
}
