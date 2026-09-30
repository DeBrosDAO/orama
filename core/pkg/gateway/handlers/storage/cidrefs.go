package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
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

	// refBackfillRetryInterval and refBackfillMaxInterval bound the backoff
	// between failed backfill attempts (it doubles from the first to the
	// second).
	refBackfillRetryInterval = 5 * time.Second
	refBackfillMaxInterval   = 5 * time.Minute

	// refDeferredInterval is how often a gateway holding deferred unpins checks
	// whether the index has become ready for them.
	refDeferredInterval = 30 * time.Second

	// maxReportedNamespaces caps the namespaces named in a not-ready error.
	maxReportedNamespaces = 20

	// maxDeferredUnpins bounds the unpins remembered while the index loads.
	maxDeferredUnpins = 100_000

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

// releaseBatch is how many of a namespace's CIDs ReleaseNamespace deletes and
// counts per round trip, and maxReleaseBatches how many rounds one call makes.
// A namespace with more remains partly held and the call says so; the caller
// retries, and the rows not yet released are still there to find. Variables
// only so a test can lower them.
var (
	releaseBatch      = 500
	maxReleaseBatches = 200
)

var (
	// ErrRefIndexUnavailable is returned when there is no registry handle to
	// count in. A caller must not read it as "no references".
	ErrRefIndexUnavailable = errors.New("the cluster reference index is not available on this gateway")

	errBackfillTooLarge = errors.New("the namespace is too large to load into the cluster reference index")

	// ErrNamespaceRefsRemain is returned by ReleaseNamespace when the namespace
	// holds more references than one call releases. Nothing is lost: call again.
	ErrNamespaceRefsRemain = errors.New("the namespace still holds references in the cluster reference index")

	// ErrRefIndexNotReady is returned while the index does not yet hold every
	// namespace's existing references, so "last reference" cannot be decided.
	ErrRefIndexNotReady = errors.New("the cluster reference index is still being built")
)

// NotBackfilledError says which namespaces have not loaded their existing
// references into the index. It is an ErrRefIndexNotReady. Its message does not
// name them (it reaches tenants); log Namespaces for the operator.
type NotBackfilledError struct{ Namespaces []string }

func (e *NotBackfilledError) Error() string {
	return fmt.Sprintf("%s: %d namespaces have not loaded their existing references yet", ErrRefIndexNotReady, len(e.Namespaces))
}

func (e *NotBackfilledError) Is(target error) bool { return target == ErrRefIndexNotReady }

// CIDRefs is the cluster-wide reference index over the cluster registry.
type CIDRefs struct {
	registry rqlite.Client
	pending  atomic.Bool
	// failure is set, once, when this gateway's own namespace can never be
	// loaded (it is over maxBackfillRefs); it is the operator-visible reason.
	failure atomic.Pointer[string]

	// mu makes "the index becomes ready" and "an unpin is deferred until it
	// does" one step each, so a deferral is never added after the flush.
	mu       sync.Mutex
	deferred map[string]ClusterPinner
}

// NewCIDRefs returns the index over the registry's RQLite. A nil registry
// yields an index whose every operation fails with ErrRefIndexUnavailable.
func NewCIDRefs(registry rqlite.Client) *CIDRefs {
	return &CIDRefs{registry: registry}
}

// CheckReady reports whether "last reference" can be decided, as nil or an
// error that is an ErrRefIndexNotReady. It is ready only when this gateway's own
// namespace has been loaded and every other live namespace has too: an
// unloaded namespace's references are missing from the count, and a count that
// omits a holder deletes that holder's data. except is a namespace being
// deleted, whose own references no longer matter.
//
// A namespace whose cluster is being torn down, or never became ready (still
// provisioning, or failed before it was ever ready), has no running gateway to
// load anything and holds no content, so it is not waited for; nor is one with
// no cluster at all (a create whose provisioning never started). Without that,
// one failed create would refuse every unpin in the cluster. The lobby
// "default" namespace has no cluster row and is always waited for (its gateway
// is the index gateway). A cluster whose ready_at was never written but whose
// status is ready or degraded is waited for: it is serving. A namespace whose cluster WAS ready and is now
// failed keeps blocking: its content is real and may be unprotected until it is
// repaired or removed (`orama cluster namespace remove`).
func (r *CIDRefs) CheckReady(ctx context.Context, except string) error {
	if r == nil || r.registry == nil {
		return ErrRefIndexUnavailable
	}
	if msg := r.failure.Load(); msg != nil {
		return fmt.Errorf("%w: %s", ErrRefIndexNotReady, *msg)
	}
	if r.pending.Load() {
		return ErrRefIndexNotReady
	}
	ctx, cancel := context.WithTimeout(ctx, refQueryTimeout)
	defer cancel()
	var rows []map[string]interface{}
	if err := r.registry.Query(ctx, &rows,
		`SELECT name FROM namespaces
		  WHERE name != ?
		    AND NOT EXISTS (SELECT 1 FROM ipfs_cid_refs r
		                     WHERE r.namespace = namespaces.name AND r.cid = '' AND r.kind = ?)
		    AND NOT EXISTS (SELECT 1 FROM namespace_clusters c
		                     WHERE c.namespace_id = namespaces.id
		                       AND (c.status = 'deprovisioning'
		                            OR (c.ready_at IS NULL AND c.status IN ('provisioning', 'failed'))))
		    AND (name = ? OR EXISTS (SELECT 1 FROM namespace_clusters c WHERE c.namespace_id = namespaces.id))
		  LIMIT ?`, except, kindBackfilled, gwauth.LobbyNamespace, maxReportedNamespaces); err != nil {
		return fmt.Errorf("failed to read which namespaces have loaded their references into the cluster reference index: %w", err)
	}
	var missing []string
	for _, row := range rows {
		if n, ok := row["name"].(string); ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return &NotBackfilledError{Namespaces: missing}
	}
	return nil
}

