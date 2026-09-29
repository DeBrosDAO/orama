package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	"golang.org/x/sync/errgroup"
)

const (
	// MaxRestoreBytes bounds a restore request: the largest frame a reader
	// accepts. The request is held in memory whole.
	MaxRestoreBytes = nsbackup.MaxFrameBytes
	// pinConcurrency is how many pins a restore has in flight at once.
	pinConcurrency = 8
	// pinPhaseTimeout bounds all of a restore's pins together.
	pinPhaseTimeout = 10 * time.Minute
)

// errBatchUnavailable means this gateway's RQLite client has no native
// connection, so it cannot write the restored secrets atomically.
var errBatchUnavailable = errors.New("this gateway's RQLite client cannot run atomic batches")

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

// restorePlan is everything decided before the first write.
type restorePlan struct {
	ops    []rqlite.BatchOp
	budget storageBudget
}

// RestoreHandler serves POST /v1/namespace/restore.
//
// Everything that can be refused is checked before anything is written: the
// frame, the namespace, that RQLite can batch, every secret opening under this
// namespace's restore key, and the storage quota. Then the snapshot is loaded,
// the destination's quota and the secrets are written, and the CIDs pinned. A
// failure after the load leaves the loaded database; the same restore again
// is safe.
func (h *Handler) RestoreHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httputil.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.authorize(w, r, true) {
		return
	}
	release, ok := h.begin(w)
	if !ok {
		return
	}
	defer release()
	req, ok := h.readRestore(w, r)
	if !ok {
		return
	}
	plan, ok := h.prepare(r.Context(), w, req)
	if !ok {
		return
	}
	resp, err := h.apply(r.Context(), req, plan)
	if errors.Is(err, ErrOverQuota) {
		httputil.WriteError(w, http.StatusRequestEntityTooLarge,
			err.Error()+"; the database was replaced and no CID was pinned")
		return
	}
	if err != nil {
		h.internalError(w, http.StatusBadGateway,
			"restore failed after the database was replaced; running the same restore again is safe", err)
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

// readRestore reads and validates the body, writing the refusal if it fails.
func (h *Handler) readRestore(w http.ResponseWriter, r *http.Request) (nsbackup.RestoreRequest, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(MaxRestoreBytes)))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httputil.WriteError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("restore request is over %d bytes", MaxRestoreBytes))
			return nsbackup.RestoreRequest{}, false
		}
		httputil.WriteError(w, http.StatusBadRequest, "the restore request could not be read")
		return nsbackup.RestoreRequest{}, false
	}
	req, err := nsbackup.UnmarshalRestoreRequest(body)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return nsbackup.RestoreRequest{}, false
	}
	if req.Namespace != h.cfg.Namespace {
		httputil.WriteError(w, http.StatusConflict,
			fmt.Sprintf("the backup is of namespace %q; this gateway serves %q", req.Namespace, h.cfg.Namespace))
		return nsbackup.RestoreRequest{}, false
	}
	return req, true
}

// prepare decides everything a restore will write, and writes nothing. On a
// refusal it writes the response and returns false.
func (h *Handler) prepare(ctx context.Context, w http.ResponseWriter, req nsbackup.RestoreRequest) (restorePlan, bool) {
	plan, err := h.plan(ctx, req)
	switch {
	case err == nil:
		return plan, true
	case errors.Is(err, errBatchUnavailable):
		h.internalError(w, http.StatusServiceUnavailable, errBatchUnavailable.Error()+"; nothing was written", err)
	case errors.Is(err, nsbackup.ErrNotForKey):
		httputil.WriteError(w, http.StatusBadRequest, err.Error()+
			"; the secrets were not sealed to this namespace's current restore key, fetch it again with 'orama namespace restore-key'")
	case errors.Is(err, nsbackup.ErrSecretMismatch), errors.Is(err, nsbackup.ErrCorrupt):
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrOverQuota):
		httputil.WriteError(w, http.StatusRequestEntityTooLarge, err.Error()+"; nothing was written")
	default:
		h.internalError(w, http.StatusInternalServerError, "prepare the restore", err)
	}
	return restorePlan{}, false
}

