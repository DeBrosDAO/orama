// Package enroll implements the one-time enrollment server for OramaOS nodes.
//
// On first boot the agent prints a registration code on the console and listens
// on port 9999. The operator reads the code off the console and gives it to the
// gateway (`orama node enroll`). The gateway proves it holds that code by
// sending a payload that decrypts under it.
//
// The code is never served over the network. It used to be: a GET on / handed
// it to whoever asked first, which both published the secret and let anyone
// race the operator for it. It was also sent as an HTTP header on the
// completion POST, which put the seal key on the wire next to the ciphertext.
package enroll

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/DeBrosOfficial/orama-os/agent/internal/types"
)

// codeBytes is the size of a registration code. It is the only thing standing
// between a booting node and being enrolled into somebody else's cluster, and
// it keys the payload that carries the cluster secret, so it is a key and not
// an identifier. Ten bytes is 80 bits: hopeless to guess against a live
// listener and out of reach for an offline attack on a captured payload.
//
// It was four bytes. The operator reads it off a console, so this is twenty
// characters instead of eight.
const codeBytes = 10

// Result contains the enrollment data received from the Gateway.
type Result struct {
	NodeID          string       `json:"node_id"`
	WireGuardConfig string       `json:"wireguard_config"`
	ClusterSecret   string       `json:"cluster_secret"`
	Peers           []types.Peer `json:"peers"`
}

// completionResponse is what the agent returns to the gateway, sealed under the
// same registration code.
//
// AgentToken is minted here rather than by the gateway: it is this node's own
// credential, and the node is the only party that needs to hold it in the
// clear. The gateway sends it back on every command it issues.
type completionResponse struct {
	Status     string `json:"status"`
	AgentToken string `json:"agent_token"`
}

// Server is the enrollment HTTP server.
type Server struct {
	done chan struct{}
}

// NewServer creates a new enrollment server.
func NewServer() *Server {
	return &Server{
		done: make(chan struct{}),
	}
}

// Run starts the enrollment server and blocks until enrollment is complete.
// Returns the enrollment result containing cluster configuration, and the
// token the gateway must present on every later command to this node.
func (s *Server) Run() (*Result, string, error) {
	code, err := generateCode()
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate registration code: %w", err)
	}
	agentToken, err := generateAgentToken()
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate agent token: %w", err)
	}

	// The console is the only place this is printed. The journal used to
	// receive it via log.Printf, so anyone who could read `orama node logs`
	// during the window had the seal key.
	printEnrollmentCode(code)
	log.Printf("Waiting for enrollment on port 9999...")

	enrollCh := make(chan *Result, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent/enroll/complete", s.completeHandler(code, agentToken, enrollCh))

	server := &http.Server{
		Addr:         ":9999",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- fmt.Errorf("enrollment server error: %w", err)
		}
	}()

	select {
	case result := <-enrollCh:
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		server.Shutdown(ctx)
		log.Println("enrollment server closed")
		return result, agentToken, nil
	case err := <-errCh:
		return nil, "", err
	}
}

// printEnrollmentCode writes the registration code to the console TTY, not the
// process logger. The unit captures stdout/stderr into the journal.
func printEnrollmentCode(code string) {
	line := fmt.Sprintf("ENROLLMENT CODE: %s\n", code)
	f, err := os.OpenFile("/dev/console", os.O_WRONLY, 0)
	if err != nil {
		// Tests and hosts without a console still need the operator to see it.
		_, _ = os.Stderr.WriteString(line)
		return
	}
	defer f.Close()
	_, _ = io.WriteString(f, line)
}

// completeHandler accepts the cluster configuration, from a caller that proves
// it holds the registration code by producing a payload that decrypts under it.
//
// This endpoint used to accept any POST at all: reaching a booting node before
// its operator's gateway did was enough to enrol it into another cluster, with
// another cluster's WireGuard peers. It then required the code in a header,
// which put the seal key on the wire next to the ciphertext.
func (s *Server) completeHandler(code, agentToken string, enrolled chan<- *Result) http.HandlerFunc {
	var taken atomic.Bool
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "could not read the request body", http.StatusBadRequest)
			return
		}

		plaintext, err := Open(code, string(body))
		if err != nil {
			log.Printf("refused an enrollment payload that did not decrypt from %s: %v", r.RemoteAddr, err)
			http.Error(w, "the enrollment payload did not decrypt", http.StatusBadRequest)
			return
		}

		var result Result
		if err := json.Unmarshal(plaintext, &result); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		sealed, err := Seal(code, mustJSON(completionResponse{Status: "ok", AgentToken: agentToken}))
		if err != nil {
			http.Error(w, "could not seal the response", http.StatusInternalServerError)
			return
		}

		if !taken.CompareAndSwap(false, true) {
			http.Error(w, "enrollment already complete", http.StatusConflict)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, sealed)

		enrolled <- &result
	}
}

func mustJSON(v any) []byte {
	// completionResponse is two strings; it cannot fail to marshal.
	b, _ := json.Marshal(v)
	return b
}

// generateCode generates a registration code the operator reads off the
// console. See codeBytes for why it is this long.
func generateCode() (string, error) {
	b := make([]byte, codeBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateAgentToken generates this node's own credential. Every command the
// gateway later sends to the agent has to carry it.
func generateAgentToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
