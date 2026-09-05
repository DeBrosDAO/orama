// Package command implements the command receiver that accepts instructions
// from the Gateway over WireGuard.
//
// The agent listens on a local HTTP endpoint (only accessible via WG) for
// commands like restart, status, logs, and leave.
package command

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/orama-os/agent/internal/sandbox"
)

const (
	// ListenPort is the port the command receiver listens on. The address it
	// binds is the node's WireGuard address, passed in at construction — the
	// constant used to be ":9998" with a comment claiming it was WireGuard
	// only, which bound every interface including the public one.
	ListenPort = 9998
	logsDir    = "/opt/orama/.orama/logs"

	// HeaderAuthorization carries the node's agent token as a bearer.
	HeaderAuthorization = "Authorization"
)

// knownLogServices is the only set of log files the agent will read
// (bugboard #93). Query `service` is not concatenated into a path.
var knownLogServices = []string{"rqlite", "olric", "ipfs", "ipfs-cluster", "gateway", "coredns", "agent"}

func allowedLogService(service string) bool {
	if service == "" || strings.ContainsAny(service, `/\:`) || strings.Contains(service, "..") {
		return false
	}
	for _, s := range knownLogServices {
		if s == service {
			return true
		}
	}
	return false
}

func logPathForService(service string) string {
	return logsDir + "/" + service + ".log"
}

// Command represents an incoming command from the Gateway.
type Command struct {
	Action  string `json:"action"`  // "restart", "status", "logs", "leave"
	Service string `json:"service"` // optional: specific service name
}

// Receiver listens for commands from the Gateway.
type Receiver struct {
	supervisor *sandbox.Supervisor
	server     *http.Server

	// wgIP is the node's address on the overlay. It is kept as the address
	// rather than as a joined "host:port" because net.JoinHostPort("", port)
	// is ":9998" — a non-empty string that binds every interface, which is
	// exactly the state this exists to prevent.
	wgIP string

	// token is this node's own credential, minted at enrollment. Every request
	// has to present it. Without it, restarting any service on any OramaOS node
	// took one unauthenticated POST from anywhere that could route to the node.
	token string
}

// NewReceiver creates a command receiver bound to the node's WireGuard address
// and authenticated by the node's agent token.
func NewReceiver(supervisor *sandbox.Supervisor, wgIP, token string) *Receiver {
	return &Receiver{
		supervisor: supervisor,
		wgIP:       strings.TrimSpace(wgIP),
		token:      strings.TrimSpace(token),
	}
}

// Listen starts the HTTP server for receiving commands.
//
// It refuses to start without an address and a token: a receiver that binds
// everything and authenticates nothing is what this exists to prevent, and
// falling back to that on a misconfiguration would defeat the point.
func (r *Receiver) Listen() {
	if r.wgIP == "" || r.token == "" {
		log.Printf("command receiver not started: it needs the node's WireGuard address " +
			"and agent token, and will not listen on every interface without a credential")
		return
	}
	listenAddr := net.JoinHostPort(r.wgIP, strconv.Itoa(ListenPort))

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent/command", r.authenticated(r.handleCommand))
	mux.HandleFunc("/v1/agent/status", r.authenticated(r.handleStatus))
	mux.HandleFunc("/v1/agent/health", r.authenticated(r.handleHealth))
	mux.HandleFunc("/v1/agent/logs", r.authenticated(r.handleLogs))

	r.server = &http.Server{
		Addr:         listenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Printf("command receiver listening on %s", listenAddr)
	if err := r.server.ListenAndServe(); err != http.ErrServerClosed {
		log.Printf("command receiver error: %v", err)
	}
}

// authenticated refuses anything that does not present the node's agent token.
//
// Being on the WireGuard mesh is not the credential: every namespace's services
// are on that mesh, and one of them being compromised should not mean every
// node's services can be restarted.
func (r *Receiver) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		presented := strings.TrimPrefix(req.Header.Get(HeaderAuthorization), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(presented)), []byte(r.token)) != 1 {
			log.Printf("refused an unauthenticated agent request from %s for %s",
				req.RemoteAddr, req.URL.Path)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, req)
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

	result := make(map[string]string)

	if service == "all" {
		for _, svc := range knownLogServices {
			result[svc] = tailFile(logPathForService(svc), maxLines)
		}
	} else {
		if !allowedLogService(service) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown service"})
			return
		}
		result[service] = tailFile(logPathForService(service), maxLines)
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
