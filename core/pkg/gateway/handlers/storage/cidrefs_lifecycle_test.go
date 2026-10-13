package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

func pinRequest(h *Handlers, ns, cid string) *httptest.ResponseRecorder {
	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/storage/pin", strings.NewReader(`{"cid":"`+cid+`"}`)), ns)
	rec := httptest.NewRecorder()
	h.PinHandler(rec, req)
	return rec
}

// Bug: the reference was registered before Pin and nothing released it when
// Pin failed, so every other namespace's unpin of the CID kept the pin forever.
func TestPin_failure_dropsTheReferenceItRegistered(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinErr: errors.New("cluster down")}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	if err := a.recordCIDOwnership(context.Background(), sharedCID, "ns-a", "f", "ns-a", 1); err != nil {
		t.Fatal(err)
	}
	if rec := pinRequest(a, "ns-a", sharedCID); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if n := refsOf(t, registry, sharedCID); n != 0 {
		t.Fatalf("a pin that failed left %d references", n)
	}
}

// A failed RE-pin must not drop the reference of the pin that still stands:
// another namespace's unpin would then remove a pin this one depends on.
func TestPin_failedRepin_keepsTheExistingReference(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	pinAs(t, a, "ns-a", sharedCID)
	mock.pinErr = errors.New("cluster down")
	if rec := pinRequest(a, "ns-a", sharedCID); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if n := refsOf(t, registry, sharedCID); n != 1 {
		t.Fatalf("references = %d, want the standing pin's 1", n)
	}
}

// The async upload path gives up after one retry and drops the reference too.
func TestPinAsync_givingUp_dropsTheReference(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinErr: errors.New("cluster down")}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	if err := a.registerRef(context.Background(), sharedCID, "ns-a"); err != nil {
		t.Fatal(err)
	}
	a.pinAsync(sharedCID, "f", 3, "ns-a")
	if n := refsOf(t, registry, sharedCID); n != 0 {
		t.Fatalf("a pin that was given up on left %d references", n)
	}
}

// The unpin marks the row unpinned before it releases the reference.
func TestUnpin_marksRowUnpinned(t *testing.T) {
	registry := registrySchema(t)
	nsDB := namespaceSchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	a := gatewayFor(t, mock, nsDB, registry)
	pinAs(t, a, "ns-a", sharedCID)
	if rec := unpinAs(a, "ns-a", sharedCID); rec.Code != http.StatusOK {
		t.Fatalf("unpin = %d", rec.Code)
	}
	var pinned bool
	if err := nsDB.db.QueryRow(`SELECT is_pinned FROM ipfs_content_ownership WHERE cid = ?`, sharedCID).Scan(&pinned); err != nil || pinned {
		t.Fatalf("is_pinned = %v (%v), want false", pinned, err)
	}
}

// Bug: with no registry handle the release returned (0, nil), which reads as
// "last reference" and failed open.
func TestRelease_withoutARegistry_isAnErrorNotZero(t *testing.T) {
	refs := NewCIDRefs(nil)
	if n, err := refs.Release(context.Background(), sharedCID, "ns-a", KindStorage); !errors.Is(err, ErrRefIndexUnavailable) {
		t.Fatalf("Release = %d, %v; want ErrRefIndexUnavailable", n, err)
	}
	if err := refs.Register(context.Background(), sharedCID, "ns-a", KindStorage); !errors.Is(err, ErrRefIndexUnavailable) {
		t.Fatalf("Register err = %v", err)
	}
	if _, err := refs.Count(context.Background(), sharedCID); !errors.Is(err, ErrRefIndexUnavailable) {
		t.Fatalf("Count err = %v", err)
	}
	if err := UnpinIfLastRef(context.Background(), refs, &mockIPFSClient{}, sharedCID, "ns-a", KindStorage); !errors.Is(err, ErrRefIndexUnavailable) {
		t.Fatalf("UnpinIfLastRef err = %v", err)
	}
}