func (h *Handler) plan(ctx context.Context, req nsbackup.RestoreRequest) (restorePlan, error) {
	// The stdlib-only client has no Batch. Finding that out after the load
	// would leave a database whose secrets nobody here can read.
	if _, err := h.cfg.DB.Batch(ctx, []rqlite.BatchOp{{Kind: rqlite.BatchOpQuery, SQL: "SELECT 1"}}); err != nil {
		return restorePlan{}, fmt.Errorf("%w: %v", errBatchUnavailable, err)
	}
	ops, err := h.resealHere(req)
	if err != nil {
		return restorePlan{}, err
	}
	budget, err := h.readBudget(ctx)
	if err != nil {
		return restorePlan{}, err
	}
	if err := h.checkQuota(budget, req.StoredBytes); err != nil {
		return restorePlan{}, err
	}
	return restorePlan{ops: ops, budget: budget}, nil
}

// resealHere opens every secret with this namespace's restore key and
// encrypts it under this cluster's encryption root, returning the UPDATEs
// that write them. It writes nothing.
func (h *Handler) resealHere(req nsbackup.RestoreRequest) ([]rqlite.BatchOp, error) {
	root := h.cfg.Root()
	_, priv, err := nsbackup.RestoreKey(root, h.cfg.Namespace)
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
			return nil, fmt.Errorf("%w: %s.%s is not a namespace secret column", nsbackup.ErrCorrupt, s.Table, s.Column)
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

func (h *Handler) apply(ctx context.Context, req nsbackup.RestoreRequest, plan restorePlan) (RestoreResponse, error) {
	resp := RestoreResponse{Namespace: req.Namespace, RQLiteBytes: len(req.RQLite), Pins: len(req.Pins), Secrets: len(plan.ops)}
	if err := h.cfg.Snapshots.Load(ctx, req.RQLite); err != nil {
		return resp, fmt.Errorf("load the RQLite snapshot: %w", err)
	}
	if _, err := h.writeBatches(ctx, []rqlite.BatchOp{h.restoreBudgetOp(plan.budget)}); err != nil {
		return resp, fmt.Errorf("put back this cluster's storage quota: %w", err)
	}
	missing, err := h.writeBatches(ctx, plan.ops)
	if err != nil {
		return resp, fmt.Errorf("write restored secrets: %w", err)
	}
	resp.SecretsWithoutRow = missing
	// The restored table is what the storage quota counts from now on, and
	// may not say what the request header did.
	used, err := h.storedBytes(ctx)
	if err != nil {
		return resp, err
	}
	if err := h.checkQuota(plan.budget, used); err != nil {
		return resp, err
	}
	return resp, h.pinAll(ctx, req.Pins)
}

// pinAll pins cids with at most pinConcurrency in flight, all within
// pinPhaseTimeout. The first failure cancels the rest.
func (h *Handler) pinAll(ctx context.Context, cids []string) error {
	ctx, cancel := context.WithTimeout(ctx, pinPhaseTimeout)
	defer cancel()
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(pinConcurrency)
	for _, c := range cids {
		g.Go(func() error {
			if _, err := h.cfg.Pins.Pin(ctx, c, "", h.cfg.ReplicationFactor); err != nil {
				return fmt.Errorf("pin %s: %w", c, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// writeBatches runs ops in atomic batches of rqlite.MaxBatchOps and returns
// how many matched no row.
func (h *Handler) writeBatches(ctx context.Context, ops []rqlite.BatchOp) (int, error) {
	missing := 0
	for start := 0; start < len(ops); start += rqlite.MaxBatchOps {
		chunk := ops[start:min(start+rqlite.MaxBatchOps, len(ops))]
		res, err := h.cfg.DB.Batch(ctx, chunk)
		if err != nil {
			return 0, err
		}
		if !res.Committed {
			return 0, fmt.Errorf("batch rolled back: %s", batchFailure(res))
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
