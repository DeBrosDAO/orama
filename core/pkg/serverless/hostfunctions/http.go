package hostfunctions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// HTTPFetch makes an outbound HTTP request directly from the gateway.
func (h *HostFunctions) HTTPFetch(ctx context.Context, method, url string, headers map[string]string, body []byte) ([]byte, error) {
	return h.doFetch(ctx, "http_fetch", h.httpClient, method, url, headers, body)
}

// SetHTTPResponse records a verbatim HTTP response for a RawHTTPResponse
// function (bugboard #835). It delegates to the per-invocation collector
// attached on ctx by the engine; the HTTP invoke handler replays the result
// byte-for-byte. Validation (raw mode enabled, status range, header/body caps)
// lives in serverless.SetRawHTTPResponse.
func (h *HostFunctions) SetHTTPResponse(ctx context.Context, status int, headers map[string]string, body []byte) error {
	if err := serverless.SetRawHTTPResponse(ctx, status, headers, body); err != nil {
		return &serverless.HostFunctionError{Function: "set_http_response", Cause: err}
	}
	return nil
}

// AnyoneFetch makes an outbound HTTP request routed through the Anyone
// (ANyONe protocol) SOCKS5 proxy, so the third-party endpoint sees an
// Anyone exit IP instead of the gateway IP and the gateway can't
// correlate (function → external request) traffic by source IP.
// Feat-11 — server-side analog of anchat's client-side proxyClient.
//
// Privacy guarantee: there is NO silent fallback to direct. If Anyone
// routing isn't available on this gateway (operator disabled it via
// --disable-anonrc / ANYONE_DISABLE=1, so h.anyoneHTTPClient is nil),
// this returns a typed error rather than leaking the request over the
// direct path. If the Anyone daemon is configured-but-down, the SOCKS
// dial to localhost:9050 fails and surfaces as a transport error — also
// never a direct send. This is the explicit ask in feat-11: a privacy
// regression must fail loudly, not degrade silently.
func (h *HostFunctions) AnyoneFetch(ctx context.Context, method, url string, headers map[string]string, body []byte) ([]byte, error) {
	if h.anyoneHTTPClient == nil {
		// Anyone routing not enabled on this gateway. Return the typed
		// error envelope (status 0) rather than dialing direct — the
		// caller explicitly asked for anonymized egress and we must not
		// silently downgrade it.
		errorResp := map[string]interface{}{
			"error":  "anyone routing not available on this gateway (disabled by operator)",
			"status": 0,
			"proxy":  "anyone",
		}
		return json.Marshal(errorResp)
	}
	return h.doFetch(ctx, "anyone_fetch", h.anyoneHTTPClient, method, url, headers, body)
}

// doFetch is the shared request/response machinery for HTTPFetch and
// AnyoneFetch — identical except for which *http.Client (direct vs
// SOCKS-routed) does the dialing and the function name used in logs +
// HostFunctionError.
func (h *HostFunctions) doFetch(ctx context.Context, fnName string, client *http.Client, method, rawURL string, headers map[string]string, body []byte) ([]byte, error) {
	if err := denyInternalURL(rawURL); err != nil {
		errorResp := map[string]interface{}{
			"error":  err.Error(),
			"status": 0,
		}
		return json.Marshal(errorResp)
	}

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		h.logger.Error(fnName+" request creation error", zap.Error(err), zap.String("url", rawURL))
		errorResp := map[string]interface{}{
			"error":  "failed to create request: " + err.Error(),
			"status": 0,
		}
		return json.Marshal(errorResp)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		h.logger.Error(fnName+" transport error", zap.Error(err), zap.String("url", rawURL))
		errorResp := map[string]interface{}{
			"error":  err.Error(),
			"status": 0, // Transport error
		}
		return json.Marshal(errorResp)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		h.logger.Error(fnName+" response read error", zap.Error(err), zap.String("url", rawURL))
		errorResp := map[string]interface{}{
			"error":  "failed to read response: " + err.Error(),
			"status": resp.StatusCode,
		}
		return json.Marshal(errorResp)
	}

	// Encode response with status code
	response := map[string]interface{}{
		"status":  resp.StatusCode,
		"headers": resp.Header,
		"body":    string(respBody),
	}

	data, err := json.Marshal(response)
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: fnName, Cause: fmt.Errorf("failed to marshal response: %w", err)}
	}

	return data, nil
}

// denyInternalURL rejects a URL that is refusable from its text alone.
//
// It is the first of two checks and not the load-bearing one. It used to be the
// only one, and it let through every URL whose host was a name rather than an
// IP literal — `http://rqlite.internal/`, or any name the tenant controlled
// pointed at 10.0.0.x. A name cannot be checked: the resolver decides what it
// becomes, the answer can change between this check and the connection, and a
// redirect goes somewhere this URL never named.
//
// The real check is guardEgressAddress, which runs on the socket itself with
// the resolved address, for every attempt and every redirect hop. What is left
// here is what can be settled without resolving anything: the scheme, the names
// that mean the machine itself, and an IP literal that is already refusable —
// answered as a clear message rather than as a connection failure.
func denyInternalURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid url")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("url scheme not allowed")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "metadata.google.internal" {
		return fmt.Errorf("url host not allowed")
	}
	if ip := net.ParseIP(host); ip != nil && blockedIP(ip) {
		return fmt.Errorf("url host not allowed")
	}
	return nil
}
