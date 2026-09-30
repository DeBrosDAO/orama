package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// The cluster-wide reference count behind "may this unpin remove the shared
// cluster pin". IPFS-Cluster keeps one pin per CID however many namespaces
// hold it, and a namespace's own RQLite only lists that namespace's rows, so
// the count lives in the cluster registry (ipfs_cid_refs, migration 064) and
// every namespace gateway reads it through globalDB.

const (
	refKindStorage    = "storage"
	refKindDeployment = "deployment"

	// refQueryTimeout bounds one registry round trip on the request path.
	refQueryTimeout = 5 * time.Second

	// refSyncInterval is how often a settled gateway re-derives its own
	// deployment references; refSyncRetryInterval is the cadence until the
	// first sync has succeeded.
	refSyncInterval      = time.Minute
	refSyncRetryInterval = 5 * time.Second

	// refInsertChunk caps rows per multi-row INSERT so a large namespace's
	// backfill stays well inside SQLite's bound-variable limit (3 per row).
	refInsertChunk = 100
)

// registerCIDRef records that namespace holds cid. It runs BEFORE the cluster
// pin is requested, so a concurrent unpin by another namespace already sees
// this reference when it counts.
func (h *Handlers) registerCIDRef(ctx context.Context, cid, namespace string) error {
	if h.globalDB == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, refQueryTimeout)
	defer cancel()
	if _, err := h.globalDB.Exec(ctx,
		`INSERT OR IGNORE INTO ipfs_cid_refs (cid, namespace, kind) VALUES (?, ?, ?)`,
		cid, namespace, refKindStorage); err != nil {
		return fmt.Errorf("failed to record the reference of namespace %s to %s in the cluster reference index: %w", namespace, cid, err)
	}
	return nil
}

// releaseCIDRef drops namespace's storage reference to cid and returns how many
// references to cid remain anywhere in the cluster, this namespace's
// deployments included.
//
// The delete comes first and the count second, and that order is the
// atomicity: the registry is linearizable, so of any set of concurrent
// releases the one whose delete lands last counts zero, and no release can
// count zero while another reference is still recorded. A check-then-delete
// would let two namespaces each see the other's reference and both keep a pin
// nobody owns.
func (h *Handlers) releaseCIDRef(ctx context.Context, cid, namespace string) (int, error) {
	if h.globalDB == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, refQueryTimeout)
	defer cancel()
	if _, err := h.globalDB.Exec(ctx,
		`DELETE FROM ipfs_cid_refs WHERE cid = ? AND namespace = ? AND kind = ?`,
		cid, namespace, refKindStorage); err != nil {
		return 0, fmt.Errorf("failed to release the reference of namespace %s to %s: %w", namespace, cid, err)
	}
	return h.countCIDRefs(ctx, cid)
}