// Register records that namespace holds cid as kind. Registrations are
// counted (holders): a row shared by two concurrent registrants of the same
// content survives the failure of one of them, see Unregister. It runs BEFORE
// the cluster pin is requested, so a concurrent unpin by another namespace
// already counts it.
func (r *CIDRefs) Register(ctx context.Context, cid, namespace, kind string) error {
	if r == nil || r.registry == nil {
		return ErrRefIndexUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, refQueryTimeout)
	defer cancel()
	if _, err := r.registry.Exec(ctx,
		`INSERT INTO ipfs_cid_refs (cid, namespace, kind) VALUES (?, ?, ?)
		 ON CONFLICT(cid, namespace, kind) DO UPDATE SET holders = holders + 1`, cid, namespace, kind); err != nil {
		return fmt.Errorf("failed to record the %s reference of namespace %s to %s in the cluster reference index: %w", kind, namespace, cid, err)
	}
	return nil
}

// Unregister takes back one Register whose pin or deployment did not happen.
// The row goes only when no other registration stands behind it, so a request
// that failed cannot delete the reference a concurrent request that succeeded
// depends on. Use Release, not this, when the namespace lets go of the CID.
func (r *CIDRefs) Unregister(ctx context.Context, cid, namespace, kind string) error {
	if r == nil || r.registry == nil {
		return ErrRefIndexUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, refQueryTimeout)
	defer cancel()
	if _, err := r.registry.Exec(ctx,
		`UPDATE ipfs_cid_refs SET holders = holders - 1 WHERE cid = ? AND namespace = ? AND kind = ?`, cid, namespace, kind); err != nil {
		return fmt.Errorf("failed to take back the %s registration of namespace %s for %s: %w", kind, namespace, cid, err)
	}
	if _, err := r.registry.Exec(ctx,
		`DELETE FROM ipfs_cid_refs WHERE cid = ? AND namespace = ? AND kind = ? AND holders <= 0`, cid, namespace, kind); err != nil {
		return fmt.Errorf("failed to remove the unheld %s reference of namespace %s to %s: %w", kind, namespace, cid, err)
	}
	return nil
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
		`SELECT COUNT(*) AS count FROM ipfs_cid_refs WHERE cid = ? AND holders > 0`, cid); err != nil {
		return 0, fmt.Errorf("failed to count the references to %s in the cluster reference index: %w", cid, err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return countFromRow(rows[0]["count"]), nil
}

// ReleaseNamespace drops every reference namespace holds, and returns the CIDs
// that no namespace references any more, whose pins may now be removed. It works
// in batches: a batch's rows are first tombstoned (holders set to zero, which the
// count ignores, exactly as if they were gone), then recounted, and only then
// deleted. So of two releases racing on one CID the later tombstone sees zero,
// and a batch that fails at any step leaves its rows in place: they are found
// again by the next call, and their CIDs are neither forgotten nor unpinned
// unseen. The call returns the orphans of the batches that completed, and the
// error. The namespace's backfill marker is not touched here (RemoveMarker),
// because it must outlive every step that can still fail. A namespace larger
// than maxReleaseBatches*releaseBatch CIDs returns ErrNamespaceRefsRemain.
func (r *CIDRefs) ReleaseNamespace(ctx context.Context, namespace string) ([]string, error) {
	if r == nil || r.registry == nil {
		return nil, ErrRefIndexUnavailable
	}
	var orphaned []string
	for range maxReleaseBatches {
		batchOrphans, n, err := r.releaseBatch(ctx, namespace)
		if err != nil {
			return orphaned, err
		}
		if n == 0 {
			return orphaned, nil
		}
		orphaned = append(orphaned, batchOrphans...)
	}
	return orphaned, ErrNamespaceRefsRemain
}

// releaseBatch releases up to releaseBatch of the namespace's CIDs and returns
// those that are now unreferenced and how many CIDs it handled. Each registry
// round trip runs under its own refQueryTimeout.
func (r *CIDRefs) releaseBatch(ctx context.Context, namespace string) ([]string, int, error) {
	var held []map[string]interface{}
	if err := r.query(ctx, &held,
		`SELECT DISTINCT cid FROM ipfs_cid_refs WHERE namespace = ? AND cid != '' LIMIT ?`, namespace, releaseBatch); err != nil {
		return nil, 0, fmt.Errorf("failed to list the references of namespace %s: %w", namespace, err)
	}
	cids := cidsOf(held)
	if len(cids) == 0 {
		return nil, 0, nil
	}
	in := "(" + strings.TrimSuffix(strings.Repeat("?,", len(cids)), ",") + ")"
	cidArgs := make([]any, len(cids))
	for i, c := range cids {
		cidArgs[i] = c
	}
	nsArgs := append([]any{namespace}, cidArgs...)

	if err := r.exec(ctx, `UPDATE ipfs_cid_refs SET holders = 0 WHERE namespace = ? AND cid IN `+in, nsArgs...); err != nil {
		return nil, 0, fmt.Errorf("failed to release %d references of namespace %s: %w", len(cids), namespace, err)
	}
	var still []map[string]interface{}
	if err := r.query(ctx, &still, `SELECT DISTINCT cid FROM ipfs_cid_refs WHERE holders > 0 AND cid IN `+in, cidArgs...); err != nil {
		return nil, 0, fmt.Errorf("failed to count the references that remain after releasing %d of namespace %s (its rows stay for a retry): %w", len(cids), namespace, err)
	}
	if err := r.exec(ctx, `DELETE FROM ipfs_cid_refs WHERE namespace = ? AND holders <= 0 AND cid IN `+in, nsArgs...); err != nil {
		return nil, 0, fmt.Errorf("failed to remove the released references of namespace %s (its rows stay for a retry): %w", namespace, err)
	}
	remaining := make(map[string]struct{}, len(still))
	for _, c := range cidsOf(still) {
		remaining[c] = struct{}{}
	}
	var orphaned []string
	for _, c := range cids {
		if _, ok := remaining[c]; !ok {
			orphaned = append(orphaned, c)
		}
	}
	return orphaned, len(cids), nil
}

func (r *CIDRefs) query(ctx context.Context, dest any, q string, args ...any) error {
	ctx, cancel := context.WithTimeout(ctx, refQueryTimeout)
	defer cancel()
	return r.registry.Query(ctx, dest, q, args...)
}

func (r *CIDRefs) exec(ctx context.Context, q string, args ...any) error {
	ctx, cancel := context.WithTimeout(ctx, refQueryTimeout)
	defer cancel()
	_, err := r.registry.Exec(ctx, q, args...)
	return err
}

// RemoveMarker deletes the namespace's backfill marker. Call it as the very last
// step of deleting the namespace, after its row is gone: the marker is what
// keeps CheckReady true for a namespace that still exists.
func (r *CIDRefs) RemoveMarker(ctx context.Context, namespace string) error {
	if r == nil || r.registry == nil {
		return ErrRefIndexUnavailable
	}
	if err := r.exec(ctx, `DELETE FROM ipfs_cid_refs WHERE namespace = ? AND cid = '' AND kind = ?`, namespace, kindBackfilled); err != nil {
		return fmt.Errorf("failed to remove the reference index marker of namespace %s: %w", namespace, err)
	}
	return nil
}

// UnpinIfLastRef releases namespace's kind reference to cid and removes the
// cluster pin only if that leaves no reference anywhere. It never unpins when
// the count cannot be trusted: a leaked pin is recoverable, another tenant's
// deleted data is not. While the index is not ready (CheckReady) references
// written by older gateways may be missing, so a release that leaves none does
// not unpin now: the CID is remembered and, once the index is ready, recounted
// and unpinned if still unreferenced. The memory is the process's; a restart
// inside that window loses it and the pin stays. Readiness is read first, so
// its round trip is not spent between the count and the unpin. Empty cid or nil
// pinner is a no-op.
func UnpinIfLastRef(ctx context.Context, refs *CIDRefs, ipfs ClusterPinner, cid, namespace, kind string) error {
	if cid == "" || ipfs == nil {
		return nil
	}
	readyErr := refs.CheckReady(ctx, "")
	if readyErr != nil && !errors.Is(readyErr, ErrRefIndexNotReady) {
		return readyErr
	}
	remaining, err := refs.Release(ctx, cid, namespace, kind)
	if err != nil {
		return err
	}
	if remaining > 0 {
		return nil
	}
	if readyErr != nil {
		refs.deferUnpin(cid, ipfs)
		return nil
	}
	_, err = refs.UnpinUnreferenced(ctx, ipfs, cid)
	return err
}

// UnpinOutcome says what UnpinUnreferenced found.
type UnpinOutcome struct {
	// AlreadyUnpinned: the cluster had no pin to remove.
	AlreadyUnpinned bool
	// Restored: a reference appeared while the pin was being removed, so the pin
	// was put back (or left to the new holder's own pin).
	Restored bool
}

// UnpinUnreferenced removes the cluster pin of a CID the caller has just counted
// zero references to, and closes the gap between that count and the unpin.
//
// A namespace can register the CID and ask for its pin (idempotent, so a no-op
// on a pin that still stands) after the count and before the unpin lands; the
// unpin then removes the pin that namespace relies on, and a loop of pin and
// unpin of a known CID would destroy anyone's copy. So after the unpin the CID
// is counted again: a reference that appeared before that count means the pin is
// put back with the options it had. A reference registered after it registers
// before its own pin, which is then a real pin, because it comes after the
// unpin. The choice is after-the-fact repair over a lease that every
// registration must check: it needs nothing from Register, and its only cost is
// that a CID which just gained a holder is unpinned and pinned again, so the
// content can be briefly unreplicated (never deleted: blocks leave a node only
// by garbage collection, on a timer, or the explicit eviction, which counts
// again).
func (r *CIDRefs) UnpinUnreferenced(ctx context.Context, ipfs ClusterPinner, cid string) (UnpinOutcome, error) {
	name, rf, known, err := pinOptionsOf(ctx, ipfs, cid)
	if err != nil {
		return UnpinOutcome{}, err
	}
	if err := ipfs.Unpin(ctx, cid); err != nil {
		if isAlreadyUnpinned(err) {
			return UnpinOutcome{AlreadyUnpinned: true}, nil
		}
		return UnpinOutcome{}, fmt.Errorf("failed to remove the cluster pin of %s, which no namespace references any more: %w", cid, err)
	}
	after := context.WithoutCancel(ctx)
	n, countErr := r.Count(after, cid)
	if countErr == nil && n == 0 {
		return UnpinOutcome{}, nil
	}
	if known {
		if _, err := ipfs.Pin(after, cid, name, rf); err != nil {
			return UnpinOutcome{Restored: true}, fmt.Errorf("the cluster pin of %s was removed while a reference to it appeared, and putting it back failed: %w", cid, err)
		}
	}
	if countErr != nil {
		return UnpinOutcome{Restored: known}, fmt.Errorf("could not confirm after unpinning %s that nothing references it, so its pin was put back: %w", cid, countErr)
	}
	return UnpinOutcome{Restored: true}, nil
}

// pinOptionsOf reads the pin's name and replication so it can be put back. known
// is false when the cluster has no pin (nothing to restore). An unreadable
// cluster is an error: the pin is not removed blind.
func pinOptionsOf(ctx context.Context, ipfs ClusterPinner, cid string) (name string, rf int, known bool, err error) {
	status, err := ipfs.PinStatus(ctx, cid)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "not found") || strings.Contains(msg, "404") || isAlreadyUnpinned(err) {
			return "", 0, false, nil
		}
		return "", 0, false, fmt.Errorf("failed to read the pin of %s before removing it: %w", cid, err)
	}
	if status == nil {
		return "", 0, false, nil
	}
	rf = status.ReplicationMax
	if rf == 0 {
		rf = status.ReplicationFactor
	}
	return status.Name, rf, true, nil
}

