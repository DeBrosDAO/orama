// Package command implements the command receiver that accepts instructions
// from the Gateway over WireGuard.
//
// The agent listens on a local HTTP endpoint (only accessible via WG) for
// commands like restart, status, logs, and leave.
package command

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/orama-os/agent/internal/sandbox"
)

const (
	// ListenAddr is the address for the command receiver (WG-only).
	ListenAddr = ":9998"
)

// Command represents an incoming command from the Gateway.
type Command struct {
	Action  string `json:"action"`  // "restart", "status", "logs", "leave"
	Service string `json:"service"` // optional: specific service name
}

// Receiver listens for commands from the Gateway.
type Receiver struct {
	supervisor *sandbox.Supervisor
	server     *http.Server
}

// NewReceiver creates a new command receiver.
func NewReceiver(supervisor *sandbox.Supervisor) *Receiver {
	return &Receiver{
		supervisor: supervisor,
	}
}

// Listen starts the HTTP server for receiving commands.
func (r *Receiver) Listen() {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/agent/command", r.handleCommand)
	mux.HandleFunc("/v1/agent/status", r.handleStatus)
	mux.HandleFunc("/v1/agent/health", r.handleHealth)
	mux.HandleFunc("/v1/agent/logs", r.handleLogs)

	r.server = &http.Server{
		Addr:         ListenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Printf("command receiver listening on %s", ListenAddr)
	if err := r.server.ListenAndServe(); err != http.ErrServerClosed {
		log.Printf("command receiver error: %v", err)
	}
}

// Stop gracefully shuts down the command receiver.
func (r *Receiver) Stop() {
	if r.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.server.Shutdown(ctx)
	}
}

func (r *Receiver) handleCommand(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var cmd Command
	if err := json.NewDecoder(req.Body).Decode(&cmd); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	log.Printf("received command: %s (service: %s)", cmd.Action, cmd.Service)

	switch cmd.Action {
	case "restart":
		if cmd.Service == "" {
			http.Error(w, "service name required for restart", http.StatusBadRequest)
			return
		}
		if err := r.supervisor.RestartService(cmd.Service); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})

	case "status":
		status := r.supervisor.GetStatus()
		writeJSON(w, http.StatusOK, status)

	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action: " + cmd.Action})
	}
}

func (r *Receiver) handleStatus(w http.ResponseWriter, req *http.Request) {
	status := r.supervisor.GetStatus()
	writeJSON(w, http.StatusOK, status)
}

func (r *Receiver) handleHealth(w http.ResponseWriter, req *http.Request) {
	status := r.supervisor.GetStatus()

	healthy := true
	for _, running := range status {
		if !running {
			healthy = false
			break
		}
	}

	result := map[string]interface{}{
		"healthy":  healthy,
		"services": status,
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Receiver) handleLogs(w http.ResponseWriter, req *http.Request) {
	service := req.URL.Query().Get("service")
	if service == "" {
		service = "all"
	}

	linesParam := req.URL.Query().Get("lines")
	maxLines := 100
	if linesParam != "" {
		if n, err := parseInt(linesParam); err == nil && n > 0 {
			maxLines = n
			if maxLines > 1000 {
				maxLines = 1000
			}
		}
	}

	const logsDir = "/opt/orama/.orama/logs"
	result := make(map[string]string)

	if service == "all" {
		// Return tail of each service log
		services := []string{"rqlite", "olric", "ipfs", "ipfs-cluster", "gateway", "coredns"}
		for _, svc := range services {
			logPath := logsDir + "/" + svc + ".log"
			lines := tailFile(logPath, maxLines)
			result[svc] = lines
		}
	} else {
		logPath := logsDir + "/" + service + ".log"
		result[service] = tailFile(logPath, maxLines)
	}

	writeJSON(w, http.StatusOK, result)
}

func tailFile(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func parseInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}