// countCIDRefs is the number of references to cid across every namespace.
func (h *Handlers) countCIDRefs(ctx context.Context, cid string) (int, error) {
	var rows []map[string]interface{}
	if err := h.globalDB.Query(ctx, &rows,
		`SELECT COUNT(*) AS count FROM ipfs_cid_refs WHERE cid = ?`, cid); err != nil {
		return 0, fmt.Errorf("failed to count the references to %s in the cluster reference index: %w", cid, err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return countFromRow(rows[0]["count"]), nil
}

// StartCIDRefSync brings this namespace's references into the cluster index and
// keeps its deployment references current. Until the first sync succeeds the
// index may lack references written before it existed (content pinned by an
// older gateway), so UnpinHandler refuses to decide "last reference": a wrong
// "last" deletes another tenant's data, a refusal is simply retried.
func (h *Handlers) StartCIDRefSync(ctx context.Context, namespace string) {
	h.refSyncPending.Store(true)
	go func() {
		var wait time.Duration
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if err := h.syncCIDRefs(ctx, namespace); err != nil {
				h.logger.ComponentError(logging.ComponentGeneral, "cid reference sync failed; unpin stays disabled until it succeeds",
					zap.String("namespace", namespace), zap.Error(err))
				wait = refSyncRetryInterval
				continue
			}
			h.refSyncPending.Store(false)
			wait = refSyncInterval
		}
	}()
}

// syncCIDRefs makes the index agree with this namespace's own database.
//
// Storage references are added, never removed here: an unpin removes its own
// reference synchronously, and a periodic remove could race an in-flight pin.
// Deployment references are replaced wholesale, because deployments are
// created, updated and deleted in many handlers and the database row is what
// says whether one still serves the CID.
func (h *Handlers) syncCIDRefs(ctx context.Context, namespace string) error {
	if h.db == nil || h.globalDB == nil {
		return nil
	}
	var pinned []map[string]interface{}
	if err := h.db.Query(ctx, &pinned,
		`SELECT cid FROM ipfs_content_ownership WHERE is_pinned = 1 AND namespace = ?`, namespace); err != nil {
		return fmt.Errorf("failed to read the pinned content of namespace %s: %w", namespace, err)
	}
	if err := h.insertRefs(ctx, cidsOf(pinned), namespace, refKindStorage); err != nil {
		return err
	}

	var deployed []map[string]interface{}
	if err := h.db.Query(ctx, &deployed,
		`SELECT content_cid AS cid FROM deployments WHERE namespace = ? AND content_cid IS NOT NULL AND content_cid != ''
		 UNION
		 SELECT build_cid AS cid FROM deployments WHERE namespace = ? AND build_cid IS NOT NULL AND build_cid != ''`,
		namespace, namespace); err != nil {
		return fmt.Errorf("failed to read the deployment content of namespace %s: %w", namespace, err)
	}
	want := cidsOf(deployed)
	if err := h.insertRefs(ctx, want, namespace, refKindDeployment); err != nil {
		return err
	}
	return h.pruneDeploymentRefs(ctx, namespace, want)
}

func (h *Handlers) pruneDeploymentRefs(ctx context.Context, namespace string, want []string) error {
	var held []map[string]interface{}
	if err := h.globalDB.Query(ctx, &held,
		`SELECT cid FROM ipfs_cid_refs WHERE namespace = ? AND kind = ?`, namespace, refKindDeployment); err != nil {
		return fmt.Errorf("failed to read the deployment references of namespace %s: %w", namespace, err)
	}
	wanted := make(map[string]struct{}, len(want))
	for _, c := range want {
		wanted[c] = struct{}{}
	}
	for _, cid := range cidsOf(held) {
		if _, ok := wanted[cid]; ok {
			continue
		}
		if _, err := h.globalDB.Exec(ctx,
			`DELETE FROM ipfs_cid_refs WHERE cid = ? AND namespace = ? AND kind = ?`,
			cid, namespace, refKindDeployment); err != nil {
			return fmt.Errorf("failed to drop the stale deployment reference of namespace %s to %s: %w", namespace, cid, err)
		}
	}
	return nil
}

func (h *Handlers) insertRefs(ctx context.Context, cids []string, namespace, kind string) error {
	for start := 0; start < len(cids); start += refInsertChunk {
		end := min(start+refInsertChunk, len(cids))
		chunk := cids[start:end]
		args := make([]any, 0, len(chunk)*3)
		for _, c := range chunk {
			args = append(args, c, namespace, kind)
		}
		q := `INSERT OR IGNORE INTO ipfs_cid_refs (cid, namespace, kind) VALUES ` +
			strings.TrimSuffix(strings.Repeat("(?, ?, ?),", len(chunk)), ",")
		if _, err := h.globalDB.Exec(ctx, q, args...); err != nil {
			return fmt.Errorf("failed to record %d %s references of namespace %s: %w", len(chunk), kind, namespace, err)
		}
	}
	return nil
}

// cidsOf extracts the "cid" column of query rows, skipping empties.
func cidsOf(rows []map[string]interface{}) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if c, ok := row["cid"].(string); ok && c != "" {
			out = append(out, c)
		}
	}
	return out
}
