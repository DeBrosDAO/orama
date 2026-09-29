package backup

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

func TestRestoreHandler_refuses_up_front_when_rqlite_cannot_batch(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	seedSource(t, src)
	pub, priv := ownerKey(t)
	dst := newRig(t, destRoot, testNamespace, true)
	dst.db.batchErr = fmt.Errorf("rqlite.Batch: %w", rqlite.ErrNoNativeConnection)
	rec := do(dst.h.RestoreHandler, http.MethodPost, restoreBody(t, backupFrom(t, src, pub), priv, destRoot))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "nothing was written") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertNothingWritten(t, dst)
}

func TestRestoreHandler_runs_one_at_a_time(t *testing.T) {
	dst := newRig(t, destRoot, testNamespace, true)
	release, ok := dst.h.begin(nil)
	if !ok {
		t.Fatal("the free slot was refused")
	}
	rec := do(dst.h.RestoreHandler, http.MethodPost, nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("restore while busy: %d", rec.Code)
	}
	pub, _ := ownerKey(t)
	body, _ := json.Marshal(BackupRequest{PublicKey: hex.EncodeToString(pub[:])})
	if rec := do(dst.h.BackupHandler, http.MethodPost, body); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("backup while busy: %d", rec.Code)
	}
	release()
	if rec := do(dst.h.BackupHandler, http.MethodPost, body); rec.Code != http.StatusOK {
		t.Fatalf("backup after release: %d %s", rec.Code, rec.Body)
	}
	assertNothingWritten(t, dst)
}

func TestRestoreHandler_over_the_destination_quota_writes_nothing(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	src.db.used = []int64{50}
	pub, priv := ownerKey(t)
	dst := newRig(t, destRoot, testNamespace, true)
	dst.db.rows["namespace_quotas"] = []map[string]any{{"max_storage_bytes": float64(100)}}
	rec := do(dst.h.RestoreHandler, http.MethodPost, restoreBody(t, backupFrom(t, src, pub), priv, destRoot))
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "nothing was written") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertNothingWritten(t, dst)
}

// A request whose header understates the snapshot is caught against the
// restored table before anything is pinned.
func TestRestoreHandler_restored_table_over_quota_pins_nothing(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	seedSource(t, src)
	src.db.used = []int64{10}
	pub, priv := ownerKey(t)
	dst := newRig(t, destRoot, testNamespace, true)
	dst.db.rows["namespace_quotas"] = []map[string]any{{"max_storage_bytes": float64(100)}}
	dst.db.used = []int64{60}
	rec := do(dst.h.RestoreHandler, http.MethodPost, restoreBody(t, backupFrom(t, src, pub), priv, destRoot))
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "no CID was pinned") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(dst.pins.sorted()) != 0 {
		t.Fatalf("pinned %v", dst.pins.sorted())
	}
}

func TestRestoreHandler_keeps_the_destination_quota(t *testing.T) {
	for name, tc := range map[string]struct {
		rows []map[string]any
		want string
	}{
		"quota set": {rows: []map[string]any{{"max_storage_bytes": float64(1 << 40)}}, want: "INSERT INTO namespace_quotas"},
		"no quota":  {want: "DELETE FROM namespace_quotas"},
	} {
		src := newRig(t, sourceRoot, testNamespace, true)
		pub, priv := ownerKey(t)
		dst := newRig(t, destRoot, testNamespace, true)
		if tc.rows != nil {
			dst.db.rows["namespace_quotas"] = tc.rows
		}
		rec := do(dst.h.RestoreHandler, http.MethodPost, restoreBody(t, backupFrom(t, src, pub), priv, destRoot))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
		first := dst.db.batches[0][0]
		if !strings.HasPrefix(first.SQL, tc.want) || first.Args[0] != testNamespace {
			t.Fatalf("%s: first write %q %v", name, first.SQL, first.Args)
		}
	}
}

func TestRestoreHandler_refuses_secrets_sealed_for_another_namespace(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	seedSource(t, src)
	pub, priv := ownerKey(t)
	dst := newRig(t, destRoot, testNamespace, true)
	body := restoreBodyFor(t, backupFrom(t, src, pub), priv, destRoot, "other")
	rec := do(dst.h.RestoreHandler, http.MethodPost, body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "restore-key") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertNothingWritten(t, dst)
}

func TestPinAll_keeps_at_most_pinConcurrency_in_flight(t *testing.T) {
	dst := newRig(t, destRoot, testNamespace, true)
	cids := make([]string, 3*pinConcurrency)
	for i := range cids {
		cids[i] = fmt.Sprintf("cid-%d", i)
	}
	dst.pins.entered = make(chan struct{}, len(cids))
	dst.pins.gate = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- dst.h.pinAll(context.Background(), cids) }()
	for range pinConcurrency {
		<-dst.pins.entered
	}
	close(dst.pins.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if dst.pins.peak != pinConcurrency || len(dst.pins.sorted()) != len(cids) {
		t.Fatalf("peak %d, pinned %d", dst.pins.peak, len(dst.pins.sorted()))
	}
}

func TestPinAll_stops_at_the_deadline(t *testing.T) {
	dst := newRig(t, destRoot, testNamespace, true)
	dst.pins.hang = true
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := dst.h.pinAll(ctx, []string{testCIDa, testCIDb}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hung pins: %v", err)
	}
}

func TestBackupHandler_hides_internal_errors(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	src.db.queryErr = errors.New("dial tcp 10.0.0.1:5001: user:hunter2")
	pub, _ := ownerKey(t)
	body, _ := json.Marshal(BackupRequest{PublicKey: hex.EncodeToString(pub[:])})
	rec := do(src.h.BackupHandler, http.MethodPost, body)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "hunter2") ||
		strings.Contains(rec.Body.String(), "10.0.0.1") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestBackupHandler_refuses_a_namespace_over_the_limit(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	src.snap.backupErr = fmt.Errorf("%w: database is over %d bytes", nsbackup.ErrTooLarge, nsbackup.MaxRQLiteBytes)
	pub, _ := ownerKey(t)
	body, _ := json.Marshal(BackupRequest{PublicKey: hex.EncodeToString(pub[:])})
	if rec := do(src.h.BackupHandler, http.MethodPost, body); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
