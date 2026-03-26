// Package enroll implements the one-time enrollment server for OramaOS nodes.
//
// On first boot, the agent starts an HTTP server on port 9999 that serves
// a registration code. The operator retrieves this code and provides it to
// the Gateway (via `orama node enroll`). The Gateway then pushes cluster
// configuration back to the agent via WebSocket.
package enroll

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/DeBrosOfficial/orama-os/agent/internal/types"
)

// Result contains the enrollment data received from the Gateway.
type Result struct {
	NodeID          string       `json:"node_id"`
	WireGuardConfig string       `json:"wireguard_config"`
	ClusterSecret   string       `json:"cluster_secret"`
	Peers           []types.Peer `json:"peers"`
}

// Server is the enrollment HTTP server.
type Server struct {
	gatewayURL string
	result     *Result
	mu         sync.Mutex
	done       chan struct{}
}

// NewServer creates a new enrollment server.
func NewServer(gatewayURL string) *Server {
	return &Server{
		gatewayURL: gatewayURL,
		done:       make(chan struct{}),
	}
}

// Run starts the enrollment server and blocks until enrollment is complete.
// Returns the enrollment result containing cluster configuration.
func (s *Server) Run() (*Result, error) {
	// Generate registration code (8 alphanumeric chars)
	code, err := generateCode()
	if err != nil {
		return nil, fmt.Errorf("failed to generate registration code: %w", err)
	}

	log.Printf("ENROLLMENT CODE: %s", code)
	log.Printf("Waiting for enrollment on port 9999...")

	// Channel for enrollment completion
	enrollCh := make(chan *Result, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()

	// Serve registration code — one-shot endpoint
	var served bool
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if served {
			s.mu.Unlock()
			http.Error(w, "already served", http.StatusGone)
			return
		}
		served = true
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"code":    code,
			"expires": time.Now().Add(10 * time.Minute).Format(time.RFC3339),
		})
	})

	// Receive enrollment config from Gateway (pushed after code verification)
	mux.HandleFunc("/v1/agent/enroll/complete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var result Result
		if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		enrollCh <- &result
	})

	server := &http.Server{
		Addr:         ":9999",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Start server in background
	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- fmt.Errorf("enrollment server error: %w", err)
		}
	}()

	// Wait for enrollment or error
	select {
	case result := <-enrollCh:
		// Gracefully shut down the enrollment server
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		server.Shutdown(ctx)
		log.Println("enrollment server closed")
		return result, nil
	case err := <-errCh:
		return nil, err
	}
}

// generateCode generates an 8-character alphanumeric registration code.
func generateCode() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
