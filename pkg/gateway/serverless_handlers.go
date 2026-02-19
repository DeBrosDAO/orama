package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// ServerlessHandlers contains handlers for serverless function endpoints.
// It's a separate struct to keep the Gateway struct clean.
type ServerlessHandlers struct {
	invoker        *serverless.Invoker
	registry       serverless.FunctionRegistry
	wsManager      *serverless.WSManager
	triggerManager serverless.TriggerManager
	logger         *zap.Logger
}

// NewServerlessHandlers creates a new ServerlessHandlers instance.
func NewServerlessHandlers(
	invoker *serverless.Invoker,
	registry serverless.FunctionRegistry,
	wsManager *serverless.WSManager,
	triggerManager serverless.TriggerManager,
	logger *zap.Logger,
) *ServerlessHandlers {
	return &ServerlessHandlers{
		invoker:        invoker,
		registry:       registry,
		wsManager:      wsManager,
		triggerManager: triggerManager,
		logger:         logger,
	}
}

// RegisterRoutes registers all serverless routes on the given mux.
func (h *ServerlessHandlers) RegisterRoutes(mux *http.ServeMux) {
	// Function management
	mux.HandleFunc("/v1/functions", h.handleFunctions)
	mux.HandleFunc("/v1/functions/", h.handleFunctionByName)

	// Direct invoke endpoint
	mux.HandleFunc("/v1/invoke/", h.handleInvoke)
}

// handleFunctions handles GET /v1/functions (list) and POST /v1/functions (deploy)
func (h *ServerlessHandlers) handleFunctions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listFunctions(w, r)
	case http.MethodPost:
		h.deployFunction(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleFunctionByName handles operations on a specific function
// Routes:
//   - GET    /v1/functions/{name}           - Get function info
//   - DELETE /v1/functions/{name}           - Delete function
//   - POST   /v1/functions/{name}/invoke    - Invoke function
//   - GET    /v1/functions/{name}/versions  - List versions
//   - GET    /v1/functions/{name}/logs      - Get logs
//   - WS     /v1/functions/{name}/ws        - WebSocket invoke
func (h *ServerlessHandlers) handleFunctionByName(w http.ResponseWriter, r *http.Request) {
	// Parse path: /v1/functions/{name}[/{action}]
	path := strings.TrimPrefix(r.URL.Path, "/v1/functions/")
	parts := strings.SplitN(path, "/", 2)

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Function name required", http.StatusBadRequest)
		return
	}

	name := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	// Parse version from name if present (e.g., "myfunction@2")
	version := 0
	if idx := strings.Index(name, "@"); idx > 0 {
		vStr := name[idx+1:]
		name = name[:idx]
		if v, err := strconv.Atoi(vStr); err == nil {
			version = v
		}
	}

	switch action {
	case "invoke":
		h.invokeFunction(w, r, name, version)
	case "ws":
		h.handleWebSocket(w, r, name, version)
	case "versions":
		h.listVersions(w, r, name)
	case "logs":
		h.getFunctionLogs(w, r, name)
	case "triggers":
		h.handleFunctionTriggers(w, r, name)
	case "":
		switch r.Method {
		case http.MethodGet:
			h.getFunctionInfo(w, r, name, version)
		case http.MethodDelete:
			h.deleteFunction(w, r, name, version)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	default:
		http.Error(w, "Unknown action", http.StatusNotFound)
	}
}

// handleInvoke handles POST /v1/invoke/{namespace}/{name}[@version]
func (h *ServerlessHandlers) handleInvoke(w http.ResponseWriter, r *http.Request) {
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

	h.invokeFunction(w, r, namespace+"/"+name, version)
}

// listFunctions handles GET /v1/functions
func (h *ServerlessHandlers) listFunctions(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		// Get namespace from JWT if available
		namespace = h.getNamespaceFromRequest(r)
	}

	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	functions, err := h.registry.List(ctx, namespace)
	if err != nil {
		h.logger.Error("Failed to list functions", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "Failed to list functions")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"functions": functions,
		"count":     len(functions),
	})
}

