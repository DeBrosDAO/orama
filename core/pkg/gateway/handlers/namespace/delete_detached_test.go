package namespace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	namespacepkg "github.com/DeBrosOfficial/network/pkg/namespace"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// disconnectingDeprov is a teardown during which the client goes away: the
// request context is cancelled the moment the teardown starts, as a killed CLI
// cancels it.
type disconnectingDeprov struct {
	disconnect context.CancelFunc
	errDuring  error
	deadline   time.Time
	hasLimit   bool
}

func (d *disconnectingDeprov) DeprovisionCluster(ctx context.Context, _ int64) error {
	d.disconnect()
	d.errDuring = ctx.Err()
	d.deadline, d.hasLimit = ctx.Deadline()
	return nil
}

// A client that disconnects mid-delete used to cancel the teardown half way:
// the stop sent to a node failed "context canceled", and so did the write that
// recorded the failure for retry, which left the cluster in 'deprovisioning'
// with nothing owed to anyone. The removal belongs to the handler: it is not
// cancelled by the request, is bounded by its own timeout, and finishes.
//
// Mutation check: pass r.Context() to DeprovisionCluster again and errDuring is
// context.Canceled.
func TestServeHTTP_aClientThatDisconnectsMidDeleteDoesNotCancelTheTeardown(t *testing.T) {
	db := migratedDB(t)
	dp := &disconnectingDeprov{}
	h := NewDeleteHandler(dp, rqlite.NewClient(db), nil, nil, zap.NewNop())
	seedNamespaces(t, h, "gone")

	reqCtx, cancel := context.WithCancel(deleteRequest("gone").Context())
	dp.disconnect = cancel
	req := deleteRequest("gone").WithContext(reqCtx)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if dp.errDuring != nil {
		t.Fatalf("the teardown's context was cancelled with the request: %v", dp.errDuring)
	}
	if !dp.hasLimit || time.Until(dp.deadline) > namespacepkg.DeprovisionTimeout {
		t.Fatalf("the teardown has no bound of its own (deadline %v, set %v)", dp.deadline, dp.hasLimit)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE name = 'gone'`); n != 0 {
		t.Fatal("the delete stopped after the client left: the namespace row is still there")
	}
}

// The values of the request are kept for the detached removal: the namespace
// override is how the audit and the registry writes know whose delete it is.
func TestServeHTTP_theDetachedRemovalKeepsTheRequestValues(t *testing.T) {
	db := migratedDB(t)
	dp := &valueCheckingDeprov{}
	h := NewDeleteHandler(dp, rqlite.NewClient(db), nil, nil, zap.NewNop())
	seedNamespaces(t, h, "gone")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, deleteRequest("gone"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !dp.sawNamespace {
		t.Fatal("the removal's context lost the request's values")
	}
}

type valueCheckingDeprov struct{ sawNamespace bool }

func (d *valueCheckingDeprov) DeprovisionCluster(ctx context.Context, _ int64) error {
	d.sawNamespace = ctx.Value(ctxkeys.NamespaceOverride) == "gone"
	return nil
}

// blockingDeprov holds the teardown open until released, counting its calls.
type blockingDeprov struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func (d *blockingDeprov) DeprovisionCluster(context.Context, int64) error {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	d.started <- struct{}{}
	<-d.release
	return nil
}

// The first delete keeps running when its client leaves, so the client retries:
// a second delete of the same namespace is refused 409 and retryable, and the
// teardown runs once.
//
// Mutation check: drop the per-process guard and the registry claim and the
// second request starts a second teardown.
func TestServeHTTP_aSecondDeleteWhileTheFirstRunsIsRefused(t *testing.T) {
	db := migratedDB(t)
	dp := &blockingDeprov{started: make(chan struct{}, 2), release: make(chan struct{})}
	h := NewDeleteHandler(dp, rqlite.NewClient(db), nil, nil, zap.NewNop())
	seedNamespaces(t, h, "gone")

	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(first, deleteRequest("gone")); close(done) }()
	<-dp.started

	second := httptest.NewRecorder()
	h.ServeHTTP(second, deleteRequest("gone"))
	if second.Code != http.StatusConflict {
		t.Fatalf("the second delete answered %d: %s", second.Code, second.Body.String())
	}
	if second.Header().Get("Retry-After") == "" || !strings.Contains(second.Body.String(), `"retryable":true`) ||
		!strings.Contains(second.Body.String(), ErrCodeDeleteInProgress) {
		t.Fatalf("the refusal is not a retryable %s: %v %s", ErrCodeDeleteInProgress, second.Header(), second.Body.String())
	}

	close(dp.release)
	<-done
	if first.Code != http.StatusOK {
		t.Fatalf("the first delete answered %d: %s", first.Code, first.Body.String())
	}
	dp.mu.Lock()
	defer dp.mu.Unlock()
	if dp.calls != 1 {
		t.Fatalf("teardowns = %d, want 1", dp.calls)
	}
}

// The registry refuses it across gateways: a cluster another gateway is tearing
// down (stamped within the window) is not torn down again, and an abandoned one
// (stamp older than the window) is taken over.
func TestServeHTTP_aTeardownOwnedByAnotherGatewayRefusesTheDelete(t *testing.T) {
	for name, tc := range map[string]struct {
		stamp string
		want  int
	}{
		"fresh stamp":            {"-1 minutes", http.StatusConflict},
		"stale stamp":            {"-1 hour", http.StatusOK},
		"no stamp (old release)": {"", http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			db := migratedDB(t)
			dp := &callCountingDeprov{}
			h := NewDeleteHandler(dp, rqlite.NewClient(db), nil, nil, zap.NewNop())
			seedNamespaces(t, h, "gone")
			if tc.stamp == "" {
				db.Exec(`UPDATE namespace_clusters SET status = 'deprovisioning', deprovisioning_at = NULL WHERE namespace_name = 'gone'`)
			} else if _, err := db.Exec(`UPDATE namespace_clusters SET status = 'deprovisioning', deprovisioning_at = datetime('now', ?) WHERE namespace_name = 'gone'`, tc.stamp); err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, deleteRequest("gone"))
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if (tc.want == http.StatusConflict) == (dp.calls > 0) {
				t.Fatalf("teardown calls = %d for a %d", dp.calls, tc.want)
			}
		})
	}
}

type callCountingDeprov struct{ calls int }

func (d *callCountingDeprov) DeprovisionCluster(context.Context, int64) error { d.calls++; return nil }

// The refusal of a finished delete's retry is not a 409: once the first has
// ended, the same name is deletable (or gone) again.
func TestServeHTTP_theGuardIsReleasedWhenTheDeleteEnds(t *testing.T) {
	db := migratedDB(t)
	h := NewDeleteHandler(&callCountingDeprov{}, rqlite.NewClient(db), nil, nil, zap.NewNop())
	seedNamespaces(t, h, "gone")
	for i, want := range []int{http.StatusOK, http.StatusNotFound} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, deleteRequest("gone"))
		if rec.Code != want {
			t.Fatalf("delete %d answered %d, want %d: %s", i, rec.Code, want, rec.Body.String())
		}
	}
}

type countingFailingDeprov struct{ calls int }

func (d *countingFailingDeprov) DeprovisionCluster(context.Context, int64) error {
	d.calls++
	return errors.New("node 10.0.0.2 did not answer")
}

// A delete that fails gives back the claim it took, so the retry its 500 asks
// for runs instead of being refused 409 for the rest of the window.
func TestServeHTTP_aFailedDeleteReleasesItsClaimForTheRetry(t *testing.T) {
	db := migratedDB(t)
	dp := &countingFailingDeprov{}
	h := NewDeleteHandler(dp, rqlite.NewClient(db), nil, nil, zap.NewNop())
	seedNamespaces(t, h, "gone")
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, deleteRequest("gone"))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("delete %d answered %d, want 500: %s", i, rec.Code, rec.Body.String())
		}
	}
	if dp.calls != 2 {
		t.Fatalf("the retry ran %d teardowns, want 2: the failed delete kept its claim", dp.calls)
	}
}
