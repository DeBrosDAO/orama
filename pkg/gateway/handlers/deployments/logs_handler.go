package deployments

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"go.uber.org/zap"
)

// LogsHandler handles deployment logs
type LogsHandler struct {
	service        *DeploymentService
	processManager *process.Manager
	logger         *zap.Logger
}

// NewLogsHandler creates a new logs handler
func NewLogsHandler(service *DeploymentService, processManager *process.Manager, logger *zap.Logger) *LogsHandler {
	return &LogsHandler{
		service:        service,
		processManager: processManager,
		logger:         logger,
	}
}

// HandleLogs streams deployment logs
func (h *LogsHandler) HandleLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := ctx.Value("namespace").(string)
	name := r.URL.Query().Get("name")

	if name == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}

	// Parse parameters
	lines := 100
	if linesStr := r.URL.Query().Get("lines"); linesStr != "" {
		if l, err := strconv.Atoi(linesStr); err == nil {
			lines = l
		}
	}

	follow := false
	if followStr := r.URL.Query().Get("follow"); followStr == "true" {
		follow = true
	}

	h.logger.Info("Streaming logs",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.Int("lines", lines),
		zap.Bool("follow", follow),
	)

	// Get deployment
	deployment, err := h.service.GetDeployment(ctx, namespace, name)
	if err != nil {
		if err == deployments.ErrDeploymentNotFound {
			http.Error(w, "Deployment not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to get deployment", http.StatusInternalServerError)
		}
		return
	}

	// Check if deployment has logs (only dynamic deployments)
	if deployment.Port == 0 {
		http.Error(w, "Static deployments do not have logs", http.StatusBadRequest)
		return
	}

	// Get logs from process manager
	logs, err := h.processManager.GetLogs(ctx, deployment, lines, follow)
	if err != nil {
		h.logger.Error("Failed to get logs", zap.Error(err))
		http.Error(w, "Failed to get logs", http.StatusInternalServerError)
		return
	}

	// Set headers for streaming
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Stream logs
	if follow {
		// For follow mode, stream continuously
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		scanner := bufio.NewScanner(strings.NewReader(string(logs)))
		for scanner.Scan() {
			fmt.Fprintf(w, "%s\n", scanner.Text())
			flusher.Flush()
		}
	} else {
		// For non-follow mode, write all logs at once
		w.Write(logs)
	}
}

// HandleGetEvents gets deployment events
func (h *LogsHandler) HandleGetEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := ctx.Value("namespace").(string)
	name := r.URL.Query().Get("name")

	if name == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}

	// Get deployment
	deployment, err := h.service.GetDeployment(ctx, namespace, name)
	if err != nil {
		http.Error(w, "Deployment not found", http.StatusNotFound)
		return
	}

	// Query events
	type eventRow struct {
		EventType string `db:"event_type"`
		Message   string `db:"message"`
		CreatedAt string `db:"created_at"`
	}

	var rows []eventRow
	query := `
		SELECT event_type, message, created_at
		FROM deployment_events
		WHERE deployment_id = ?
		ORDER BY created_at DESC
		LIMIT 100
	`

	err = h.service.db.Query(ctx, &rows, query, deployment.ID)
	if err != nil {
		h.logger.Error("Failed to query events", zap.Error(err))
		http.Error(w, "Failed to query events", http.StatusInternalServerError)
		return
	}

	events := make([]map[string]interface{}, len(rows))
	for i, row := range rows {
		events[i] = map[string]interface{}{
			"event_type": row.EventType,
			"message":    row.Message,
			"created_at": row.CreatedAt,
		}
	}

	resp := map[string]interface{}{
		"deployment_name": name,
		"events":          events,
		"total":           len(events),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
