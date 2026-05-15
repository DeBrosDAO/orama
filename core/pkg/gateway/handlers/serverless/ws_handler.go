package serverless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// checkWSOrigin validates WebSocket origins against the request's Host header.
// Non-browser clients (no Origin) are allowed. Browser clients must match the host.
//
// Bug #240/#249 root cause: when this handler runs on a NAMESPACE gateway,
// the request has been proxied through `handleNamespaceGatewayRequest`
// which REWRITES `r.Host` to the backend target's IP:port (e.g.
// "10.0.0.6:10004") before forwarding. The original public host (e.g.
// "ns-anchat-test.orama-devnet.network") is preserved in the
// `X-Forwarded-Host` header. If we only compare the Origin against
// `r.Host`, browser/RN-iOS clients (which always send Origin) are
// rejected with 403 because their Origin's `ns-anchat-test.orama-devnet.network`
// will never match the proxied `10.0.0.6` target. Curl tests that don't
// send Origin slip through, masking the bug.
//
// Prefer X-Forwarded-Host (the original public host) when present,
// falling back to r.Host for direct (non-proxied) connections.
func checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return false
	}
	// Strip port from host if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := parsed.Hostname()
	return originHost == host || strings.HasSuffix(originHost, "."+host)
}

// HandleWebSocket handles WebSocket connections for function streaming.
// It upgrades HTTP connections to WebSocket and manages bi-directional communication
// for real-time function invocation and streaming responses.
//
// Routes to one of two execution models based on function metadata:
//   - WSPersistent=true: persistent per-connection WASM instance (plan 06)
//   - WSPersistent=false (default): per-frame stateless invocation
func (h *ServerlessHandlers) HandleWebSocket(w http.ResponseWriter, r *http.Request, name string, version int) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = h.getNamespaceFromRequest(r)
	}

	if namespace == "" {
		http.Error(w, "namespace required", http.StatusBadRequest)
		return
	}

	// Look up the function once to decide which execution model to use.
	fn, lookupErr := h.registry.Get(r.Context(), namespace, name, version)
	if lookupErr == nil && fn != nil && fn.WSPersistent {
		h.handlePersistentWebSocket(w, r, fn, namespace)
		return
	}
	// (lookup error not fatal — fall through; per-frame path's invoker will
	// re-resolve and surface a proper error.)

	// Upgrade to WebSocket
	upgrader := websocket.Upgrader{
		CheckOrigin: checkWSOrigin,
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("WebSocket upgrade failed", zap.Error(err))
		return
	}

	clientID := uuid.New().String()
	wsConn := &serverless.GorillaWSConn{Conn: conn}

	// Register connection
	h.wsManager.Register(clientID, wsConn)
	defer h.wsManager.Unregister(clientID)

	// Track client → namespace for ws_pubsub_bridge auth checks, and
	// auto-clean any bridged topics when the connection ends.
	if h.wsBridge != nil {
		h.wsBridge.SetClientNamespace(clientID, namespace)
		defer h.wsBridge.RemoveClient(context.Background(), clientID)
	}

	// Server-side keepalive: ping every 30s, expect pong within 60s.
	// Without this, a half-open TCP can hang for 2h before the OS notices.
	const (
		pingInterval = 30 * time.Second
		pongWait     = 60 * time.Second
	)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	pingDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pingDone:
				return
			case <-ticker.C:
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			}
		}
	}()
	defer close(pingDone)

	h.logger.Info("WebSocket connected",
		zap.String("client_id", clientID),
		zap.String("function", name),
	)

	callerWallet := h.getWalletFromRequest(r)
	callerIP := extractRemoteIP(r)
	// Capture custom claims at upgrade time and reuse for every frame —
	// the JWT context is request-scoped and won't survive past upgrade.
	callerClaims := h.getCallerClaimsFromRequest(r)
	callerJWTSubject := h.getJWTSubjectFromRequest(r)

	// Message loop
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logger.Warn("WebSocket error", zap.Error(err))
			}
			break
		}
		h.wsManager.RecordInbound(clientID, len(message))

		// Invoke function with WebSocket context
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		req := &serverless.InvokeRequest{
			Namespace:        namespace,
			FunctionName:     name,
			Version:          version,
			Input:            message,
			TriggerType:      serverless.TriggerTypeWebSocket,
			CallerWallet:     callerWallet,
			CallerIP:         callerIP,
			CallerClaims:     callerClaims,
			CallerJWTSubject: callerJWTSubject,
			WSClientID:       clientID,
		}

		resp, err := h.invoker.Invoke(ctx, req)
		cancel()

		// Send response back
		response := map[string]interface{}{
			"request_id":  resp.RequestID,
			"status":      resp.Status,
			"duration_ms": resp.DurationMS,
		}

		if err != nil {
			response["error"] = resp.Error
		} else if len(resp.Output) > 0 {
			// Try to parse output as JSON
			var output interface{}
			if json.Unmarshal(resp.Output, &output) == nil {
				response["output"] = output
			} else {
				response["output"] = string(resp.Output)
			}
		}

		respBytes, _ := json.Marshal(response)
		if err := conn.WriteMessage(websocket.TextMessage, respBytes); err != nil {
			break
		}
	}
}
