package backup

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	"go.uber.org/zap"
)

// maxBackupRequestBytes bounds the JSON body of a backup request: one hex key.
const maxBackupRequestBytes = 1024

// BackupRequest is the body of POST /v1/namespace/backup.
type BackupRequest struct {
	// PublicKey is the owner's X25519 backup public key, 64 hex characters.
	PublicKey string `json:"public_key"`
}

// RestoreKeyResponse is the body of GET /v1/namespace/restore-key.
type RestoreKeyResponse struct {
	Namespace string `json:"namespace"`
	// PublicKey is this gateway's X25519 restore public key for the
	// namespace, 64 hex characters.
	PublicKey string `json:"public_key"`
}

// BackupHandler serves POST /v1/namespace/backup: the namespace's RQLite
// snapshot, pinned CIDs and decrypted secrets, sealed to the owner's key.
func (h *Handler) BackupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httputil.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorize(w, r, true) {
		return
	}
	var req BackupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBackupRequestBytes)).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body: expected JSON {public_key}")
		return
	}
	pub, err := parseKey(req.PublicKey)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	release, ok := h.begin(w)
	if !ok {
		return
	}
	defer release()
	sealed, payload, err := h.sealBackup(r.Context(), pub)
	if errors.Is(err, nsbackup.ErrTooLarge) {
		httputil.WriteError(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}
	if err != nil {
		h.internalError(w, http.StatusInternalServerError, "namespace backup failed", err)
		return
	}
	h.cfg.Audit.RecordFromRequest(r.Context(), r, auth.AuditEvent{
		Namespace: h.cfg.Namespace,
		Actor:     auth.ActorFromRequest(r),
		Action:    auth.AuditNamespaceBackedUp,
		Resource:  h.cfg.Namespace,
		Result:    auth.AuditSuccess,
		Metadata:  countsMetadata(len(payload.Pins), len(payload.Secrets), len(payload.RQLite)),
	})
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(sealed)))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(sealed); err != nil {
		h.cfg.Logger.Warn("namespace backup: writing the response failed", zap.Error(err))
	}
}

// sealBackup gathers the namespace and seals it to pub.
func (h *Handler) sealBackup(ctx context.Context, pub *[32]byte) ([]byte, nsbackup.Payload, error) {
	payload, err := h.gather(ctx)
	if err != nil {
		return nil, payload, err
	}
	plain, err := payload.Marshal()
	if err != nil {
		return nil, payload, fmt.Errorf("encode the backup: %w", err)
	}
	sealed, err := nsbackup.Seal(pub, plain)
	if err != nil {
		return nil, payload, err
	}
	return sealed, payload, nil
}

// RestoreKeyHandler serves GET /v1/namespace/restore-key.
func (h *Handler) RestoreKeyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httputil.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorize(w, r, false) {
		return
	}
	pub, _, err := nsbackup.RestoreKey(h.cfg.Root(), h.cfg.Namespace)
	if err != nil {
		h.internalError(w, http.StatusInternalServerError, "derive the restore key", err)
		return
	}
	httputil.WriteJSON(w, http.StatusOK, RestoreKeyResponse{Namespace: h.cfg.Namespace, PublicKey: hex.EncodeToString(pub[:])})
}

func (h *Handler) gather(ctx context.Context) (nsbackup.Payload, error) {
	db, err := h.cfg.Snapshots.Backup(ctx)
	if err != nil {
		return nsbackup.Payload{}, fmt.Errorf("snapshot RQLite: %w", err)
	}
	// Secrets are read after the snapshot, so every row in it has its secret;
	// one inserted in between is carried too and matches no row on restore.
	pins, err := h.pinnedCIDs(ctx)
	if err != nil {
		return nsbackup.Payload{}, err
	}
	stored, err := h.storedBytes(ctx)
	if err != nil {
		return nsbackup.Payload{}, err
	}
	secs, err := h.readSecrets(ctx)
	if err != nil {
		return nsbackup.Payload{}, err
	}
	return nsbackup.Payload{Namespace: h.cfg.Namespace, Pins: pins, StoredBytes: stored, Secrets: secs, RQLite: db}, nil
}

// pinnedCIDs is every CID the namespace holds pinned: stored objects and the
// content and build of each deployment. Sorted and without duplicates.
func (h *Handler) pinnedCIDs(ctx context.Context) ([]string, error) {
	var stored, deployed []map[string]any
	if err := h.cfg.DB.Query(ctx, &stored,
		"SELECT cid FROM ipfs_content_ownership WHERE namespace = ? AND is_pinned = 1", h.cfg.Namespace); err != nil {
		return nil, fmt.Errorf("list the namespace's pinned objects: %w", err)
	}
	if err := h.cfg.Registry.Query(ctx, &deployed,
		"SELECT content_cid, build_cid FROM deployments WHERE namespace = ?", h.cfg.Namespace); err != nil {
		return nil, fmt.Errorf("list the namespace's deployment CIDs: %w", err)
	}
	seen := map[string]struct{}{}
	for _, row := range stored {
		seen[stringify(row["cid"])] = struct{}{}
	}
	for _, row := range deployed {
		seen[stringify(row["content_cid"])] = struct{}{}
		seen[stringify(row["build_cid"])] = struct{}{}
	}
	delete(seen, "")
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}

// readSecrets decrypts every stored secret with this cluster's encryption
// root. Unprefixed leftover plaintext (deployment env, TURN) is carried as it
// is, as the re-encrypt walker treats it. A value that does not decrypt fails
// the backup: a backup that silently lacks a secret is how one is lost.
func (h *Handler) readSecrets(ctx context.Context) ([]nsbackup.Secret, error) {
	root := h.cfg.Root()
	var out []nsbackup.Secret
	for _, col := range secrets.NamespaceColumns() {
		ks, err := root.Keyset(col.Purpose)
		if err != nil {
			return nil, fmt.Errorf("derive the %s key: %w", col.Purpose, err)
		}
		var rows []map[string]any
		query := fmt.Sprintf("SELECT %s, %s FROM %s", strings.Join(col.IDCols, ", "), col.Column, col.Table)
		if err := h.cfg.DB.Query(ctx, &rows, query); err != nil {
			return nil, fmt.Errorf("read %s.%s: %w", col.Table, col.Column, err)
		}
		for _, row := range rows {
			raw := stringify(row[col.Column])
			if strings.TrimSpace(raw) == "" {
				continue
			}
			ids := make([]string, 0, len(col.IDCols))
			for _, c := range col.IDCols {
				ids = append(ids, stringify(row[c]))
			}
			plain := raw
			if secrets.IsEncrypted(raw) {
				if plain, err = ks.Decrypt(raw); err != nil {
					return nil, fmt.Errorf("decrypt %s.%s %v: %w", col.Table, col.Column, ids, err)
				}
			}
			out = append(out, nsbackup.Secret{Table: col.Table, Column: col.Column, IDs: ids, Value: plain})
		}
	}
	return out, nil
}

// countsMetadata is what an audit event records about a backup or restore:
// how much it moved, never what.
func countsMetadata(pins, secretCount, rqliteBytes int) map[string]string {
	return map[string]string{
		"pins":         strconv.Itoa(pins),
		"secrets":      strconv.Itoa(secretCount),
		"rqlite_bytes": strconv.Itoa(rqliteBytes),
	}
}

func parseKey(s string) (*[32]byte, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("public_key must be a 32-byte X25519 key, 64 hex characters")
	}
	var k [32]byte
	copy(k[:], raw)
	return &k, nil
}

// stringify renders a column value as the string RQLite compares it with.
// Integers arrive as float64 from JSON and are written without an exponent.
func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}
