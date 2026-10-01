// Package backup serves a namespace gateway's backup and restore routes.
//
// A backup is sealed on the gateway to a public key the owner supplies; the
// gateway never holds the private key. A restore is opened on the owner's
// machine, which re-seals the secrets to this gateway's restore key and sends
// the result here. See pkg/nsbackup for the formats.
package backup

import (
	"context"
	"fmt"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	"go.uber.org/zap"
)

// Snapshotter is the namespace RQLite's native snapshot: /db/backup to read
// the whole database and /db/load to replace it.
type Snapshotter interface {
	Backup(ctx context.Context) ([]byte, error)
	Load(ctx context.Context, db []byte) error
}

// Pinner pins a CID in the cluster's IPFS.
type Pinner interface {
	Pin(ctx context.Context, cid string, name string, replicationFactor int) (*ipfs.PinResponse, error)
}

// DB is the namespace's own RQLite.
type DB interface {
	Query(ctx context.Context, dest any, query string, args ...any) error
	Batch(ctx context.Context, ops []rqlite.BatchOp) (*rqlite.BatchResult, error)
}

// Auditor records a backup or a restore.
type Auditor interface {
	RecordFromRequest(ctx context.Context, r *http.Request, event auth.AuditEvent)
}

// Caller resolves the namespace a request's credential belongs to, and
// whether that credential is the namespace owner's.
type Caller func(r *http.Request) (namespace string, owner bool)

// Config is everything a Handler needs. Every field is required.
type Config struct {
	// Namespace is the namespace this gateway serves.
	Namespace string
	// DB is this namespace gateway's own RQLite: the snapshot, the restore,
	// and the namespace's stored objects and quota.
	DB DB
	// Registry is the cluster registry, where the namespace's deployments
	// live. Read from DB they were an empty table, and every backup left out
	// the content and builds of the namespace's deployments.
	Registry          DB
	Snapshots         Snapshotter
	Pins              Pinner
	Root              func() secrets.Root
	ReplicationFactor int
	Caller            Caller
	Audit             Auditor
	Logger            *zap.Logger
	// Slot is shared with the gateway's other whole-database routes. Nil gets
	// a slot of its own.
	Slot *Slot
}

// Slot admits one whole-database transfer at a time on a gateway: a backup, a
// restore, an RQLite export or an RQLite import. Each holds a snapshot of the
// database in memory or keeps a connection to RQLite open for minutes, so one
// owner could otherwise run as many as the gateway would accept.
type Slot struct{ busy chan struct{} }

// NewSlot returns a free Slot.
func NewSlot() *Slot { return &Slot{busy: make(chan struct{}, 1)} }

// Begin takes the slot, or writes 429 and returns false when a transfer is
// running.
func (s *Slot) Begin(w http.ResponseWriter) (release func(), ok bool) {
	select {
	case s.busy <- struct{}{}:
		return func() { <-s.busy }, true
	default:
		w.Header().Set("Retry-After", retryAfterSeconds)
		httputil.WriteError(w, http.StatusTooManyRequests,
			"another backup, restore, export or import is running on this gateway; try again when it has finished")
		return nil, false
	}
}

// Handler serves /v1/namespace/backup, /v1/namespace/restore-key and
// /v1/namespace/restore.
type Handler struct {
	cfg  Config
	slot *Slot
}

// begin takes the gateway's one transfer slot.
func (h *Handler) begin(w http.ResponseWriter) (release func(), ok bool) {
	return h.slot.Begin(w)
}

// retryAfterSeconds is what a refused backup or restore is told to wait.
const retryAfterSeconds = "30"

// internalError logs err in full and gives the client only what failed.
func (h *Handler) internalError(w http.ResponseWriter, status int, what string, err error) {
	h.cfg.Logger.Error(what, zap.String("namespace", h.cfg.Namespace), zap.Error(err))
	httputil.WriteError(w, status, what+"; the gateway log has the detail")
}

// New checks cfg and returns a Handler.
func New(cfg Config) (*Handler, error) {
	if !httputil.ValidateNamespace(cfg.Namespace) {
		return nil, fmt.Errorf("namespace backup: %q is not a namespace this gateway can serve", cfg.Namespace)
	}
	if cfg.DB == nil || cfg.Registry == nil || cfg.Snapshots == nil || cfg.Pins == nil || cfg.Root == nil ||
		cfg.Caller == nil || cfg.Audit == nil || cfg.Logger == nil {
		return nil, fmt.Errorf("namespace backup: database, RQLite snapshots, IPFS, encryption root, caller, audit and logger are all required")
	}
	if cfg.ReplicationFactor <= 0 {
		return nil, fmt.Errorf("namespace backup: IPFS replication factor must be positive, got %d", cfg.ReplicationFactor)
	}
	slot := cfg.Slot
	if slot == nil {
		slot = NewSlot()
	}
	return &Handler{cfg: cfg, slot: slot}, nil
}

// authorize writes a refusal and returns false unless the caller's credential
// belongs to this gateway's namespace (and, when requireOwner, is its owner's).
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, requireOwner bool) bool {
	ns, owner := h.cfg.Caller(r)
	if ns != h.cfg.Namespace {
		httputil.WriteError(w, http.StatusForbidden,
			fmt.Sprintf("this gateway serves namespace %q; the credential is for %q", h.cfg.Namespace, ns))
		return false
	}
	if requireOwner && !owner {
		httputil.WriteError(w, http.StatusForbidden,
			"only the namespace's owner may back it up or restore it; an admin grant is not enough")
		return false
	}
	return true
}

// secretColumn is the trusted description of table.column, so SQL is only
// ever built from the list in pkg/secrets, never from request text.
func secretColumn(table, column string) (secrets.Column, bool) {
	for _, c := range secrets.NamespaceColumns() {
		if c.Table == table && c.Column == column {
			return c, true
		}
	}
	return secrets.Column{}, false
}