// deferUnpin remembers an unpin for when the index is ready. When the memory is
// full nothing is remembered and the pin is left in place.
func (r *CIDRefs) deferUnpin(cid string, ipfs ClusterPinner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.deferred) >= maxDeferredUnpins {
		return
	}
	if r.deferred == nil {
		r.deferred = make(map[string]ClusterPinner)
	}
	r.deferred[cid] = ipfs
}

func (r *CIDRefs) takeDeferred() map[string]ClusterPinner {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.deferred
	r.deferred = nil
	return d
}

func (r *CIDRefs) hasDeferred() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.deferred) > 0
}

// applyDeferred unpins each deferred CID that still has no reference. A CID
// that was registered again meanwhile is left alone.
func (h *Handlers) applyDeferred(ctx context.Context, deferred map[string]ClusterPinner) {
	for cid, ipfs := range deferred {
		n, err := h.refs.Count(ctx, cid)
		if err != nil {
			h.logger.ComponentError(logging.ComponentGeneral, "could not recount a deferred unpin; the pin stays",
				zap.String("cid", cid), zap.Error(err))
			continue
		}
		if n > 0 {
			continue
		}
		if _, err := h.refs.UnpinUnreferenced(ctx, ipfs, cid); err != nil {
			h.logger.ComponentError(logging.ComponentGeneral, "deferred unpin failed",
				zap.String("cid", cid), zap.Error(err))
		}
	}
}

