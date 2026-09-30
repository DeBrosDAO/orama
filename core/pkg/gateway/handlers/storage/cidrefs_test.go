package storage

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

const sharedCID = "QmSharedAcrossNamespaces"

// registrySchema is the cluster registry's ipfs_cid_refs, from the migration.
// One connection, so statements are serialised the way rqlite serialises them.
func registrySchema(t *testing.T) *sqliteDB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	// namespaces is the registry's list of live namespaces, which the index
	// readiness check reads.
	if _, err := db.Exec(`CREATE TABLE namespaces (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE);
		CREATE TABLE namespace_clusters (namespace_id INTEGER NOT NULL UNIQUE, status TEXT NOT NULL, ready_at TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"064_ipfs_cid_refs.sql", "065_ipfs_cid_refs_holders.sql"} {
		ddl, err := os.ReadFile("../../../../migrations/" + f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(ddl)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
	return &sqliteDB{db: db}
}

// namespaceSchema is one namespace's own RQLite: its ownership rows and its
// deployments, and nothing of any other namespace's.
func namespaceSchema(t *testing.T) *sqliteDB {
	t.Helper()
	db := ownershipSchema(t)
	db.db.SetMaxOpenConns(1)
	ddl, err := os.ReadFile("../../../../migrations/007_deployments.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(string(ddl)); err != nil {
		t.Fatalf("apply 007: %v", err)
	}
	return db
}

// gatewayFor is a namespace gateway: its own database, the shared registry.
func gatewayFor(t *testing.T, ipfsClient IPFSClient, ns *sqliteDB, registry *sqliteDB) *Handlers {
	t.Helper()
	return New(ipfsClient, newTestLogger(), Config{IPFSReplicationFactor: 3}, ns, registry)
}

func pinAs(t *testing.T, h *Handlers, ns, cid string) {
	t.Helper()
	if err := h.recordCIDOwnership(context.Background(), cid, ns, "f", ns, 1); err != nil {
		t.Fatal(err)
	}
	body := strings.NewReader(`{"cid":"` + cid + `"}`)
	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/storage/pin", body), ns)
	rec := httptest.NewRecorder()
	h.PinHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pin as %s: status %d: %s", ns, rec.Code, rec.Body.String())
	}
}

func unpinAs(h *Handlers, ns, cid string) *httptest.ResponseRecorder {
	req := withNamespace(httptest.NewRequest(http.MethodDelete, "/v1/storage/unpin/"+cid, nil), ns)
	rec := httptest.NewRecorder()
	h.UnpinHandler(rec, req)
	return rec
}

func refsOf(t *testing.T, registry *sqliteDB, cid string) int {
	t.Helper()
	var n int
	if err := registry.db.QueryRow(`SELECT COUNT(*) FROM ipfs_cid_refs WHERE cid = ?`, cid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Bug: two namespaces hold the same CID, each in its OWN RQLite. Unpinning in
// one counted rows in that one database, saw no other reference, and removed
// the cluster pin (and, with ?immediate=true, the blob) the other tenant still
// needed. e2e: TestUnpin_sharedContentKept.
//
// Mutation check: count in h.db again (CIDInUseByOtherNamespace) and the first
// unpin removes the pin.
func TestUnpin_sharedAcrossNamespaces_lastReferenceRemovesPin(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	b := gatewayFor(t, mock, namespaceSchema(t), registry)
	pinAs(t, a, "ns-a", sharedCID)
	pinAs(t, b, "ns-b", sharedCID)

	rec := unpinAs(a, "ns-a", sharedCID)
	if rec.Code != http.StatusOK {
		t.Fatalf("unpin as ns-a: %d %s", rec.Code, rec.Body.String())
	}
	if mock.unpinCalls != 0 {
		t.Fatalf("ns-a's unpin removed the cluster pin ns-b still holds")
	}
	if body := decodeBody(t, rec); body["shared"] != true {
		t.Errorf("shared = %v, want true", body["shared"])
	}

	rec = unpinAs(b, "ns-b", sharedCID)
	if rec.Code != http.StatusOK {
		t.Fatalf("unpin as ns-b: %d %s", rec.Code, rec.Body.String())
	}
	if mock.unpinCalls != 1 {
		t.Fatalf("the last reference's unpin removed the pin %d times, want 1", mock.unpinCalls)
	}
	if n := refsOf(t, registry, sharedCID); n != 0 {
		t.Errorf("%d references remain after both unpinned", n)
	}
}

// A namespace's own deployment serving the CID is a reference too.
func TestUnpin_otherNamespaceDeploymentKeepsPin(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	if err := NewCIDRefs(registry).Register(context.Background(), sharedCID, "ns-b", KindDeployment); err != nil {
		t.Fatal(err)
	}
	pinAs(t, a, "ns-a", sharedCID)

	if rec := unpinAs(a, "ns-a", sharedCID); rec.Code != http.StatusOK {
		t.Fatalf("unpin: %d", rec.Code)
	}
	if mock.unpinCalls != 0 {
		t.Fatal("unpin removed a pin another namespace's deployment serves")
	}
}

// Two namespaces unpinning at the same moment: never a pin removed while a
// reference is still recorded, and never a pin left behind with none.
func TestUnpin_concurrentReleases_exactlyLastRemoves(t *testing.T) {
	for i := 0; i < 25; i++ {
		registry := registrySchema(t)
		var removedWithRefs atomic.Int32
		mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
		a := gatewayFor(t, mock, namespaceSchema(t), registry)
		b := gatewayFor(t, mock, namespaceSchema(t), registry)
		pinAs(t, a, "ns-a", sharedCID)
		pinAs(t, b, "ns-b", sharedCID)
		mock.onUnpin = func() {
			if refsOf(t, registry, sharedCID) != 0 {
				removedWithRefs.Add(1)
			}
		}

		var wg sync.WaitGroup
		for _, c := range []struct {
			h  *Handlers
			ns string
		}{{a, "ns-a"}, {b, "ns-b"}} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if rec := unpinAs(c.h, c.ns, sharedCID); rec.Code != http.StatusOK {
					t.Errorf("unpin as %s: %d", c.ns, rec.Code)
				}
			}()
		}
		wg.Wait()

		if removedWithRefs.Load() != 0 {
			t.Fatalf("round %d: the cluster pin was removed while a reference was still recorded", i)
		}
		if mock.unpinCalls < 1 {
			t.Fatalf("round %d: both namespaces unpinned and the pin is still there", i)
		}
	}
}

// While the index is still being filled, "last reference" cannot be decided.
func TestUnpin_refuses_whileIndexIsBuilding(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	pinAs(t, a, "ns-a", sharedCID)
	a.refs.pending.Store(true)

	rec := unpinAs(a, "ns-a", sharedCID)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if mock.unpinCalls != 0 {
		t.Fatal("unpin removed a pin without knowing the cluster-wide reference count")
	}
}

// A registry that cannot be read must leave the pin alone.
func TestUnpin_registryError_leavesPin(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	pinAs(t, a, "ns-a", sharedCID)
	if _, err := registry.db.Exec(`DROP TABLE ipfs_cid_refs`); err != nil {
		t.Fatal(err)
	}

	rec := unpinAs(a, "ns-a", sharedCID)
	if rec.Code != http.StatusServiceUnavailable || mock.unpinCalls != 0 {
		t.Fatalf("status %d, unpins %d; want a retryable 503 and the pin kept", rec.Code, mock.unpinCalls)
	}
}

// A registry that answers the readiness check but fails the release still
// leaves the pin alone; the namespace is logically unpinned.
func TestUnpin_releaseError_leavesPin(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	pinAs(t, a, "ns-a", sharedCID)
	if _, err := registry.db.Exec(`CREATE TRIGGER refuse_delete BEFORE DELETE ON ipfs_cid_refs BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	rec := unpinAs(a, "ns-a", sharedCID)
	if rec.Code != http.StatusOK || mock.unpinCalls != 0 {
		t.Fatalf("status %d, unpins %d; want 200 and the pin kept", rec.Code, mock.unpinCalls)
	}
	if body := decodeBody(t, rec); body["evicted"] != "skipped" {
		t.Errorf("evicted = %v, want skipped", body["evicted"])
	}
}

