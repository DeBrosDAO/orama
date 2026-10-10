package storage

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

// failingRegistry fails the Nth query that mentions substr.
type failingRegistry struct {
	*sqliteDB
	substr string
	armed  atomic.Bool
}

func (f *failingRegistry) Query(ctx context.Context, dest any, q string, args ...any) error {
	if f.armed.Load() && strings.Contains(q, f.substr) {
		f.armed.Store(false)
		return errors.New("registry timed out")
	}
	return f.sqliteDB.Query(ctx, dest, q, args...)
}

// Bug: the recount after the delete failed, the rows were already gone, and the
// retry found nothing to release: those CIDs' pins leaked for good.
//
// Mutation check: delete before recounting again and this fails.
func TestReleaseNamespace_failedRecountLeavesTheRowsForARetry(t *testing.T) {
	ctx := context.Background()
	inner := registrySchema(t)
	reg := &failingRegistry{sqliteDB: inner, substr: "holders > 0 AND cid IN"}
	refs := NewCIDRefs(reg)
	for _, cid := range []string{"QmA", "QmB", "QmShared"} {
		if err := refs.Register(ctx, cid, "ns-a", KindStorage); err != nil {
			t.Fatal(err)
		}
	}
	if err := refs.Register(ctx, "QmShared", "ns-b", KindStorage); err != nil {
		t.Fatal(err)
	}

	reg.armed.Store(true)
	first, err := refs.ReleaseNamespace(ctx, "ns-a")
	if err == nil {
		t.Fatal("the failed recount was not reported")
	}
	if len(first) != 0 {
		t.Fatalf("orphans %v were claimed from a batch whose recount failed", first)
	}
	var held int
	if err := inner.db.QueryRow(`SELECT COUNT(*) FROM ipfs_cid_refs WHERE namespace = 'ns-a'`).Scan(&held); err != nil || held != 3 {
		t.Fatalf("ns-a rows = %d (%v); the batch must stay for the retry", held, err)
	}

	rest, err := refs.ReleaseNamespace(ctx, "ns-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 2 {
		t.Fatalf("the retry found %v, want QmA and QmB (QmShared is held by ns-b)", rest)
	}
}

// A tombstoned row no longer counts as a reference, so a concurrent release of
// the same CID by another namespace sees it as gone.
func TestCount_ignoresTombstonedRows(t *testing.T) {
	ctx := context.Background()
	reg := registrySchema(t)
	refs := NewCIDRefs(reg)
	if err := refs.Register(ctx, "QmA", "ns-a", KindStorage); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.db.Exec(`UPDATE ipfs_cid_refs SET holders = 0`); err != nil {
		t.Fatal(err)
	}
	if n, err := refs.Count(ctx, "QmA"); err != nil || n != 0 {
		t.Fatalf("count = %d, %v", n, err)
	}
	// A registration that arrives meanwhile revives the row.
	if err := refs.Register(ctx, "QmA", "ns-a", KindStorage); err != nil {
		t.Fatal(err)
	}
	if n, _ := refs.Count(ctx, "QmA"); n != 1 {
		t.Fatalf("count after re-registering = %d, want 1", n)
	}
}

