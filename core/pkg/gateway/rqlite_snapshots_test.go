package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/nsbackup"
)

// followerRQLite answers POST /db/load with a redirect to the leader, and
// counts any request that arrives at the leader path.
func followerRQLite(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var atLeader atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/leader/db/load" {
			atLeader.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/leader/db/load", http.StatusMovedPermanently)
	}))
	t.Cleanup(srv.Close)
	return srv, &atLeader
}

func TestRQLiteSnapshotsLoad_refuses_a_redirect(t *testing.T) {
	srv, atLeader := followerRQLite(t)
	err := newRQLiteSnapshots(srv.URL).Load(context.Background(), []byte("SQLite format 3\x00"))
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("redirected load: %v", err)
	}
	if atLeader.Load() != 0 {
		t.Fatal("the redirect was followed, as a GET without the database")
	}
}

func TestRQLiteSnapshotsLoad_sends_the_database_with_credentials(t *testing.T) {
	db := []byte("SQLite format 3\x00rows")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != rqliteLoadPath || !ok || user != "u" || pass != "p" || !bytes.Equal(body, db) {
			http.Error(w, "bad load", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	base := strings.Replace(srv.URL, "http://", "http://u:p@", 1)
	if err := newRQLiteSnapshots(base).Load(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestRQLiteSnapshotsBackup_refuses_a_database_over_the_cap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 20))
	}))
	defer srv.Close()
	s := newRQLiteSnapshots(srv.URL)
	s.maxBytes = 10
	if _, err := s.Backup(context.Background()); !errors.Is(err, nsbackup.ErrTooLarge) {
		t.Fatalf("oversized backup: %v", err)
	}
	s.maxBytes = 20
	if db, err := s.Backup(context.Background()); err != nil || len(db) != 20 {
		t.Fatalf("at the cap: %d %v", len(db), err)
	}
}

func TestRQLiteSnapshots_errors_do_not_carry_the_password(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := strings.Replace(srv.URL, "http://", "http://u:hunter2@", 1)
	srv.Close()
	s := newRQLiteSnapshots(base)
	_, berr := s.Backup(context.Background())
	lerr := s.Load(context.Background(), []byte("x"))
	for _, err := range []error{berr, lerr} {
		if err == nil || strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("error leaks or is missing: %v", err)
		}
	}
}

func TestRQLiteImportHandler_refuses_a_redirect_and_hides_the_dsn(t *testing.T) {
	srv, atLeader := followerRQLite(t)
	g := newTestGateway(t)
	g.cfg = &Config{RQLiteDSN: strings.Replace(srv.URL, "http://", "http://u:hunter2@", 1)}
	req := httptest.NewRequest(http.MethodPost, "/v1/rqlite/import", strings.NewReader("SQLite format 3\x00"))
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	g.rqliteImportHandler(rec, req)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if atLeader.Load() != 0 {
		t.Fatal("the redirect was followed")
	}
}
