package serverless

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// HandleWebSocket handles WebSocket connections for function streaming.
// It upgrades HTTP connections to WebSocket and manages bi-directional communication
// for real-time function invocation and streaming responses.
func (h *ServerlessHandlers) HandleWebSocket(w http.ResponseWriter, r *http.Request, name string, version int) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = h.getNamespaceFromRequest(r)
	}

	if namespace == "" {
		http.Error(w, "namespace required", http.StatusBadRequest)
		return
	}

	// Upgrade to WebSocket
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
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

	h.logger.Info("WebSocket connected",
		zap.String("client_id", clientID),
		zap.String("function", name),
	)

	callerWallet := h.getWalletFromRequest(r)

	// Message loop
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logger.Warn("WebSocket error", zap.Error(err))
			}
			break
		}

		// Invoke function with WebSocket context
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		req := &serverless.InvokeRequest{
			Namespace:    namespace,
			FunctionName: name,
			Version:      version,
			Input:        message,
			TriggerType:  serverless.TriggerTypeWebSocket,
			CallerWallet: callerWallet,
			WSClientID:   clientID,
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