// Pinning something registers the reference; a registry that rejects it fails
// the pin instead of leaving a pin no unpin will count.
func TestPin_registersReference(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	pinAs(t, a, "ns-a", sharedCID)
	if n := refsOf(t, registry, sharedCID); n != 1 {
		t.Fatalf("references = %d, want 1", n)
	}
	pinAs(t, a, "ns-a", sharedCID)
	if n := refsOf(t, registry, sharedCID); n != 1 {
		t.Fatalf("a second pin by the same namespace made %d references, want 1", n)
	}
}

func TestPin_registryFailure_failsThePin(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	if err := a.recordCIDOwnership(context.Background(), sharedCID, "ns-a", "f", "ns-a", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.db.Exec(`DROP TABLE ipfs_cid_refs`); err != nil {
		t.Fatal(err)
	}
	req := withNamespace(httptest.NewRequest(http.MethodPost, "/v1/storage/pin", strings.NewReader(`{"cid":"`+sharedCID+`"}`)), "ns-a")
	rec := httptest.NewRecorder()
	a.PinHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// The backfill loads what a namespace already holds (content pinned before the
// index existed), once.
func TestBackfill_loadsExistingContentOnce(t *testing.T) {
	registry := registrySchema(t)
	nsDB := namespaceSchema(t)
	h := gatewayFor(t, &mockIPFSClient{}, nsDB, registry)
	ctx := context.Background()

	if _, err := nsDB.db.Exec(`INSERT INTO ipfs_content_ownership (id, cid, namespace, is_pinned, uploaded_at, uploaded_by)
		VALUES ('1', 'QmLegacyPinned', 'ns-a', 1, datetime('now'), 'ns-a'), ('2', 'QmNotPinned', 'ns-a', 0, datetime('now'), 'ns-a')`); err != nil {
		t.Fatal(err)
	}
	if _, err := nsDB.db.Exec(`INSERT INTO deployments (id, namespace, name, type, content_cid, build_cid, deployed_by) VALUES ('d1', 'ns-a', 'app', 'nodejs', 'QmContent', 'QmBuild', 'x')`); err != nil {
		t.Fatal(err)
	}
	if err := h.backfillCIDRefs(ctx, "ns-a"); err != nil {
		t.Fatal(err)
	}
	for cid, want := range map[string]int{"QmLegacyPinned": 1, "QmNotPinned": 0, "QmContent": 1, "QmBuild": 1} {
		if got := refsOf(t, registry, cid); got != want {
			t.Errorf("references to %s = %d, want %d", cid, got, want)
		}
	}

	// A row a tenant writes into its own tables afterwards never reaches the
	// index: the backfill does not run again.
	if _, err := nsDB.db.Exec(`INSERT INTO ipfs_content_ownership (id, cid, namespace, is_pinned, uploaded_at, uploaded_by)
		VALUES ('3', 'QmVictimsCID', 'ns-a', 1, datetime('now'), 'ns-a')`); err != nil {
		t.Fatal(err)
	}
	if err := h.backfillCIDRefs(ctx, "ns-a"); err != nil {
		t.Fatal(err)
	}
	if got := refsOf(t, registry, "QmVictimsCID"); got != 0 {
		t.Fatalf("a forged row written after the backfill became %d references", got)
	}
}

// A namespace over the bound is not loaded in part: a partial index would call
// live content unreferenced.
func TestBackfill_refusesANamespaceOverTheBound(t *testing.T) {
	registry := registrySchema(t)
	nsDB := namespaceSchema(t)
	h := gatewayFor(t, &mockIPFSClient{}, nsDB, registry)
	defer func(old int) { maxBackfillRefs = old }(maxBackfillRefs)
	maxBackfillRefs = 10
	if _, err := nsDB.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < ?)
		INSERT INTO ipfs_content_ownership (id, cid, namespace, is_pinned, uploaded_at, uploaded_by)
		SELECT 'id'||i, 'Qm'||i, 'ns-a', 1, datetime('now'), 'x' FROM n`, maxBackfillRefs+1); err != nil {
		t.Fatal(err)
	}
	if err := h.backfillCIDRefs(t.Context(), "ns-a"); err == nil {
		t.Fatal("a namespace over the bound was loaded")
	}
	var n int
	if err := registry.db.QueryRow(`SELECT COUNT(*) FROM ipfs_cid_refs`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the index holds %d rows (%v) after a refused backfill", n, err)
	}
}

// Until the backfill succeeds unpins are refused; StartCIDRefBackfill flips the
// gateway to ready when it has.
func TestStartCIDRefBackfill_gatesUntilLoaded(t *testing.T) {
	registry := registrySchema(t)
	h := gatewayFor(t, &mockIPFSClient{}, namespaceSchema(t), registry)
	h.StartCIDRefBackfill(t.Context(), "ns-a")
	deadline := time.Now().Add(5 * time.Second)
	for h.refs.CheckReady(t.Context(), "") != nil {
		if time.Now().After(deadline) {
			t.Fatal("the backfill of an empty namespace never completed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A namespace with many CIDs is inserted in chunks, none lost at a boundary.
func TestSyncCIDRefs_chunkBoundaries(t *testing.T) {
	registry := registrySchema(t)
	h := gatewayFor(t, &mockIPFSClient{}, namespaceSchema(t), registry)
	var cids []string
	for i := 0; i < refInsertChunk*2+1; i++ {
		cids = append(cids, "Qm"+strings.Repeat("x", 3)+string(rune('A'+i/26))+string(rune('a'+i%26)))
	}
	if err := h.insertRefs(context.Background(), cids, "ns-a", KindStorage); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := registry.db.QueryRow(`SELECT COUNT(*) FROM ipfs_cid_refs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(cids) {
		t.Fatalf("stored %d references, want %d", n, len(cids))
	}
	if err := h.insertRefs(context.Background(), nil, "ns-a", KindStorage); err != nil {
		t.Fatalf("empty insert: %v", err)
	}
}

// addServingNamespaces registers namespaces with a ready cluster: ones that
// hold content and that the reference index waits for.
func addServingNamespaces(registry *sqliteDB, names ...string) error {
	for _, n := range names {
		res, err := registry.db.Exec(`INSERT INTO namespaces (name) VALUES (?)`, n)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := registry.db.Exec(`INSERT INTO namespace_clusters (namespace_id, status, ready_at) VALUES (?, 'ready', datetime('now'))`, id); err != nil {
			return err
		}
	}
	return nil
}
