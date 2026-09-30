package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// The cluster-wide reference count behind "may this unpin remove the shared
// cluster pin". IPFS-Cluster keeps one pin per CID however many namespaces
// hold it, and a namespace's own RQLite only lists that namespace's rows, so
// the count lives in the cluster registry (ipfs_cid_refs, migration 064) and
// every namespace gateway reads it through the registry handle.
//
// Only gateway handlers write these rows, at the moment they pin, deploy,
// unpin or delete. They are never derived from a tenant-writable table except
// once, at the upgrade that introduced the index (see StartCIDRefBackfill):
// a tenant with database access can write any row into its own database,
// including ones naming another tenant's CID, and a reference copied from
// there would let it hold that tenant's content pinned or read it.

const (
	// KindStorage is a reference made by pinning through /v1/storage.
	KindStorage = "storage"
	// KindDeployment is a reference made by a deployment serving the CID as its
	// content or build.
	KindDeployment = "deployment"

	// kindBackfilled marks a namespace whose existing content has been loaded
	// into the index. Its cid is empty, which no count ever asks about.
	kindBackfilled = "backfilled"

	// refQueryTimeout bounds one registry round trip on the request path.
	refQueryTimeout = 5 * time.Second

	// refBackfillRetryInterval is the cadence of backfill attempts until one
	// succeeds.
	refBackfillRetryInterval = 5 * time.Second

	// refInsertChunk caps rows per multi-row INSERT so a large namespace's
	// backfill stays well inside SQLite's bound-variable limit (3 per row).
	refInsertChunk = 100
)

// maxBackfillRefs bounds the one-time backfill. A namespace with more
// references than this is not loaded partially (a partial index would call
// live content unreferenced): the backfill fails and unpins stay disabled on
// that gateway until an operator deals with it. A variable only so a test can
// lower it.
var maxBackfillRefs = 1_000_000

var (
	// ErrRefIndexUnavailable is returned when there is no registry handle to
	// count in. A caller must not read it as "no references".
	ErrRefIndexUnavailable = errors.New("the cluster reference index is not available on this gateway")

	// ErrRefIndexNotReady is returned while this gateway's namespace has not yet
	// been loaded into the index, so "last reference" cannot be decided.
	ErrRefIndexNotReady = errors.New("the cluster reference index is still being built on this gateway")
)

// CIDRefs is the cluster-wide reference index over the cluster registry.
type CIDRefs struct {
	registry rqlite.Client
	pending  atomic.Bool
}

// NewCIDRefs returns the index over the registry's RQLite. A nil registry
// yields an index whose every operation fails with ErrRefIndexUnavailable.
func NewCIDRefs(registry rqlite.Client) *CIDRefs {
	return &CIDRefs{registry: registry}
}

// Ready reports whether "last reference" can be decided on this gateway.
func (r *CIDRefs) Ready() bool { return !r.pending.Load() }

