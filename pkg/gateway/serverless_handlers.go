package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// ServerlessHandlers contains handlers for serverless function endpoints.
// It's a separate struct to keep the Gateway struct clean.
type ServerlessHandlers struct {
	invoker   *serverless.Invoker
	registry  serverless.FunctionRegistry
	wsManager *serverless.WSManager
	logger    *zap.Logger
}

// NewServerlessHandlers creates a new ServerlessHandlers instance.
func NewServerlessHandlers(
	invoker *serverless.Invoker,
	registry serverless.FunctionRegistry,
	wsManager *serverless.WSManager,
	logger *zap.Logger,
) *ServerlessHandlers {
	return &ServerlessHandlers{
		invoker:   invoker,
		registry:  registry,
		wsManager: wsManager,
		logger:    logger,
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

	if err := h.registry.Register(ctx, &def, wasmBytes); err != nil {
		h.logger.Error("Failed to deploy function",
			zap.String("name", def.Name),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to deploy: "+err.Error())
		return
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

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"message":  "Function deployed successfully",
		"function": fn,
	})
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
	// TODO: Implement log retrieval from function_logs table
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs":    []interface{}{},
		"message": "Log retrieval not yet implemented",
	})
}

// getNamespaceFromRequest extracts namespace from JWT or query param
func (h *ServerlessHandlers) getNamespaceFromRequest(r *http.Request) string {
	// Try query param first
	if ns := r.URL.Query().Get("namespace"); ns != "" {
		return ns
	}

	// Try to extract from JWT (if authentication middleware has set it)
	if ns := r.Header.Get("X-Namespace"); ns != "" {
		return ns
	}

	return "default"
}

// getWalletFromRequest extracts wallet address from JWT
func (h *ServerlessHandlers) getWalletFromRequest(r *http.Request) string {
	if wallet := r.Header.Get("X-Wallet"); wallet != "" {
		return wallet
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
