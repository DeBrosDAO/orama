package serverless

import (
	"context"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/persistent"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// handlePersistentWebSocket runs the per-connection persistent function model.
// One WASM instance is bound to this WS for its entire lifetime. Frames are
// processed serially via the instance's inbound channel.
//
// See plan: core/plans/platform/06_PERSISTENT_WS_FUNCTIONS.md
func (h *ServerlessHandlers) handlePersistentWebSocket(
	w http.ResponseWriter, r *http.Request, fn *serverless.Function, namespace string,
) {
	// Hard prerequisites — without engine + manager, persistent WS can't run.
	if h.engine == nil || h.persistentMgr == nil {
		http.Error(w, "persistent WebSocket support not configured", http.StatusServiceUnavailable)
		return
	}

	// Capacity check BEFORE upgrade so we don't leak a half-open WS.
	if !h.persistentMgr.Acquire() {
		http.Error(w, "gateway at persistent-ws capacity", http.StatusServiceUnavailable)
		return
	}
	releaseSlot := true
	defer func() {
		if releaseSlot {
			h.persistentMgr.Release()
		}
	}()

	upgrader := websocket.Upgrader{CheckOrigin: checkWSOrigin}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("persistent WS upgrade failed", zap.Error(err))
		return
	}

	clientID := uuid.New().String()
	wsConn := &serverless.GorillaWSConn{Conn: conn}
	h.wsManager.Register(clientID, wsConn)
	defer h.wsManager.Unregister(clientID)

	// Bridge bookkeeping (mirrors stateless path): the persistent WASM
	// instance can call ws_pubsub_bridge from ws_open or any frame handler;
	// the bridge needs to know which namespace owns this client.
	if h.wsBridge != nil {
		h.wsBridge.SetClientNamespace(clientID, namespace)
		defer h.wsBridge.RemoveClient(context.Background(), clientID)
	}

	callerWallet := h.getWalletFromRequest(r)
	callerIP := extractRemoteIP(r)
	callerClaims := h.getCallerClaimsFromRequest(r)

	invCtx := &serverless.InvocationContext{
		FunctionID:   fn.ID,
		FunctionName: fn.Name,
		Namespace:    fn.Namespace,
		CallerWallet: callerWallet,
		CallerIP:     callerIP,
		CallerClaims: callerClaims,
		WSClientID:   clientID,
		TriggerType:  serverless.TriggerTypeWebSocket,
	}

	// Instantiate the persistent module. This compiles once (cached) and
	// creates one wazero instance bound to this connection.
	module, err := h.engine.InstantiatePersistent(r.Context(), fn, invCtx)
	if err != nil {
		h.logger.Warn("persistent WS instantiate failed",
			zap.String("function", fn.Name),
			zap.String("namespace", fn.Namespace),
			zap.Error(err))
		_ = conn.Close()
		return
	}

	inst, err := persistent.NewInstance(module, persistent.Config{
		ClientID:          clientID,
		FunctionName:      fn.Name,
		Namespace:         fn.Namespace,
		FrameTimeoutSec:   fn.TimeoutSeconds,
		MaxInflightFrames: fn.WSMaxInflightPerConn,
	}, h.logger)
	if err != nil {
		h.logger.Warn("persistent WS NewInstance failed",
			zap.String("function", fn.Name),
			zap.Error(err))
		_ = module.Close(context.Background())
		_ = conn.Close()
		return
	}

	h.persistentMgr.Register(inst)
	// Hand the slot off to instance lifecycle. Released when we Close below.
	releaseSlot = false
	defer h.persistentMgr.Release()
	defer h.persistentMgr.Unregister(clientID)

	// ws_open — invoked synchronously. A non-zero return rejects the upgrade.
	openInput := persistent.WSOpenInput{
		ClientID:  clientID,
		Wallet:    callerWallet,
		Namespace: namespace,
	}
	if err := inst.Open(r.Context(), openInput); err != nil {
		h.logger.Info("persistent WS rejected by ws_open",
			zap.String("function", fn.Name),
			zap.String("client_id", clientID),
			zap.Error(err))
		inst.Close(context.Background(), persistent.CloseReasonRejected)
		_ = conn.Close()
		return
	}

	// Spawn the per-instance frame processor.
	runCtx, runCancel := context.WithCancel(context.Background())
	go inst.Run(runCtx)

	// Server-side keepalive (matches stateless WS handler's behavior).
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

	// Read loop — enqueue frames into the instance.
	for {
		_, frame, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		h.wsManager.RecordInbound(clientID, len(frame))
		if err := inst.Submit(frame); err != nil {
			h.logger.Warn("persistent WS submit failed (queue full?)",
				zap.String("client_id", clientID),
				zap.Error(err))
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(1009, "queue full"),
				time.Now().Add(time.Second))
			break
		}
	}

	// Tear down: stop ping, stop instance Run, invoke ws_close, close WS.
	close(pingDone)
	runCancel()
	inst.Close(context.Background(), persistent.CloseReasonClientDisconnect)
	_ = conn.Close()
}