// Register records that namespace holds cid as kind, and reports whether the
// row is new. A caller that fails after registering releases the reference only
// if it was new: an existing one may be what another holder of the same CID in
// that namespace depends on. It runs BEFORE the cluster pin is requested, so a
// concurrent unpin by another namespace already counts it.
func (r *CIDRefs) Register(ctx context.Context, cid, namespace, kind string) (bool, error) {
	if r == nil || r.registry == nil {
		return false, ErrRefIndexUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, refQueryTimeout)
	defer cancel()
	res, err := r.registry.Exec(ctx,
		`INSERT OR IGNORE INTO ipfs_cid_refs (cid, namespace, kind) VALUES (?, ?, ?)`, cid, namespace, kind)
	if err != nil {
		return false, fmt.Errorf("failed to record the %s reference of namespace %s to %s in the cluster reference index: %w", kind, namespace, cid, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to tell whether the %s reference of namespace %s to %s was new: %w", kind, namespace, cid, err)
	}
	return n > 0, nil
}

// Release drops namespace's kind reference to cid and returns how many
// references to cid remain anywhere in the cluster.
//
// The delete comes first and the count second, and that order is the
// atomicity: the registry is linearizable, so of any set of concurrent
// releases the one whose delete lands last counts zero, and no release can
// count zero while another reference is still recorded. A check-then-delete
// would let two namespaces each see the other's reference and both keep a pin
// nobody owns.
func (r *CIDRefs) Release(ctx context.Context, cid, namespace, kind string) (int, error) {
	if r == nil || r.registry == nil {
		return 0, ErrRefIndexUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, refQueryTimeout)
	defer cancel()
	if _, err := r.registry.Exec(ctx,
		`DELETE FROM ipfs_cid_refs WHERE cid = ? AND namespace = ? AND kind = ?`, cid, namespace, kind); err != nil {
		return 0, fmt.Errorf("failed to release the %s reference of namespace %s to %s: %w", kind, namespace, cid, err)
	}
	return r.count(ctx, cid)
}

// Count is the number of references to cid across every namespace.
func (r *CIDRefs) Count(ctx context.Context, cid string) (int, error) {
	if r == nil || r.registry == nil {
		return 0, ErrRefIndexUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, refQueryTimeout)
	defer cancel()
	return r.count(ctx, cid)
}

func (r *CIDRefs) count(ctx context.Context, cid string) (int, error) {
	var rows []map[string]interface{}
	if err := r.registry.Query(ctx, &rows,
		`SELECT COUNT(*) AS count FROM ipfs_cid_refs WHERE cid = ?`, cid); err != nil {
		return 0, fmt.Errorf("failed to count the references to %s in the cluster reference index: %w", cid, err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return countFromRow(rows[0]["count"]), nil
}

// ReleaseNamespace drops every reference namespace holds, and returns the CIDs
// that no namespace references any more, whose pins may now be removed. Each
// CID is counted after the whole namespace is released, so a concurrent release
// elsewhere still leaves exactly one of the two seeing zero.
func (r *CIDRefs) ReleaseNamespace(ctx context.Context, namespace string) ([]string, error) {
	if r == nil || r.registry == nil {
		return nil, ErrRefIndexUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, refQueryTimeout)
	defer cancel()
	var held []map[string]interface{}
	if err := r.registry.Query(ctx, &held,
		`SELECT DISTINCT cid FROM ipfs_cid_refs WHERE namespace = ? AND cid != ''`, namespace); err != nil {
		return nil, fmt.Errorf("failed to list the references of namespace %s: %w", namespace, err)
	}
	if _, err := r.registry.Exec(ctx, `DELETE FROM ipfs_cid_refs WHERE namespace = ?`, namespace); err != nil {
		return nil, fmt.Errorf("failed to release the references of namespace %s: %w", namespace, err)
	}
	var orphaned []string
	for _, cid := range cidsOf(held) {
		n, err := r.count(ctx, cid)
		if err != nil {
			return orphaned, err
		}
		if n == 0 {
			orphaned = append(orphaned, cid)
		}
	}
	return orphaned, nil
}

// UnpinIfLastRef releases namespace's kind reference to cid and removes the
// cluster pin only if that leaves no reference anywhere. It never unpins when
// the count cannot be made: a leaked pin is recoverable, another tenant's
// deleted data is not. Empty cid or nil unpinner is a no-op.
func UnpinIfLastRef(ctx context.Context, refs *CIDRefs, ipfs ClusterUnpinner, cid, namespace, kind string) error {
	if cid == "" || ipfs == nil {
		return nil
	}
	if !refs.Ready() {
		return ErrRefIndexNotReady
	}
	remaining, err := refs.Release(ctx, cid, namespace, kind)
	if err != nil {
		return err
	}
	if remaining > 0 {
		return nil
	}
	if err := ipfs.Unpin(ctx, cid); err != nil {
		return fmt.Errorf("failed to remove the cluster pin of %s, which no namespace references any more: %w", cid, err)
	}
	return nil
}

// registerRef and releaseRef are the handlers' entry to the index. A handler
// set built with no database at all is the unit-test configuration (as in
// checkCIDOwnership), and has no references to keep.
func (h *Handlers) registerRef(ctx context.Context, cid, namespace string) (bool, error) {
	if h.db == nil {
		return false, nil
	}
	return h.refs.Register(ctx, cid, namespace, KindStorage)
}

func (h *Handlers) releaseRef(ctx context.Context, cid, namespace string) (int, error) {
	if h.db == nil {
		return 0, nil
	}
	return h.refs.Release(ctx, cid, namespace, KindStorage)
}

// dropFreshRef undoes a registration whose pin never happened, so it does not
// hold other namespaces' unpins back forever. Only a reference this call
// created is dropped: an existing one belongs to a pin that still stands. It
// runs on a context that outlives the request, since a cancelled request is
// one of the reasons a pin fails.
func (h *Handlers) dropFreshRef(ctx context.Context, cid, namespace string, fresh bool) {
	if !fresh {
		return
	}
	if _, err := h.releaseRef(context.WithoutCancel(ctx), cid, namespace); err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to drop the reference of a pin that did not happen; the cid stays referenced until the namespace unpins it",
			zap.Error(err), zap.String("cid", cid), zap.String("namespace", namespace))
	}
}

// StartCIDRefBackfill loads this namespace's existing pins and deployments
// into the index ONCE, for the upgrade that introduced it: content pinned by an
// older gateway has no row until then. Until it succeeds unpins are refused.
//
// This is the one place a reference is copied from tenant-writable tables. It
// is bounded: it runs once per namespace (a marker row records that it did),
// so nothing a tenant writes afterwards reaches the index, and it refuses a
// namespace with more than maxBackfillRefs references instead of loading part
// of it. A row forged into those tables BEFORE the upgrade cannot be told from
// a real one.
func (h *Handlers) StartCIDRefBackfill(ctx context.Context, namespace string) {
	h.refs.pending.Store(true)
	go func() {
		var wait time.Duration
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if err := h.backfillCIDRefs(ctx, namespace); err != nil {
				h.logger.ComponentError(logging.ComponentGeneral, "cid reference backfill failed; unpin stays disabled until it succeeds",
					zap.String("namespace", namespace), zap.Error(err))
				wait = refBackfillRetryInterval
				continue
			}
			h.refs.pending.Store(false)
			return
		}
	}()
}

