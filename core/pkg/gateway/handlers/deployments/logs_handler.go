package deployments

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"go.uber.org/zap"
)

// defaultLogLines is how many lines a request without lines= gets.
const defaultLogLines = 100

// maxConcurrentLogReadsPerNamespace bounds the journal reads one namespace has
// running at once. Each holds one of the privileged helper's shared slots, which
// deploys, restarts and node reports also need, so a tenant polling logs must
// not be able to take them all.
const maxConcurrentLogReadsPerNamespace = 2

// logReadRetryAfterSeconds is what a refused read is told to wait.
const logReadRetryAfterSeconds = "1"

// logsTruncatedHeader tells the client the oldest requested lines were cut to
// fit the helper's response limit; the log text itself carries no notice.
const logsTruncatedHeader = "X-Logs-Truncated"

// LogsHandler handles deployment logs
type LogsHandler struct {
	service        *DeploymentService
	processManager *process.Manager
	logger         *zap.Logger

	mu       sync.Mutex
	inflight map[string]int // journal reads running, by namespace
}

// acquireLogRead takes one of namespace's read slots; false when none is free.
func (h *LogsHandler) acquireLogRead(namespace string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inflight[namespace] >= maxConcurrentLogReadsPerNamespace {
		return false
	}
	if h.inflight == nil {
		h.inflight = make(map[string]int)
	}
	h.inflight[namespace]++
	return true
}

func (h *LogsHandler) releaseLogRead(namespace string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inflight[namespace]--; h.inflight[namespace] <= 0 {
		delete(h.inflight, namespace)
	}
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
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}
	name := r.URL.Query().Get("name")

	if name == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}

	lines := defaultLogLines
	if linesStr := r.URL.Query().Get("lines"); linesStr != "" {
		n, err := privhelper.ParseJournalLines(linesStr)
		if err != nil {
			http.Error(w, "invalid lines: "+err.Error(), http.StatusBadRequest)
			return
		}
		lines = n
	}

	// A follow would have to hold the journal open through the helper, which
	// answers once; refuse it instead of returning a snapshot as if it followed.
	if r.URL.Query().Get("follow") == "true" {
		http.Error(w, "following logs is not supported; re-run without --follow to read the last lines", http.StatusNotImplemented)
		return
	}

	if !h.acquireLogRead(namespace) {
		w.Header().Set("Retry-After", logReadRetryAfterSeconds)
		http.Error(w, "too many log reads of this namespace are running; retry shortly", http.StatusTooManyRequests)
		return
	}
	defer h.releaseLogRead(namespace)

	h.logger.Info("Streaming logs",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.Int("lines", lines),
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
	logs, truncated, err := h.processManager.GetLogs(ctx, deployment, lines)
	if err != nil {
		h.logger.Error("Failed to get logs", zap.String("namespace", namespace), zap.String("name", name), zap.Error(err))
		http.Error(w, "failed to read the logs of "+name+" on its node; the gateway journal on that node has the cause", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if truncated {
		w.Header().Set(logsTruncatedHeader, "true")
	}
	w.Write(logs)
}

// HandleGetEvents gets deployment events
func (h *LogsHandler) HandleGetEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}
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
