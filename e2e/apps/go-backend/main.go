// Command app is the reference Go backend: a notes service whose bodies live
// in the namespace's IPFS and whose index lives in the namespace's cache,
// both reached through the namespace gateway as the app itself (its own
// renewed workload token, granted the runtime role). It keeps a visit counter
// in $ORAMA_STATE_DIR to show the one directory it may write.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	// notesMap is the cache dmap the note index lives in.
	notesMap = "ref-notes"
	// release is replaced per build, so an update and a rollback are
	// visible in what the running binary says.
	release = "RELEASE_MARKER"
)

type server struct {
	gw     *gateway
	mu     sync.Mutex
	visits int
}

func main() {
	gw, err := newGateway()
	if err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		os.Exit(1)
	}
	go gw.renewLoop()
	s := &server{gw: gw}
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { reply(w, http.StatusOK, map[string]any{"ok": true}) })
	http.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, map[string]any{"version": release, "env": os.Getenv("APP_VERSION")})
	})
	http.HandleFunc("/visits", s.handleVisits)
	http.HandleFunc("/renew", s.handleRenew)
	http.HandleFunc("/notes", s.handleCreate)
	http.HandleFunc("/notes/", s.handleRead)
	if err := http.ListenAndServe(":"+os.Getenv("PORT"), nil); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fail passes a gateway refusal on with its status (so a revoked grant is
// visible to the caller as the gateway's 401/403), anything else as 502.
func fail(w http.ResponseWriter, what string, err error) {
	status := http.StatusBadGateway
	var ge *gatewayError
	if errors.As(err, &ge) {
		status = ge.Status
	}
	reply(w, status, map[string]any{"error": what + ": " + err.Error()})
}

func (s *server) handleRenew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		reply(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST"})
		return
	}
	res, err := s.gw.renew()
	if err != nil {
		fail(w, "renew", err)
		return
	}
	reply(w, http.StatusOK, res)
}

// handleCreate stores a note: body to IPFS, cid indexed in the cache.
func (s *server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		reply(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || len(body) == 0 {
		reply(w, http.StatusBadRequest, map[string]any{"error": "a note needs a body"})
		return
	}
	id := newID()
	cid, err := s.gw.upload(id+".txt", body)
	if err != nil {
		fail(w, "upload", err)
		return
	}
	if err := s.gw.postJSON("/v1/cache/put", map[string]any{"dmap": notesMap, "key": id, "value": cid}, nil); err != nil {
		fail(w, "index", err)
		return
	}
	reply(w, http.StatusCreated, map[string]any{"id": id, "cid": cid})
}

// handleRead resolves a note through the cache and downloads its body.
func (s *server) handleRead(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/notes/")
	var entry struct {
		Value string `json:"value"`
	}
	if err := s.gw.postJSON("/v1/cache/get", map[string]any{"dmap": notesMap, "key": id}, &entry); err != nil {
		fail(w, "lookup", err)
		return
	}
	var text []byte
	if err := s.gw.call(http.MethodGet, "/v1/storage/get/"+entry.Value, "", nil, &text); err != nil {
		fail(w, "download", err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"id": id, "cid": entry.Value, "text": string(text)})
}

// handleVisits counts visits in the state directory, which survives restarts.
func (s *server) handleVisits(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(os.Getenv("ORAMA_STATE_DIR"), "visits")
	if s.visits == 0 {
		if raw, err := os.ReadFile(path); err == nil {
			s.visits, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
	}
	s.visits++
	if err := os.WriteFile(path, []byte(strconv.Itoa(s.visits)), 0o600); err != nil {
		reply(w, http.StatusInternalServerError, map[string]any{"error": "state dir: " + err.Error()})
		return
	}
	reply(w, http.StatusOK, map[string]any{"visits": s.visits})
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