// deployFunction handles POST /v1/functions
func (h *ServerlessHandlers) deployFunction(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form (for WASM upload) or JSON
	contentType := r.Header.Get("Content-Type")

	var def serverless.FunctionDefinition
	var wasmBytes []byte

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Parse multipart form
		if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB max
			writeError(w, http.StatusBadRequest, "Failed to parse form: "+err.Error())
			return
		}

		// Get metadata from form field
		metadataStr := r.FormValue("metadata")
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &def); err != nil {
				writeError(w, http.StatusBadRequest, "Invalid metadata JSON: "+err.Error())
				return
			}
		}

		// Get name from form if not in metadata
		if def.Name == "" {
			def.Name = r.FormValue("name")
		}

		// Get namespace from form if not in metadata
		if def.Namespace == "" {
			def.Namespace = r.FormValue("namespace")
		}

		// Get other configuration fields from form
		if v := r.FormValue("is_public"); v != "" {
			def.IsPublic, _ = strconv.ParseBool(v)
		}
		if v := r.FormValue("memory_limit_mb"); v != "" {
			def.MemoryLimitMB, _ = strconv.Atoi(v)
		}
		if v := r.FormValue("timeout_seconds"); v != "" {
			def.TimeoutSeconds, _ = strconv.Atoi(v)
		}
		if v := r.FormValue("retry_count"); v != "" {
			def.RetryCount, _ = strconv.Atoi(v)
		}
		if v := r.FormValue("retry_delay_seconds"); v != "" {
			def.RetryDelaySeconds, _ = strconv.Atoi(v)
		}

		// Get WASM file
		file, _, err := r.FormFile("wasm")
		if err != nil {
			writeError(w, http.StatusBadRequest, "WASM file required")
			return
		}
		defer file.Close()

		wasmBytes, err = io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Failed to read WASM file: "+err.Error())
			return
		}
	} else {
		// JSON body with base64-encoded WASM
		var req struct {
			serverless.FunctionDefinition
			WASMBase64 string `json:"wasm_base64"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}

		def = req.FunctionDefinition

		if req.WASMBase64 != "" {
			// Decode base64 WASM - for now, just reject this method
			writeError(w, http.StatusBadRequest, "Base64 WASM upload not supported, use multipart/form-data")
			return
		}
	}

	// Get namespace from JWT if not provided
	if def.Namespace == "" {
		def.Namespace = h.getNamespaceFromRequest(r)
	}

	if def.Name == "" {
		writeError(w, http.StatusBadRequest, "Function name required")
		return
	}
	if def.Namespace == "" {
		writeError(w, http.StatusBadRequest, "Namespace required")
		return
	}
	if len(wasmBytes) == 0 {
		writeError(w, http.StatusBadRequest, "WASM bytecode required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	oldFn, err := h.registry.Register(ctx, &def, wasmBytes)
	if err != nil {
		h.logger.Error("Failed to deploy function",
			zap.String("name", def.Name),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to deploy: "+err.Error())
		return
	}

	// Invalidate cache for the old version to ensure the new one is loaded
	if oldFn != nil {
		h.invoker.InvalidateCache(oldFn.WASMCID)
		h.logger.Debug("Invalidated function cache",
			zap.String("name", def.Name),
			zap.String("old_wasm_cid", oldFn.WASMCID),
		)
	}

	h.logger.Info("Function deployed",
		zap.String("name", def.Name),
		zap.String("namespace", def.Namespace),
	)

	// Fetch the deployed function to return
	fn, err := h.registry.Get(ctx, def.Namespace, def.Name, def.Version)
	if err != nil {
		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"message": "Function deployed successfully",
			"name":    def.Name,
		})
		return
	}

	// Register PubSub triggers if provided in metadata
	var triggersAdded []string
	if len(def.PubSubTopics) > 0 && h.triggerManager != nil {
		for _, topic := range def.PubSubTopics {
			if err := h.triggerManager.AddPubSubTrigger(ctx, fn.ID, topic); err != nil {
				// Log but don't fail deployment
				h.logger.Warn("Failed to add pubsub trigger during deployment",
					zap.String("function", def.Name),
					zap.String("topic", topic),
					zap.Error(err),
				)
			} else {
				triggersAdded = append(triggersAdded, topic)
				h.logger.Info("PubSub trigger added during deployment",
					zap.String("function", def.Name),
					zap.String("topic", topic),
				)
			}
		}
	}

	response := map[string]interface{}{
		"message":  "Function deployed successfully",
		"function": fn,
	}
	if len(triggersAdded) > 0 {
		response["triggers_added"] = triggersAdded
	}

	writeJSON(w, http.StatusCreated, response)
}

// getFunctionInfo handles GET /v1/functions/{name}
func (h *ServerlessHandlers) getFunctionInfo(w http.ResponseWriter, r *http.Request, name string, version int) {
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

// deleteFunction handles DELETE /v1/functions/{name}
func (h *ServerlessHandlers) deleteFunction(w http.ResponseWriter, r *http.Request, name string, version int) {
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

	if err := h.registry.Delete(ctx, namespace, name, version); err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to delete function")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Function deleted successfully",
	})
}

// invokeFunction handles POST /v1/functions/{name}/invoke
func (h *ServerlessHandlers) invokeFunction(w http.ResponseWriter, r *http.Request, nameWithNS string, version int) {
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
	}

	resp, err := h.invoker.Invoke(ctx, req)
	if err != nil {
		statusCode := http.StatusInternalServerError
		if serverless.IsNotFound(err) {
			statusCode = http.StatusNotFound
		} else if serverless.IsResourceExhausted(err) {
			statusCode = http.StatusTooManyRequests
		} else if serverless.IsUnauthorized(err) {
			statusCode = http.StatusUnauthorized
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

// handleWebSocket handles WebSocket connections for function streaming
func (h *ServerlessHandlers) handleWebSocket(w http.ResponseWriter, r *http.Request, name string, version int) {
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

// listVersions handles GET /v1/functions/{name}/versions
func (h *ServerlessHandlers) listVersions(w http.ResponseWriter, r *http.Request, name string) {
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

// getFunctionLogs handles GET /v1/functions/{name}/logs
func (h *ServerlessHandlers) getFunctionLogs(w http.ResponseWriter, r *http.Request, name string) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = h.getNamespaceFromRequest(r)
	}

	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	limit := 100
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil {
			limit = l
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	logs, err := h.registry.GetLogs(ctx, namespace, name, limit)
	if err != nil {
		h.logger.Error("Failed to get function logs",
			zap.String("name", name),
			zap.String("namespace", namespace),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to get logs")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":      name,
		"namespace": namespace,
		"logs":      logs,
		"count":     len(logs),
	})
}

// getNamespaceFromRequest extracts namespace from JWT or query param
func (h *ServerlessHandlers) getNamespaceFromRequest(r *http.Request) string {
	// Try context first (set by auth middleware) - most secure
	if v := r.Context().Value(ctxKeyNamespaceOverride); v != nil {
		if ns, ok := v.(string); ok && ns != "" {
			return ns
		}
	}

	// Try query param as fallback (e.g. for public access or admin)
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		return ns
	}

	// Try header as fallback
	if ns := r.Header.Get("X-Namespace"); ns != "" {
		return ns
	}

	return "default"
}

// getWalletFromRequest extracts wallet address from JWT
func (h *ServerlessHandlers) getWalletFromRequest(r *http.Request) string {
	// 1. Try X-Wallet header (legacy/direct bypass)
	if wallet := r.Header.Get("X-Wallet"); wallet != "" {
		return wallet
	}

	// 2. Try JWT claims from context
	if v := r.Context().Value(ctxKeyJWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			subj := strings.TrimSpace(claims.Sub)
			// Ensure it's not an API key (standard Orama logic)
			if !strings.HasPrefix(strings.ToLower(subj), "ak_") && !strings.Contains(subj, ":") {
				return subj
			}
		}
	}

	// 3. Fallback to API key identity (namespace)
	if v := r.Context().Value(ctxKeyNamespaceOverride); v != nil {
		if ns, ok := v.(string); ok && ns != "" {
			return ns
		}
	}

	return ""
}

// HealthStatus returns the health status of the serverless engine
func (h *ServerlessHandlers) HealthStatus() map[string]interface{} {
	stats := h.wsManager.GetStats()
	return map[string]interface{}{
		"status":      "ok",
		"connections": stats.ConnectionCount,
		"topics":      stats.TopicCount,
	}
}

// handleFunctionTriggers handles trigger operations for a function
// Routes:
//   - GET    /v1/functions/{name}/triggers           - List all triggers
//   - POST   /v1/functions/{name}/triggers/pubsub    - Add pubsub trigger
//   - DELETE /v1/functions/{name}/triggers/{id}      - Remove trigger
func (h *ServerlessHandlers) handleFunctionTriggers(w http.ResponseWriter, r *http.Request, name string) {
	if h.triggerManager == nil {
		writeError(w, http.StatusServiceUnavailable, "Trigger management not available")
		return
	}

	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = h.getNamespaceFromRequest(r)
	}

	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace required")
		return
	}

	// Parse sub-path for trigger type or ID
	// Path after "triggers" could be: "", "pubsub", or "{trigger_id}"
	fullPath := r.URL.Path
	triggersIdx := strings.Index(fullPath, "/triggers")
	subPath := ""
	if triggersIdx > 0 {
		subPath = strings.TrimPrefix(fullPath[triggersIdx:], "/triggers")
		subPath = strings.TrimPrefix(subPath, "/")
	}

	switch r.Method {
	case http.MethodGet:
		h.listFunctionTriggers(w, r, namespace, name)
	case http.MethodPost:
		if subPath == "pubsub" {
			h.addPubSubTrigger(w, r, namespace, name)
		} else {
			writeError(w, http.StatusBadRequest, "Invalid trigger type. Use /triggers/pubsub")
		}
	case http.MethodDelete:
		if subPath == "" {
			writeError(w, http.StatusBadRequest, "Trigger ID required")
			return
		}
		h.removeFunctionTrigger(w, r, namespace, name, subPath)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// listFunctionTriggers handles GET /v1/functions/{name}/triggers
func (h *ServerlessHandlers) listFunctionTriggers(w http.ResponseWriter, r *http.Request, namespace, name string) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Get function to verify it exists and get its ID
	fn, err := h.registry.Get(ctx, namespace, name, 0)
	if err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
			return
		}
		h.logger.Error("Failed to get function",
			zap.String("name", name),
			zap.String("namespace", namespace),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to get function")
		return
	}

	// Get pubsub triggers
	pubsubTriggers, err := h.triggerManager.ListPubSubTriggers(ctx, fn.ID)
	if err != nil {
		h.logger.Error("Failed to list triggers",
			zap.String("function_id", fn.ID),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to list triggers")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":            name,
		"namespace":       namespace,
		"function_id":     fn.ID,
		"pubsub_triggers": pubsubTriggers,
		"count":           len(pubsubTriggers),
	})
}

// addPubSubTrigger handles POST /v1/functions/{name}/triggers/pubsub
func (h *ServerlessHandlers) addPubSubTrigger(w http.ResponseWriter, r *http.Request, namespace, name string) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Parse request body
	var req struct {
		Topic string `json:"topic"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	if req.Topic == "" {
		writeError(w, http.StatusBadRequest, "topic is required")
		return
	}

	// Get function to verify it exists and get its ID
	fn, err := h.registry.Get(ctx, namespace, name, 0)
	if err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
			return
		}
		h.logger.Error("Failed to get function",
			zap.String("name", name),
			zap.String("namespace", namespace),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to get function")
		return
	}

	// Add the trigger
	err = h.triggerManager.AddPubSubTrigger(ctx, fn.ID, req.Topic)
	if err != nil {
		if serverless.IsValidationError(err) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.logger.Error("Failed to add pubsub trigger",
			zap.String("function_id", fn.ID),
			zap.String("topic", req.Topic),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to add trigger")
		return
	}

	// Get the triggers to return the newly created one
	triggers, _ := h.triggerManager.ListPubSubTriggers(ctx, fn.ID)
	var newTrigger *serverless.PubSubTrigger
	for i := range triggers {
		if triggers[i].Topic == req.Topic {
			newTrigger = &triggers[i]
			break
		}
	}

	h.logger.Info("PubSub trigger added",
		zap.String("function", name),
		zap.String("namespace", namespace),
		zap.String("topic", req.Topic),
	)

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"message": "Trigger added successfully",
		"trigger": newTrigger,
	})
}

// removeFunctionTrigger handles DELETE /v1/functions/{name}/triggers/{id}
func (h *ServerlessHandlers) removeFunctionTrigger(w http.ResponseWriter, r *http.Request, namespace, name, triggerID string) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Verify function exists
	fn, err := h.registry.Get(ctx, namespace, name, 0)
	if err != nil {
		if serverless.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "Function not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to get function")
		return
	}

	// Remove the trigger
	err = h.triggerManager.RemoveTrigger(ctx, triggerID)
	if err != nil {
		if err == serverless.ErrTriggerNotFound {
			writeError(w, http.StatusNotFound, "Trigger not found")
			return
		}
		h.logger.Error("Failed to remove trigger",
			zap.String("trigger_id", triggerID),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to remove trigger")
		return
	}

	h.logger.Info("Trigger removed",
		zap.String("function", name),
		zap.String("function_id", fn.ID),
		zap.String("trigger_id", triggerID),
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":    "Trigger removed successfully",
		"trigger_id": triggerID,
	})
}