// Item 3: which namespaces CheckReady waits for.
func TestCheckReady_waitsOnlyForNamespacesThatCanHoldContent(t *testing.T) {
	registry := registrySchema(t)
	refs := NewCIDRefs(registry)
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO namespaces (id, name) VALUES (1, 'default'), (2, 'ready-ns'), (3, 'failed-create'), (4, 'once-ready-now-failed'), (5, 'going-away'), (6, 'still-provisioning'), (7, 'never-provisioned'), (8, 'serving-without-ready-at')`,
		`INSERT INTO namespace_clusters (namespace_id, status, ready_at) VALUES
		   (2, 'ready', datetime('now')), (3, 'failed', NULL), (4, 'failed', datetime('now')),
		   (5, 'deprovisioning', datetime('now')), (6, 'provisioning', NULL), (8, 'ready', NULL)`,
	} {
		if _, err := registry.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	err := refs.CheckReady(ctx, "")
	var missing *NotBackfilledError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v", err)
	}
	got := map[string]bool{}
	for _, n := range missing.Namespaces {
		got[n] = true
	}
	// never-provisioned has no cluster (its provisioning never started) and no
	// gateway, so it is not waited for (stagenet, 2026-09-30: four such
	// namespaces blocked every unpin). serving-without-ready-at is ready but its
	// ready_at write was lost: it is serving, so it is waited for (review).
	want := map[string]bool{"default": true, "ready-ns": true, "once-ready-now-failed": true, "serving-without-ready-at": true}
	if len(got) != len(want) {
		t.Fatalf("waiting for %v, want %v", got, want)
	}
	for n := range want {
		if !got[n] {
			t.Fatalf("not waiting for %s: %v", n, got)
		}
	}
}

// TOCTOU: a registrant that lands after the count and before the unpin took
// effect must not lose its pin.
//
// Mutation check: drop the recount and re-pin from UnpinUnreferenced and every
// one of these fails.
func interleavedRegistrant(t *testing.T, refs *CIDRefs, mock *mockIPFSClient) {
	t.Helper()
	mock.pinStatus = &ipfs.PinStatus{Cid: sharedCID, Name: "keep.bin", ReplicationMax: 3}
	mock.onUnpin = func() {
		if err := refs.Register(context.Background(), sharedCID, "ns-late", KindStorage); err != nil {
			t.Error(err)
		}
	}
}

func TestUnpinIfLastRef_aRegistrantBetweenCountAndUnpinKeepsItsPin(t *testing.T) {
	ctx := context.Background()
	registry := registrySchema(t)
	refs := NewCIDRefs(registry)
	if err := refs.Register(ctx, sharedCID, "ns-a", KindDeployment); err != nil {
		t.Fatal(err)
	}
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	interleavedRegistrant(t, refs, mock)
	if err := UnpinIfLastRef(ctx, refs, mock, sharedCID, "ns-a", KindDeployment); err != nil {
		t.Fatal(err)
	}
	if mock.unpinCalls != 1 || mock.pinCalls != 1 {
		t.Fatalf("unpins %d, pins %d; the pin must be put back once", mock.unpinCalls, mock.pinCalls)
	}
}

func TestUnpinHandler_aRegistrantBetweenCountAndUnpinKeepsItsPin(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	pinAs(t, a, "ns-a", sharedCID)
	markBackfilled(t, registry, "ns-a")
	interleavedRegistrant(t, a.refs, mock)
	pinsBefore := mock.pinCalls

	rec := unpinAs(a, "ns-a", sharedCID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if body := decodeBody(t, rec); body["shared"] != true || body["evicted"] != "shared" {
		t.Fatalf("answer %v; the content became shared during the unpin", body)
	}
	if mock.pinCalls != pinsBefore+1 {
		t.Fatal("the pin was not put back")
	}
}

func TestApplyDeferred_aRegistrantBetweenCountAndUnpinKeepsItsPin(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	h := gatewayFor(t, mock, namespaceSchema(t), registry)
	interleavedRegistrant(t, h.refs, mock)
	h.applyDeferred(context.Background(), map[string]ClusterPinner{sharedCID: mock})
	if mock.unpinCalls != 1 || mock.pinCalls != 1 {
		t.Fatalf("unpins %d, pins %d", mock.unpinCalls, mock.pinCalls)
	}
}

func TestUnpinUnreferenced_edgeCases(t *testing.T) {
	ctx := context.Background()
	t.Run("nobody registered: the pin stays removed", func(t *testing.T) {
		refs := NewCIDRefs(registrySchema(t))
		mock := &mockIPFSClient{pinStatus: &ipfs.PinStatus{Name: "n", ReplicationMax: 3}}
		out, err := refs.UnpinUnreferenced(ctx, mock, sharedCID)
		if err != nil || out.Restored || mock.pinCalls != 0 || mock.unpinCalls != 1 {
			t.Fatalf("out %+v err %v pins %d unpins %d", out, err, mock.pinCalls, mock.unpinCalls)
		}
	})
	t.Run("already unpinned", func(t *testing.T) {
		refs := NewCIDRefs(registrySchema(t))
		mock := &mockIPFSClient{unpinErr: errors.New("x not part of the pinset")}
		out, err := refs.UnpinUnreferenced(ctx, mock, sharedCID)
		if err != nil || !out.AlreadyUnpinned {
			t.Fatalf("out %+v err %v", out, err)
		}
	})
	t.Run("an unreadable pin is not removed blind", func(t *testing.T) {
		refs := NewCIDRefs(registrySchema(t))
		mock := &mockIPFSClient{pinStatErr: errors.New("cluster unreachable")}
		if _, err := refs.UnpinUnreferenced(ctx, mock, sharedCID); err == nil || mock.unpinCalls != 0 {
			t.Fatalf("err %v unpins %d", err, mock.unpinCalls)
		}
	})
	t.Run("a recount that fails puts the pin back", func(t *testing.T) {
		reg := &failingRegistry{sqliteDB: registrySchema(t), substr: "holders > 0"}
		refs := NewCIDRefs(reg)
		mock := &mockIPFSClient{pinStatus: &ipfs.PinStatus{Name: "n", ReplicationMax: 3}, pinResp: &ipfs.PinResponse{}}
		mock.onUnpin = func() { reg.armed.Store(true) }
		if _, err := refs.UnpinUnreferenced(ctx, mock, sharedCID); err == nil || mock.pinCalls != 1 {
			t.Fatalf("err %v pins %d; want the pin restored and the failure reported", err, mock.pinCalls)
		}
	})
	t.Run("a failed restore is reported", func(t *testing.T) {
		refs := NewCIDRefs(registrySchema(t))
		mock := &mockIPFSClient{pinStatus: &ipfs.PinStatus{ReplicationMax: 3}, pinErr: errors.New("cluster down")}
		interleavedRegistrant(t, refs, mock)
		mock.pinStatus = &ipfs.PinStatus{ReplicationMax: 3}
		if _, err := refs.UnpinUnreferenced(ctx, mock, sharedCID); err == nil {
			t.Fatal("a pin that could not be put back was reported as success")
		}
	})
}
