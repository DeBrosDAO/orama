package serverless

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/persistent"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// oramaControlFramePrefix is a cheap byte-level sniff for the WS
// control-frame envelope shape `{"__orama":"..."}`. We peek for this
// before JSON-decoding to keep the per-frame fast path free of
// json.Unmarshal cost — the vast majority of inbound frames are
// application traffic that goes straight to WASM. Bugboard #321.
var oramaControlFramePrefix = []byte(`"__orama"`)

// oramaControlFrame is the wire shape for gateway-handled control
// frames on a persistent WS. The single Type field discriminates;
// payload fields specific to each Type ride alongside.
//
// Today supports:
//
//	{"__orama":"auth.refresh","jwt":"<new-token>"}
//
// Future types (e.g. "ping.app", "subscribe.status") follow the same
// shape. Reserve "__orama" as the namespace so application frames
// never collide.
type oramaControlFrame struct {
	Type string `json:"__orama"`
	JWT  string `json:"jwt,omitempty"`
}

// oramaControlAck is the response shape sent back on the WS after a
// control frame is handled. Clients SHOULD await this before assuming
// the gateway has applied the change.
type oramaControlAck struct {
	Type    string `json:"__orama_ack"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Subject string `json:"subject,omitempty"` // populated on successful auth.refresh
}

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

	invCtx := h.buildPersistentInvocationContext(r, fn, clientID)
	callerWallet := invCtx.CallerWallet

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
		// Per-instance identity binding. The persistent.Instance attaches
		// this to the ctx of every WASM-host call (ws_open / ws_frame /
		// ws_close + nested function_invoke), so caller identity is
		// race-free across concurrent persistent WS connections — fixes
		// the cross-tenant identity-leak on the shared HostFunctions
		// singleton (security audit follow-up to Layer 7 of Feature #73).
		InvocationContext: invCtx,
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

	// Read loop — enqueue frames into the instance. Bugboard #321:
	// gateway-handled control frames (e.g. {"__orama":"auth.refresh"})
	// are intercepted here BEFORE submission so they don't reach WASM.
	for {
		_, frame, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		h.wsManager.RecordInbound(clientID, len(frame))

		// Cheap byte-level prefix sniff so the per-frame fast path
		// avoids json.Unmarshal for every application frame. Only
		// frames carrying the `"__orama"` key get parsed.
		if bytes.Contains(frame, oramaControlFramePrefix) {
			handled, ackErr := h.handleOramaControlFrame(frame, fn, inst, namespace, clientID, conn)
			if ackErr != nil {
				h.logger.Warn("persistent WS: control-frame ack write failed",
					zap.String("client_id", clientID),
					zap.Error(ackErr))
				// Don't kill the WS for an ack write failure — the
				// client will time-out the ack and retry. Continue.
			}
			if handled {
				continue // Don't forward control frames to WASM.
			}
			// Not actually a control frame (false-positive prefix
			// match — e.g. a JSON string literal containing
			// `"__orama"`); fall through and submit as a normal
			// application frame.
		}

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

// buildPersistentInvocationContext constructs the per-connection InvocationContext
// for a persistent WS instance. Extracted from handlePersistentWebSocket so the
// auth-field plumbing can be unit-tested without doing a real WS upgrade.
//
// IMPORTANT: this context is sticky for the lifetime of the connection — it is
// bound once at instantiation (pkg/serverless/engine.go InstantiatePersistent)
// and reused for every ws_open / ws_frame / ws_close call, as well as for any
// nested function_invoke call originating inside the WASM instance. Missing a
// field here (notably CallerJWTSubject) means every sub-function invoked via
// `oh.FunctionInvoke` sees an empty value for the missing field — Layer 7 of
// the WS bug chain (Feature #73 on bugboard; AnChat sync-deltas was returning
// AUTH_REQUIRED because oh.JwtSubjectUserID() was "" inside the sub-function).
//
// Keep this in sync with the stateless WS handler's InvokeRequest construction
// in ws_handler.go — they must populate the same auth-identity fields.
func (h *ServerlessHandlers) buildPersistentInvocationContext(
	r *http.Request, fn *serverless.Function, clientID string,
) *serverless.InvocationContext {
	return &serverless.InvocationContext{
		FunctionID:       fn.ID,
		FunctionName:     fn.Name,
		Namespace:        fn.Namespace,
		CallerWallet:     h.getWalletFromRequest(r),
		CallerIP:         extractRemoteIP(r),
		CallerClaims:     h.getCallerClaimsFromRequest(r),
		CallerJWTSubject: h.getJWTSubjectFromRequest(r),
		WSClientID:       clientID,
		TriggerType:      serverless.TriggerTypeWebSocket,
	}
}

// handleOramaControlFrame parses a frame as the orama control envelope
// and dispatches by type. Returns (handled=true, _) if the frame was a
// well-formed control frame (regardless of whether it succeeded);
// (false, nil) for false-positives where the byte sniff matched but
// the JSON shape isn't ours. The returned error reflects only the ack
// write — not the underlying control action (which surfaces via the
// ack body's ok/error fields).
//
// Bugboard #321: introduced for the auth.refresh path so persistent
// WS connections survive JWT rotation without a close+reconnect.
func (h *ServerlessHandlers) handleOramaControlFrame(
	frame []byte,
	fn *serverless.Function,
	inst *persistent.Instance,
	namespace, clientID string,
	conn *websocket.Conn,
) (handled bool, ackErr error) {
	var ctrl oramaControlFrame
	if err := json.Unmarshal(frame, &ctrl); err != nil {
		// Not JSON, or doesn't match our shape. Treat as application
		// frame (false-positive on the prefix sniff).
		return false, nil
	}
	if ctrl.Type == "" {
		return false, nil
	}

	switch ctrl.Type {
	case "auth.refresh":
		return true, h.handleAuthRefresh(ctrl, fn, inst, namespace, clientID, conn)
	default:
		// Unknown control type — ack with an error so the client knows
		// the frame was seen but ignored. Treat as handled (don't
		// forward to WASM), since the `__orama` namespace is reserved.
		return true, h.writeControlAck(conn, oramaControlAck{
			Type:  ctrl.Type,
			OK:    false,
			Error: "unknown __orama control type",
		})
	}
}

// handleAuthRefresh validates the new JWT, swaps the persistent
// instance's invocation context atomically, and acks the client.
// On invalid JWT: ack with ok=false and a reason. Does NOT close the
// WS — the client can retry with a fresh token. Bugboard #321.
func (h *ServerlessHandlers) handleAuthRefresh(
	ctrl oramaControlFrame,
	fn *serverless.Function,
	inst *persistent.Instance,
	namespace, clientID string,
	conn *websocket.Conn,
) error {
	if h.jwtVerifier == nil {
		return h.writeControlAck(conn, oramaControlAck{
			Type:  "auth.refresh",
			OK:    false,
			Error: "mid-session auth refresh not supported on this gateway",
		})
	}
	if ctrl.JWT == "" {
		return h.writeControlAck(conn, oramaControlAck{
			Type:  "auth.refresh",
			OK:    false,
			Error: "jwt field required",
		})
	}
	claims, err := h.jwtVerifier.ParseAndVerifyJWT(ctrl.JWT)
	if err != nil {
		h.logger.Info("persistent WS: auth.refresh rejected (invalid jwt)",
			zap.String("client_id", clientID),
			zap.Error(err))
		return h.writeControlAck(conn, oramaControlAck{
			Type:  "auth.refresh",
			OK:    false,
			Error: "invalid or expired jwt: " + err.Error(),
		})
	}

	if reason := validateRefreshClaims(claims, fn.Namespace); reason != "" {
		h.logger.Warn("persistent WS: auth.refresh rejected",
			zap.String("client_id", clientID),
			zap.String("reason", reason),
			zap.String("ws_namespace", fn.Namespace),
			zap.String("jwt_namespace", claims.Namespace),
			zap.String("jwt_subject", claims.Sub),
		)
		return h.writeControlAck(conn, oramaControlAck{
			Type:  "auth.refresh",
			OK:    false,
			Error: reason,
		})
	}

	// Audit log when the refreshed subject DIFFERS from the original
	// (bug #321 audit LOW #8). Same-subject rotations are the common
	// case (token renewal); cross-subject is legal but rare enough
	// that operators benefit from seeing it in the audit trail.
	prevSubject := ""
	if cur := inst.CurrentInvocationContext(); cur != nil {
		prevSubject = cur.CallerJWTSubject
	}
	if prevSubject != "" && prevSubject != claims.Sub {
		h.logger.Info("persistent WS: auth.refresh swapping subject identity on socket",
			zap.String("client_id", clientID),
			zap.String("previous_subject", prevSubject),
			zap.String("new_subject", claims.Sub),
		)
	}

	// Build a fresh InvocationContext with the new identity. Preserve
	// the connection-scoped fields (FunctionID/Name, Namespace,
	// WSClientID, CallerIP, TriggerType) — those don't change. Wallet
	// resolution follows the same precedence as the original upgrade:
	// JWT subject is the source of truth here since the caller is
	// proving fresh identity.
	customClaims := map[string]string{}
	for k, v := range claims.Custom {
		customClaims[k] = v
	}
	newInvCtx := &serverless.InvocationContext{
		FunctionID:       fn.ID,
		FunctionName:     fn.Name,
		Namespace:        fn.Namespace,
		CallerWallet:     claims.Sub,
		CallerClaims:     customClaims,
		CallerJWTSubject: claims.Sub,
		WSClientID:       clientID,
		TriggerType:      serverless.TriggerTypeWebSocket,
	}

	if err := inst.UpdateInvocationContext(newInvCtx); err != nil {
		// nil-guard inside UpdateInvocationContext is the only error
		// path today; we just built newInvCtx with non-nil fields so
		// this shouldn't fire. If it does, surface as an internal error.
		h.logger.Error("persistent WS: UpdateInvocationContext failed",
			zap.String("client_id", clientID),
			zap.Error(err))
		return h.writeControlAck(conn, oramaControlAck{
			Type:  "auth.refresh",
			OK:    false,
			Error: "internal: failed to apply refresh",
		})
	}

	h.logger.Info("persistent WS: auth.refresh applied",
		zap.String("client_id", clientID),
		zap.String("namespace", namespace),
		zap.String("new_subject", claims.Sub))

	return h.writeControlAck(conn, oramaControlAck{
		Type:    "auth.refresh",
		OK:      true,
		Subject: claims.Sub,
	})
}

// validateRefreshClaims is the policy decision for whether a
// post-validation JWT may be installed on a persistent WS via the
// auth.refresh control frame. Returns "" if allowed, or a
// human-readable reason string suitable for the ack body.
//
// SECURITY (bug #321 audit HIGH #9): reject JWTs minted for a
// DIFFERENT namespace. Without this check, an attacker who
// legitimately owns an account in namespace B could rotate their
// already-established namespace-A WS to run as their B-subject
// against A's WASM/secrets/data. The upgrade-time auth middleware
// already enforces namespace match; this preserves the invariant
// across mid-session rotations.
//
// Empty claims.Namespace is treated as a hard reject — JWTs minted
// by this gateway always populate it; an empty value either means
// a foreign issuer slipped through or a malformed token. Either
// way, refuse rather than silently default to the WS's namespace.
//
// Extracted as a pure function so the policy decision can be
// regression-tested without a live WS connection.
func validateRefreshClaims(claims *auth.JWTClaims, wsNamespace string) string {
	if claims == nil {
		return "internal: nil claims after verification"
	}
	if claims.Namespace == "" {
		return "jwt missing namespace claim"
	}
	if claims.Namespace != wsNamespace {
		return "jwt namespace does not match websocket namespace"
	}
	if claims.Sub == "" {
		// Subject-less JWTs would swap the WS into an anonymous
		// identity, breaking every downstream auth check. Reject.
		return "jwt missing subject claim"
	}
	return ""
}

// writeControlAck JSON-encodes the ack and writes it as a single text
// message back to the client. Bounded write deadline so a slow client
// doesn't block the read loop.
func (h *ServerlessHandlers) writeControlAck(conn *websocket.Conn, ack oramaControlAck) error {
	payload, err := json.Marshal(ack)
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetWriteDeadline(time.Time{})
	return conn.WriteMessage(websocket.TextMessage, payload)
}