// registerRef and releaseRef are the handlers' entry to the index. A handler
// set built with no database at all is the unit-test configuration (as in
// checkCIDOwnership), and has no references to keep.
func (h *Handlers) registerRef(ctx context.Context, cid, namespace string) error {
	if h.db == nil {
		return nil
	}
	return h.refs.Register(ctx, cid, namespace, KindStorage)
}

func (h *Handlers) releaseRef(ctx context.Context, cid, namespace string) (int, error) {
	if h.db == nil {
		return 0, nil
	}
	return h.refs.Release(ctx, cid, namespace, KindStorage)
}

// dropRef takes back a registration whose pin never happened, so it does not
// hold other namespaces' unpins back forever. It only subtracts this request's
// registration: a concurrent request registering the same content keeps its
// own. It runs on a context that outlives the request, since a cancelled
// request is one of the reasons a pin fails.
func (h *Handlers) dropRef(ctx context.Context, cid, namespace string) {
	if h.db == nil {
		return
	}
	if err := h.refs.Unregister(context.WithoutCancel(ctx), cid, namespace, KindStorage); err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to take back the reference of a pin that did not happen; the cid stays referenced until the namespace unpins it",
			zap.Error(err), zap.String("cid", cid), zap.String("namespace", namespace))
	}
}

