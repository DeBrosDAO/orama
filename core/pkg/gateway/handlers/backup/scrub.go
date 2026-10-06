package backup

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/sqlguard"
)

// What a loaded database image may carry that its writer could have crafted.
//
// image.go refuses an image that carries any of this before RQLite loads it, so
// that no gateway serves writes against it; what follows is the backstop, run
// detached after every load, which finds nothing to do on an image that passed.
//
// A restore or an import replaces every row of the namespace's database, and
// the SQL guard, which filters statements, never sees them. Most of what an
// image can hold is inert on a namespace gateway: keys, grants and sessions are
// read from the cluster registry, so the stale copies an image carries are
// read by nothing. Three things in it are not:
//
//   - a trigger, or a view over a platform table. It runs, or is queried, by a
//     statement that does not name the table, which is the one thing the guard
//     cannot see (pkg/sqlguard, package comment). Tenant SQL cannot create a
//     trigger and the platform's migrations make none, so every trigger in an
//     image is crafted.
//   - a plaintext api_keys row, the pre-hashing shape the gateway removes from
//     a namespace database at start.
//   - an ipfs_content_ownership row. /v1/storage/get serves a CID to a
//     namespace holding a row, and the storage quota is summed from the table.
//
// scrubLoadedImage drops the first two outright and removes the foreign claims
// of the third (rows of other namespaces, and rows naming a CID the registry
// records only against other namespaces). What it cannot do is check size_bytes: no gateway call reports a
// pinned object's size, so the rows an image carries for CIDs nobody else holds
// are counted as the image says (docs/SECURITY.md).

// platformSQLQuery lists the triggers and views of the database.
const platformSQLQuery = "SELECT type, name, sql FROM sqlite_master WHERE type IN ('trigger', 'view')"

// scrubChunk is how many CIDs one registry query names.
const scrubChunk = 500

// loadTimeout bounds a load and the scrub after it, which run detached from the
// request (loadContext).
const loadTimeout = 15 * time.Minute

// loadContext is the context a load and its scrub run in. A client that goes
// away after its image was handed to RQLite does not stop RQLite applying it,
// so it must not stop the scrub of what was applied either.
func loadContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), loadTimeout)
}

// LoadContext is loadContext for the gateway's import route.
func LoadContext(r *http.Request) (context.Context, context.CancelFunc) { return loadContext(r) }

// finishLoad is everything done to the namespace's database right after an
// image replaced it, or may have: the image is scrubbed, and then the
// destination's storage quota goes back. The scrub comes first so no quota is
// written while a trigger of the image could still fire on it.
func (h *Handler) finishLoad(ctx context.Context, budget storageBudget) error {
	if err := h.scrubLoadedImage(ctx); err != nil {
		return fmt.Errorf("scrub the loaded database: %w", err)
	}
	if _, err := h.writeBatches(ctx, []rqlite.BatchOp{h.restoreBudgetOp(budget)}); err != nil {
		return fmt.Errorf("put back this cluster's storage quota: %w", err)
	}
	return nil
}

// GuardLoad is for a whole-database load that does not come through restore:
// an RQLite import. It reads what the load would replace and must put back,
// and returns the function that does, and scrubs the image. Call GuardLoad
// before the load and finish after it succeeded; running finish again is safe.
func (h *Handler) GuardLoad(ctx context.Context) (finish func(context.Context) error, err error) {
	budget, err := h.readBudget(ctx)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error { return h.finishLoad(ctx, budget) }, nil
}

func (h *Handler) scrubLoadedImage(ctx context.Context) error {
	if err := h.dropPlatformSQL(ctx); err != nil {
		return err
	}
	if err := h.dropPlaintextKeys(ctx); err != nil {
		return err
	}
	return h.dropForeignOwnership(ctx)
}

