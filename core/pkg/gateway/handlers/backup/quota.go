package backup

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// The storage quota a restore must respect is the one /v1/storage/pin and
// upload enforce: namespace_quotas.max_storage_bytes (opt-in; no row or a
// non-positive value is unlimited) against SUM(ipfs_content_ownership.size_bytes)
// times the replication factor. Both tables live in the namespace RQLite, which
// a restore replaces, so the destination's budget is read before the load and
// written back after it: a backup cannot bring its own quota.

// ErrOverQuota means a restore's content would exceed the namespace's storage
// budget on this cluster.
var ErrOverQuota = errors.New("restore is over the namespace storage quota")

const (
	storageUseQuery = "SELECT COALESCE(SUM(size_bytes), 0) AS used FROM ipfs_content_ownership WHERE namespace = ?"
	budgetQuery     = "SELECT max_storage_bytes FROM namespace_quotas WHERE namespace = ?"
)

// storageBudget is the destination's quota row as it was before the restore.
type storageBudget struct {
	hasRow bool
	bytes  int64
}

// storedBytes is the namespace's logical storage use, as the storage quota
// counts it.
func (h *Handler) storedBytes(ctx context.Context) (int64, error) {
	var rows []map[string]any
	if err := h.cfg.DB.Query(ctx, &rows, storageUseQuery, h.cfg.Namespace); err != nil {
		return 0, fmt.Errorf("read the namespace's storage use: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return int64Of(rows[0]["used"])
}

func (h *Handler) readBudget(ctx context.Context) (storageBudget, error) {
	var rows []map[string]any
	if err := h.cfg.DB.Query(ctx, &rows, budgetQuery, h.cfg.Namespace); err != nil {
		return storageBudget{}, fmt.Errorf("read the namespace's storage quota: %w", err)
	}
	if len(rows) == 0 {
		return storageBudget{}, nil
	}
	n, err := int64Of(rows[0]["max_storage_bytes"])
	if err != nil {
		return storageBudget{}, fmt.Errorf("read the namespace's storage quota: %w", err)
	}
	return storageBudget{hasRow: true, bytes: n}, nil
}

// checkQuota refuses logical bytes that, replicated, exceed the budget.
func (h *Handler) checkQuota(b storageBudget, logical int64) error {
	if b.bytes <= 0 {
		return nil
	}
	projected := logical * int64(h.cfg.ReplicationFactor)
	if projected > b.bytes {
		return fmt.Errorf("%w: %d bytes (x RF %d) exceeds the budget of %d bytes",
			ErrOverQuota, logical, h.cfg.ReplicationFactor, b.bytes)
	}
	return nil
}

// restoreBudgetOp puts the destination's quota row back after the load.
func (h *Handler) restoreBudgetOp(b storageBudget) rqlite.BatchOp {
	if !b.hasRow {
		return rqlite.BatchOp{Kind: rqlite.BatchOpExec,
			SQL: "DELETE FROM namespace_quotas WHERE namespace = ?", Args: []any{h.cfg.Namespace}}
	}
	return rqlite.BatchOp{Kind: rqlite.BatchOpExec,
		SQL: "INSERT INTO namespace_quotas (namespace, max_storage_bytes) VALUES (?, ?) " +
			"ON CONFLICT(namespace) DO UPDATE SET max_storage_bytes = excluded.max_storage_bytes",
		Args: []any{h.cfg.Namespace, b.bytes}}
}

// int64Of reads an integer column, which arrives as float64 from JSON.
func int64Of(v any) (int64, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case int64:
		return n, nil
	case float64:
		return int64(n), nil
	default:
		return 0, fmt.Errorf("integer column holds %T", v)
	}
}