func (h *Handlers) backfillCIDRefs(ctx context.Context, namespace string) error {
	if h.db == nil || h.refs.registry == nil {
		return ErrRefIndexUnavailable
	}
	var done []map[string]interface{}
	if err := h.refs.registry.Query(ctx, &done,
		`SELECT COUNT(*) AS count FROM ipfs_cid_refs WHERE cid = '' AND namespace = ? AND kind = ?`,
		namespace, kindBackfilled); err != nil {
		return fmt.Errorf("failed to read whether namespace %s was backfilled: %w", namespace, err)
	}
	if len(done) > 0 && countFromRow(done[0]["count"]) > 0 {
		return nil
	}

	pinned, err := h.backfillSource(ctx, `SELECT cid FROM ipfs_content_ownership WHERE is_pinned = 1 AND namespace = ? LIMIT ?`, namespace)
	if err != nil {
		return err
	}
	deployed, err := h.backfillSource(ctx,
		`SELECT content_cid AS cid FROM deployments WHERE namespace = ? AND content_cid IS NOT NULL AND content_cid != ''
		 UNION
		 SELECT build_cid AS cid FROM deployments WHERE namespace = ? AND build_cid IS NOT NULL AND build_cid != ''
		 LIMIT ?`, namespace, namespace)
	if err != nil {
		return err
	}
	if err := h.insertRefs(ctx, pinned, namespace, KindStorage); err != nil {
		return err
	}
	if err := h.insertRefs(ctx, deployed, namespace, KindDeployment); err != nil {
		return err
	}
	if _, err := h.refs.registry.Exec(ctx,
		`INSERT OR IGNORE INTO ipfs_cid_refs (cid, namespace, kind) VALUES ('', ?, ?)`, namespace, kindBackfilled); err != nil {
		return fmt.Errorf("failed to record that namespace %s was backfilled: %w", namespace, err)
	}
	return nil
}

// backfillSource reads at most maxBackfillRefs+1 CIDs and fails when the
// namespace has more than the bound.
func (h *Handlers) backfillSource(ctx context.Context, query string, args ...any) ([]string, error) {
	var rows []map[string]interface{}
	if err := h.db.Query(ctx, &rows, query, append(args, maxBackfillRefs+1)...); err != nil {
		return nil, fmt.Errorf("failed to read the namespace's existing references: %w", err)
	}
	if len(rows) > maxBackfillRefs {
		return nil, fmt.Errorf("the namespace has more than %d references to load into the cluster reference index", maxBackfillRefs)
	}
	return cidsOf(rows), nil
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
		if _, err := h.refs.registry.Exec(ctx, q, args...); err != nil {
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