func TestUnpinIfLastRef(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*CIDRefs, *mockIPFSClient, *sqliteDB) {
		registry := registrySchema(t)
		return NewCIDRefs(registry), &mockIPFSClient{}, registry
	}

	t.Run("last reference unpins", func(t *testing.T) {
		refs, ipfsMock, _ := setup(t)
		if err := refs.Register(ctx, sharedCID, "ns-a", KindDeployment); err != nil {
			t.Fatal(err)
		}
		if err := UnpinIfLastRef(ctx, refs, ipfsMock, sharedCID, "ns-a", KindDeployment); err != nil || ipfsMock.unpinCalls != 1 {
			t.Fatalf("err %v, unpins %d", err, ipfsMock.unpinCalls)
		}
	})
	t.Run("another kind in the same namespace keeps it", func(t *testing.T) {
		refs, ipfsMock, _ := setup(t)
		for _, k := range []string{KindStorage, KindDeployment} {
			if err := refs.Register(ctx, sharedCID, "ns-a", k); err != nil {
				t.Fatal(err)
			}
		}
		if err := UnpinIfLastRef(ctx, refs, ipfsMock, sharedCID, "ns-a", KindDeployment); err != nil || ipfsMock.unpinCalls != 0 {
			t.Fatalf("err %v, unpins %d; the storage pin still holds it", err, ipfsMock.unpinCalls)
		}
	})
	t.Run("another namespace keeps it", func(t *testing.T) {
		refs, ipfsMock, _ := setup(t)
		for _, ns := range []string{"ns-a", "ns-b"} {
			if err := refs.Register(ctx, sharedCID, ns, KindDeployment); err != nil {
				t.Fatal(err)
			}
		}
		if err := UnpinIfLastRef(ctx, refs, ipfsMock, sharedCID, "ns-a", KindDeployment); err != nil || ipfsMock.unpinCalls != 0 {
			t.Fatalf("err %v, unpins %d", err, ipfsMock.unpinCalls)
		}
	})
	t.Run("not ready defers the unpin until it is", func(t *testing.T) {
		refs, ipfsMock, _ := setup(t)
		refs.pending.Store(true)
		if err := refs.Register(ctx, sharedCID, "ns-a", KindDeployment); err != nil {
			t.Fatal(err)
		}
		if err := UnpinIfLastRef(ctx, refs, ipfsMock, sharedCID, "ns-a", KindDeployment); err != nil || ipfsMock.unpinCalls != 0 {
			t.Fatalf("err %v, unpins %d; want the unpin deferred", err, ipfsMock.unpinCalls)
		}
		if !refs.hasDeferred() {
			t.Fatal("the unpin was not remembered")
		}
	})
	t.Run("registry error never unpins", func(t *testing.T) {
		refs, ipfsMock, registry := setup(t)
		if _, err := registry.db.Exec(`DROP TABLE ipfs_cid_refs`); err != nil {
			t.Fatal(err)
		}
		if err := UnpinIfLastRef(ctx, refs, ipfsMock, sharedCID, "ns-a", KindDeployment); err == nil || ipfsMock.unpinCalls != 0 {
			t.Fatalf("err %v, unpins %d", err, ipfsMock.unpinCalls)
		}
	})
	t.Run("empty cid is a no-op", func(t *testing.T) {
		refs, ipfsMock, _ := setup(t)
		if err := UnpinIfLastRef(ctx, refs, ipfsMock, "", "ns-a", KindDeployment); err != nil || ipfsMock.unpinCalls != 0 {
			t.Fatalf("err %v, unpins %d", err, ipfsMock.unpinCalls)
		}
	})
	t.Run("unpin failure is reported", func(t *testing.T) {
		refs, _, _ := setup(t)
		failing := &mockIPFSClient{unpinErr: errors.New("cluster down")}
		if err := UnpinIfLastRef(ctx, refs, failing, sharedCID, "ns-a", KindDeployment); err == nil {
			t.Fatal("an unpin that failed was reported as success")
		}
	})
}

func TestReleaseNamespace(t *testing.T) {
	ctx := context.Background()
	registry := registrySchema(t)
	refs := NewCIDRefs(registry)
	for _, r := range [][3]string{
		{"QmOnlyA", "ns-a", KindStorage},
		{"QmShared", "ns-a", KindDeployment},
		{"QmShared", "ns-b", KindStorage},
		{"", "ns-a", kindBackfilled},
	} {
		if err := refs.Register(ctx, r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
	orphaned, err := refs.ReleaseNamespace(ctx, "ns-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(orphaned) != 1 || orphaned[0] != "QmOnlyA" {
		t.Fatalf("orphaned = %v, want only QmOnlyA (QmShared is still held by ns-b)", orphaned)
	}
	var n int
	// Only the backfill marker survives: it outlives every step that can still
	// fail and is removed by RemoveMarker as the delete's last act.
	if err := registry.db.QueryRow(`SELECT COUNT(*) FROM ipfs_cid_refs WHERE namespace = 'ns-a' AND kind != 'backfilled'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("ns-a still has %d rows (%v)", n, err)
	}
	if orphaned, err := refs.ReleaseNamespace(ctx, "ns-empty"); err != nil || len(orphaned) != 0 {
		t.Fatalf("empty namespace: %v, %v", orphaned, err)
	}
}
