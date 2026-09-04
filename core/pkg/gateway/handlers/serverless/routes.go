package serverless

import (
	"net/http"
	"strconv"
	"strings"
)

// RegisterRoutes registers all serverless routes on the given mux.
func (h *ServerlessHandlers) RegisterRoutes(mux *http.ServeMux) {
	// Function management
	mux.HandleFunc("/v1/functions", h.handleFunctions)
	mux.HandleFunc("/v1/functions/", h.handleFunctionByName)

	// Direct invoke endpoint
	mux.HandleFunc("/v1/invoke/", h.HandleInvoke)

	// WS connection metrics (operator visibility)
	mux.HandleFunc("/v1/serverless/ws/connections", h.WSConnections)
	mux.HandleFunc("/v1/serverless/ws/connections/", h.WSConnections)
}

// handleFunctions handles GET /v1/functions (list) and POST /v1/functions (deploy)
func (h *ServerlessHandlers) handleFunctions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.ListFunctions(w, r)
	case http.MethodPost:
		h.DeployFunction(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleFunctionByName handles operations on a specific function
// Routes:
//   - GET    /v1/functions/{name}                    - Get function info
//   - DELETE /v1/functions/{name}                    - Delete function
//   - POST   /v1/functions/{name}/invoke             - Invoke function
//   - POST   /v1/functions/{name}/disable            - Pause without redeploy (plan 11.5)
//   - POST   /v1/functions/{name}/enable             - Resume (plan 11.5)
//   - GET    /v1/functions/{name}/versions           - List versions
//   - GET    /v1/functions/{name}/logs               - Get logs
//   - WS     /v1/functions/{name}/ws                 - WebSocket invoke
//   - POST   /v1/functions/{name}/triggers           - Add trigger
//   - GET    /v1/functions/{name}/triggers           - List triggers
//   - DELETE /v1/functions/{name}/triggers/{id}      - Remove trigger
//   - PUT    /v1/functions/secrets                   - Set a secret
//   - GET    /v1/functions/secrets                   - List secrets
//   - DELETE /v1/functions/secrets/{name}            - Delete a secret
func (h *ServerlessHandlers) handleFunctionByName(w http.ResponseWriter, r *http.Request) {
	// Parse path: /v1/functions/{name}[/{action}[/{subID}]]
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

	// Handle secrets management: /v1/functions/secrets[/{secretName}]
	if name == "secrets" {
		secretName := action // empty for list/set, secret name for delete
		switch {
		case secretName != "" && r.Method == http.MethodDelete:
			h.HandleDeleteSecret(w, r, secretName)
		case secretName == "" && r.Method == http.MethodPut:
			h.HandleSetSecret(w, r)
		case secretName == "" && r.Method == http.MethodGet:
			h.HandleListSecrets(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
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

	// Handle triggers sub-path: "triggers" or "triggers/{triggerID}"
	triggerID := ""
	if strings.HasPrefix(action, "triggers/") {
		triggerID = strings.TrimPrefix(action, "triggers/")
		action = "triggers"
	}

	switch action {
	case "invoke":
		h.InvokeFunction(w, r, name, version)
	case "disable":
		h.SetEnabledFunction(w, r, name, false)
	case "enable":
		h.SetEnabledFunction(w, r, name, true)
	case "ws":
		h.HandleWebSocket(w, r, name, version)
	case "versions":
		h.ListVersions(w, r, name)
	case "logs":
		h.GetFunctionLogs(w, r, name)
	case "triggers":
		switch {
		case triggerID != "" && r.Method == http.MethodDelete:
			h.HandleDeleteTrigger(w, r, name, triggerID)
		case r.Method == http.MethodPost:
			h.HandleAddTrigger(w, r, name)
		case r.Method == http.MethodGet:
			h.HandleListTriggers(w, r, name)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	case "":
		switch r.Method {
		case http.MethodGet:
			h.GetFunctionInfo(w, r, name, version)
		case http.MethodDelete:
			h.DeleteFunction(w, r, name, version)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	default:
		http.Error(w, "Unknown action", http.StatusNotFound)
	}
}

// Routes is every pattern RegisterRoutes registers. The gateway checks each
// against its policy table before wiring them, so a route added here without a
// declared policy fails to start rather than serving one nobody decided about.
func Routes() []string {
	return []string{
		"/v1/functions",
		"/v1/functions/",
		"/v1/invoke/",
		"/v1/serverless/ws/connections",
		"/v1/serverless/ws/connections/",
	}
}
