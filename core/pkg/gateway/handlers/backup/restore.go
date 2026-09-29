package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	"go.uber.org/zap"
)

// MaxRestoreBytes bounds a restore request. The request is held in memory
// whole, because the backup it came from is one nacl box.
const MaxRestoreBytes = 1 << 30

// RestoreResponse is the body of a successful POST /v1/namespace/restore.
type RestoreResponse struct {
	Namespace   string `json:"namespace"`
	RQLiteBytes int    `json:"rqlite_bytes"`
	Pins        int    `json:"pins"`
	Secrets     int    `json:"secrets"`
	// SecretsWithoutRow counts secrets whose row is not in the snapshot: the
	// row was created on the source after the snapshot was taken.
	SecretsWithoutRow int `json:"secrets_without_row"`
}

// RestoreHandler serves POST /v1/namespace/restore.
//
// Everything that can be refused is checked before anything is written: the
// frame, the namespace, and every secret opening under this gateway's restore
// key. Then the snapshot is loaded, the secrets are written under this
// cluster's encryption root, and the CIDs are pinned. A failure after the load
// leaves the loaded database; running the same restore again is safe.
func (h *Handler) RestoreHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httputil.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorize(w, r, true) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRestoreBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httputil.WriteError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("restore request is over %d bytes", MaxRestoreBytes))
			return
		}
		httputil.WriteError(w, http.StatusBadRequest, fmt.Sprintf("read the restore request: %v", err))
		return
	}
	req, err := nsbackup.UnmarshalRestoreRequest(body)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Namespace != h.cfg.Namespace {
		httputil.WriteError(w, http.StatusConflict,
			fmt.Sprintf("the backup is of namespace %q; this gateway serves %q", req.Namespace, h.cfg.Namespace))
		return
	}
	ops, err := h.resealHere(req)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, nsbackup.ErrNotForKey) {
			status = http.StatusBadRequest
			err = fmt.Errorf("%w; the secrets were not sealed to this gateway's current restore key, fetch it again with 'orama namespace restore-key'", err)
		}
		httputil.WriteError(w, status, err.Error())
		return
	}
	resp, err := h.apply(r.Context(), req, ops)
	if err != nil {
		h.cfg.Logger.Error("namespace restore failed after the snapshot was loaded",
			zap.String("namespace", h.cfg.Namespace), zap.Error(err))
		httputil.WriteError(w, http.StatusBadGateway,
			fmt.Sprintf("%v; the database was already replaced, running the same restore again is safe", err))
		return
	}
	h.cfg.Audit.RecordFromRequest(r.Context(), r, auth.AuditEvent{
		Namespace: h.cfg.Namespace,
		Actor:     auth.ActorFromRequest(r),
		Action:    auth.AuditNamespaceRestored,
		Resource:  h.cfg.Namespace,
		Result:    auth.AuditSuccess,
		Metadata:  countsMetadata(resp.Pins, resp.Secrets, resp.RQLiteBytes),
	})
	httputil.WriteJSON(w, http.StatusOK, resp)
}

// resealHere opens every secret with this gateway's restore key and encrypts
// it under this cluster's encryption root, returning the UPDATEs that write
// them. It writes nothing.
func (h *Handler) resealHere(req nsbackup.RestoreRequest) ([]rqlite.BatchOp, error) {
	root := h.cfg.Root()
	_, priv, err := nsbackup.RestoreKey(root)
	if err != nil {
		return nil, err
	}
	plain, err := req.OpenSecrets(priv)
	if err != nil {
		return nil, err
	}
	keysets := map[string]secrets.Keyset{}
	ops := make([]rqlite.BatchOp, 0, len(plain))
	for _, s := range plain {
		col, ok := secretColumn(s.Table, s.Column)
		if !ok {
			return nil, fmt.Errorf("%s.%s is not a namespace secret column", s.Table, s.Column)
		}
		ks, ok := keysets[col.Purpose]
		if !ok {
			if ks, err = root.Keyset(col.Purpose); err != nil {
				return nil, fmt.Errorf("derive the %s key: %w", col.Purpose, err)
			}
			keysets[col.Purpose] = ks
		}
		sealed, err := ks.Encrypt(s.Value)
		if err != nil {
			return nil, fmt.Errorf("encrypt %s.%s %v: %w", s.Table, s.Column, s.IDs, err)
		}
		ops = append(ops, updateOp(col, sealed, s.IDs))
	}
	return ops, nil
}

func updateOp(col secrets.Column, value string, ids []string) rqlite.BatchOp {
	where := make([]string, 0, len(col.IDCols))
	args := make([]any, 0, len(ids)+1)
	args = append(args, value)
	for i, c := range col.IDCols {
		where = append(where, c+" = ?")
		args = append(args, ids[i])
	}
	return rqlite.BatchOp{
		Kind: rqlite.BatchOpExec,
		SQL:  fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s", col.Table, col.Column, strings.Join(where, " AND ")),
		Args: args,
	}
}

func (h *Handler) apply(ctx context.Context, req nsbackup.RestoreRequest, ops []rqlite.BatchOp) (RestoreResponse, error) {
	resp := RestoreResponse{Namespace: req.Namespace, RQLiteBytes: len(req.RQLite), Pins: len(req.Pins), Secrets: len(ops)}
	if err := h.cfg.Snapshots.Load(ctx, req.RQLite); err != nil {
		return resp, fmt.Errorf("load the RQLite snapshot: %w", err)
	}
	missing, err := h.writeSecrets(ctx, ops)
	if err != nil {
		return resp, err
	}
	resp.SecretsWithoutRow = missing
	for _, c := range req.Pins {
		if _, err := h.cfg.Pins.Pin(ctx, c, "", h.cfg.ReplicationFactor); err != nil {
			return resp, fmt.Errorf("pin %s: %w", c, err)
		}
	}
	return resp, nil
}

// writeSecrets runs ops in atomic batches of rqlite.MaxBatchOps and returns
// how many matched no row.
func (h *Handler) writeSecrets(ctx context.Context, ops []rqlite.BatchOp) (int, error) {
	missing := 0
	for start := 0; start < len(ops); start += rqlite.MaxBatchOps {
		chunk := ops[start:min(start+rqlite.MaxBatchOps, len(ops))]
		res, err := h.cfg.DB.Batch(ctx, chunk)
		if err != nil {
			return 0, fmt.Errorf("write restored secrets: %w", err)
		}
		if !res.Committed {
			return 0, fmt.Errorf("write restored secrets: batch rolled back: %s", batchFailure(res))
		}
		for _, r := range res.Results {
			if r.RowsAffected == 0 {
				missing++
			}
		}
	}
	return missing, nil
}

func batchFailure(res *rqlite.BatchResult) string {
	if res.Error != "" {
		return res.Error
	}
	if res.FailedIndex >= 0 && res.FailedIndex < len(res.Results) {
		return res.Results[res.FailedIndex].Error
	}
	return "no reason given"
}
