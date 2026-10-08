package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/tlsstore"
	"go.uber.org/zap"
)

// The cluster's certificate store, for this node's Caddy (bugboard #751).
//
// POST /v1/internal/tls-store carries one CertMagic storage operation. The
// call must carry a coordination v2 stamp — a MAC over method, path and body,
// with a single-use nonce — made with the store's MAC key, which Caddy reads
// from /etc/caddy and this gateway derives from the cluster secret. Anything
// unstamped is answered 404, so the route does not confirm it exists.
//
// Values arrive sealed and leave sealed: this path stores what Caddy sealed and
// never opens it. (The same process opens the `*.<base>` pair to export it for
// TURN, tls_export.go.)
//
// Only the cluster gateway answers, and only to a caller on loopback: Caddy is
// on the same host, and the stamp's key is the whole cluster's, so a stamp
// captured on one node is refused by every other. A namespace gateway's
// database is its tenant's, which holds no store.

// tlsStoreRequest is one storage operation.
type tlsStoreRequest struct {
	Op        string `json:"op"`
	Key       string `json:"key,omitempty"`
	Value     string `json:"value,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
	Holder    string `json:"holder,omitempty"`
	LeaseMS   int64  `json:"lease_ms,omitempty"`
}

// tlsStoreResponse is its answer. Exists is false when a load or stat names a
// key the store does not hold: an answer, not a failure, so a refused or
// broken call is never read as "no certificate yet" and answered by obtaining
// a new one.
type tlsStoreResponse struct {
	Exists     *bool    `json:"exists,omitempty"`
	Value      string   `json:"value,omitempty"`
	Keys       []string `json:"keys,omitempty"`
	Size       int64    `json:"size,omitempty"`
	ModifiedMS int64    `json:"modified_ms,omitempty"`
	Terminal   bool     `json:"terminal,omitempty"`
	Acquired   bool     `json:"acquired,omitempty"`
}

// tlsStoreHolder is a lock holder: the random id one Lock call drew.
var tlsStoreHolder = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (g *Gateway) tlsStoreHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || g.cfg == nil || isNamespaceGateway(g.cfg) || !fromLoopback(r) {
		http.NotFound(w, r)
		return
	}
	keys, err := tlsstore.KeysFromClusterSecret(g.cfg.ClusterSecret)
	if err != nil || !nodeauth.VerifyCoordinationV2(keys.MAC, r, time.Now(), tlsstore.MACAudience) {
		http.NotFound(w, r)
		return
	}
	if g.sqlDB == nil || !g.tlsStoreReady.Load() {
		http.Error(w, "TLS store not open yet: this node's certificates are being imported", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, nodeauth.CoordinationMaxBody))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	var req tlsStoreRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "body is not a TLS store request", http.StatusBadRequest)
		return
	}
	if err := checkTLSStoreRequest(req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := g.runTLSStoreOp(r, req)
	switch {
	case errors.Is(err, tlsstore.ErrLockNotHeld):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		g.logger.Error("TLS store operation failed",
			zap.String("op", req.Op), zap.String("key", req.Key), zap.Error(err))
		http.Error(w, "TLS store unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (g *Gateway) runTLSStoreOp(r *http.Request, req tlsStoreRequest) (tlsStoreResponse, error) {
	ctx, store := r.Context(), tlsstore.NewStore(g.sqlDB)
	lease := time.Duration(req.LeaseMS) * time.Millisecond
	switch req.Op {
	case "load":
		value, err := store.Load(ctx, req.Key)
		if tlsstore.IsNotExist(err) {
			return tlsStoreResponse{Exists: boolPtr(false)}, nil
		}
		return tlsStoreResponse{Exists: boolPtr(true), Value: value}, err
	case "stat":
		info, err := store.Stat(ctx, req.Key)
		if tlsstore.IsNotExist(err) {
			return tlsStoreResponse{Exists: boolPtr(false)}, nil
		}
		return tlsStoreResponse{Exists: boolPtr(true), Size: info.Size,
			ModifiedMS: info.Modified.UnixMilli(), Terminal: info.IsTerminal}, err
	case "store":
		return tlsStoreResponse{}, store.Put(ctx, req.Key, req.Value)
	case "delete":
		return tlsStoreResponse{}, store.Delete(ctx, req.Key)
	case "list":
		keys, err := store.List(ctx, req.Key, req.Recursive)
		return tlsStoreResponse{Keys: keys}, err
	case "lock":
		ok, err := store.TryLock(ctx, req.Key, req.Holder, lease)
		return tlsStoreResponse{Acquired: ok}, err
	case "renew":
		return tlsStoreResponse{}, store.Renew(ctx, req.Key, req.Holder, lease)
	default: // "unlock"; checkTLSStoreRequest admits nothing else
		return tlsStoreResponse{}, store.Unlock(ctx, req.Key, req.Holder)
	}
}

// checkTLSStoreRequest validates what each operation needs. A list may name
// no key (the whole store); every other operation names a valid one. A stored
// value must already be sealed: the store never holds a key in the clear.
func checkTLSStoreRequest(req tlsStoreRequest) error {
	switch req.Op {
	case "load", "stat", "store", "delete", "list", "lock", "renew", "unlock":
	default:
		return errors.New("unknown op")
	}
	if !(req.Op == "list" && req.Key == "") {
		if err := tlsstore.ValidKey(req.Key); err != nil {
			return err
		}
	}
	switch req.Op {
	case "store":
		if len(req.Value) > tlsstore.MaxValueLen || !tlsstore.IsSealed(req.Value) {
			return errors.New("value must be a sealed value of at most 256 KiB")
		}
	case "lock", "renew", "unlock":
		if !tlsStoreHolder.MatchString(req.Holder) {
			return errors.New("holder must be 32 lower-case hex characters")
		}
	}
	if req.Op == "lock" || req.Op == "renew" {
		if lease := time.Duration(req.LeaseMS) * time.Millisecond; lease <= 0 || lease > tlsstore.MaxLease {
			return errors.New("lease_ms is out of range")
		}
	}
	return nil
}

func boolPtr(b bool) *bool { return &b }

// fromLoopback reports whether r came from this host's loopback.
func fromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