// dropPlatformSQL drops every trigger (tenant SQL cannot create one, and the
// platform's migrations make none) and every view the SQL guard would refuse as a
// statement.
func (h *Handler) dropPlatformSQL(ctx context.Context) error {
	var rows []map[string]any
	if err := h.cfg.DB.Query(ctx, &rows, platformSQLQuery); err != nil {
		return fmt.Errorf("list the triggers and views: %w", err)
	}
	var ops []rqlite.BatchOp
	for _, row := range rows {
		kind, _ := row["type"].(string)
		name, _ := row["name"].(string)
		sql, _ := row["sql"].(string)
		if name == "" || sqlguard.Check(sql) == nil {
			continue
		}
		ops = append(ops, rqlite.BatchOp{Kind: rqlite.BatchOpExec,
			SQL: fmt.Sprintf("DROP %s IF EXISTS %s", strings.ToUpper(kind), quoteIdent(name))})
	}
	if _, err := h.writeBatches(ctx, ops); err != nil {
		return fmt.Errorf("drop the triggers and views over platform tables: %w", err)
	}
	return nil
}

// quoteIdent quotes a SQLite identifier.
func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// dropPlaintextKeys removes the legacy plaintext rows from api_keys, the table
// an image from before the registry carries. A database without it holds none.
func (h *Handler) dropPlaintextKeys(ctx context.Context) error {
	_, err := h.writeBatches(ctx, []rqlite.BatchOp{{Kind: rqlite.BatchOpExec,
		SQL: "DELETE FROM api_keys WHERE key LIKE 'ak_%' OR key LIKE 'orama_%'"}})
	if err != nil && !strings.Contains(err.Error(), "no such table") {
		return fmt.Errorf("remove plaintext API keys: %w", err)
	}
	return nil
}

// dropForeignOwnership deletes the image's ownership rows that name another
// namespace, and those that name a CID the registry records only against
// other namespaces: a claim to content this namespace never stored here.
func (h *Handler) dropForeignOwnership(ctx context.Context) error {
	ns := h.cfg.Namespace
	ops := []rqlite.BatchOp{{Kind: rqlite.BatchOpExec,
		SQL: "DELETE FROM ipfs_content_ownership WHERE namespace <> ?", Args: []any{ns}}}
	var rows []map[string]any
	if err := h.cfg.DB.Query(ctx, &rows, "SELECT cid FROM ipfs_content_ownership WHERE namespace = ?", ns); err != nil {
		return fmt.Errorf("list the namespace's ownership rows: %w", err)
	}
	cids := make([]string, 0, len(rows))
	for _, r := range rows {
		if c, ok := r["cid"].(string); ok && c != "" {
			cids = append(cids, c)
		}
	}
	foreign, err := h.heldElsewhereOnly(ctx, cids)
	if err != nil {
		return err
	}
	for _, c := range foreign {
		ops = append(ops, rqlite.BatchOp{Kind: rqlite.BatchOpExec,
			SQL: "DELETE FROM ipfs_content_ownership WHERE cid = ? AND namespace = ?", Args: []any{c, ns}})
	}
	if _, err := h.writeBatches(ctx, ops); err != nil {
		return fmt.Errorf("remove forged ownership rows: %w", err)
	}
	return nil
}

// heldElsewhereOnly returns the cids the registry records against another
// namespace and not against this one.
func (h *Handler) heldElsewhereOnly(ctx context.Context, cids []string) ([]string, error) {
	var out []string
	for start := 0; start < len(cids); start += scrubChunk {
		chunk := cids[start:min(start+scrubChunk, len(cids))]
		marks := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk)+1)
		args = append(args, h.cfg.Namespace)
		for _, c := range chunk {
			args = append(args, c)
		}
		var rows []map[string]any
		q := "SELECT cid, MAX(namespace = ?) AS own FROM ipfs_cid_refs WHERE cid IN (" + marks + ") GROUP BY cid"
		if err := h.cfg.Registry.Query(ctx, &rows, q, args...); err != nil {
			return nil, fmt.Errorf("read who holds the CIDs of the loaded database: %w", err)
		}
		for _, r := range rows {
			c, _ := r["cid"].(string)
			own, err := int64Of(r["own"])
			if err != nil {
				return nil, fmt.Errorf("read who holds %s: %w", c, err)
			}
			if own == 0 && c != "" {
				out = append(out, c)
			}
		}
	}
	return out, nil
}