// StartCIDRefBackfill loads this namespace's existing pins and deployments
// into the index ONCE, for the upgrade that introduced it: content pinned by an
// older gateway has no row until then. Until every live namespace has done so
// (CheckReady) unpins are refused or deferred.
//
// This is the one place a reference is copied from tenant-writable tables. It
// is bounded: it runs once per namespace (a marker row records that it did),
// so nothing a tenant writes afterwards reaches the index, and it refuses a
// namespace with more than maxBackfillRefs references instead of loading part
// of it. A row forged into those tables BEFORE the upgrade cannot be told from
// a real one.
//
// A failed attempt is retried with exponential backoff. A namespace over the
// bound is not retried: it is logged once as an error, and it is the reason
// unpins answer 503 on this gateway; every other gateway's unpins are refused
// too, because this namespace never gets its marker (see docs/SECURITY.md for
// how an operator resolves it). After the backfill the loop applies the unpins
// deferred while the index was not ready.
func (h *Handlers) StartCIDRefBackfill(ctx context.Context, namespace string) {
	h.refs.pending.Store(true)
	go func() {
		wait := time.Duration(0)
		backoff := refBackfillRetryInterval
		loaded := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if !loaded {
				err := h.backfillCIDRefs(ctx, namespace)
				switch {
				case errors.Is(err, errBackfillTooLarge):
					msg := "this gateway's namespace holds more references than the one-time load into the cluster reference index allows, so no unpin can be decided; an operator must resolve it"
					h.refs.failure.Store(&msg)
					h.logger.ComponentError(logging.ComponentGeneral, "cid reference backfill cannot complete; unpins are disabled cluster-wide until an operator resolves it",
						zap.String("namespace", namespace), zap.Error(err))
					return
				case err != nil:
					h.logger.ComponentError(logging.ComponentGeneral, "cid reference backfill failed; unpin stays disabled until it succeeds",
						zap.String("namespace", namespace), zap.Duration("retry_in", backoff), zap.Error(err))
					wait = backoff
					backoff = min(backoff*2, refBackfillMaxInterval)
					continue
				}
				loaded = true
				h.refs.pending.Store(false)
			}
			wait = refDeferredInterval
			if h.refs.hasDeferred() && h.refs.CheckReady(ctx, "") == nil {
				h.applyDeferred(ctx, h.refs.takeDeferred())
			}
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
		return nil, fmt.Errorf("%w: more than %d references", errBackfillTooLarge, maxBackfillRefs)
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
