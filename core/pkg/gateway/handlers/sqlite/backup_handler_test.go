package sqlite

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"go.uber.org/zap"
)

func TestBackupDatabase_forwardsToTheHomeNode(t *testing.T) {
	h, _, hits := forwardFixture(t, "127.0.0.1")
	rr := httptest.NewRecorder()
	NewBackupHandler(h, nil, zap.NewNop()).BackupDatabase(rr, queryRequest(`{"database_name":"proofdb"}`, false))

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if *hits != 1 {
		t.Fatalf("home was asked %d times", *hits)
	}
}

func TestListBackups_readsTheColumnsTheMigrationCreated(t *testing.T) {
	var listed string
	h := NewSQLiteHandler(&mockRQLiteClient{
		QueryFunc: func(_ context.Context, dest interface{}, query string, _ ...interface{}) error {
			if strings.Contains(query, "namespace_sqlite_databases") {
				fillStringField(dest, "ID", "db-1")
				return nil
			}
			listed = query
			if strings.Contains(query, "backed_up_at") {
				t.Errorf("backup history was queried by backed_up_at, which migration 006 did not create")
			}
			return nil
		},
	}, nil, zap.NewNop(), t.TempDir(), "local-peer")

	req := httptest.NewRequest(http.MethodGet, "/v1/db/sqlite/backups?database_name=proofdb", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "stagenetproof"))
	rr := httptest.NewRecorder()
	NewBackupHandler(h, nil, zap.NewNop()).ListBackups(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(listed, "created_at") {
		t.Fatalf("history query %q does not read created_at", listed)
	}
}

func TestBackupDatabase_recordsTheColumnsTheMigrationCreated(t *testing.T) {
	var inserted string
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "stagenetproof"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stagenetproof", "proofdb.db"), []byte("sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewSQLiteHandler(&mockRQLiteClient{
		QueryFunc: func(_ context.Context, dest interface{}, query string, _ ...interface{}) error {
			if strings.Contains(query, "namespace_sqlite_databases") {
				fillStringField(dest, "ID", "db-1")
			}
			return nil
		},
		ExecFunc: func(_ context.Context, query string, _ ...interface{}) (sql.Result, error) {
			if strings.Contains(query, "namespace_sqlite_backups") {
				inserted = query
			}
			return nil, nil
		},
	}, nil, zap.NewNop(), dir, "local-peer")

	req := queryRequest(`{"database_name":"proofdb"}`, false)
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.JWT, &auth.JWTClaims{Sub: "0xowner"}))
	rr := httptest.NewRecorder()
	NewBackupHandler(h, stubIPFS{}, zap.NewNop()).BackupDatabase(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	for _, col := range []string{"id", "database_id", "backup_cid", "size_bytes", "backup_type", "created_at", "created_by"} {
		if !strings.Contains(inserted, col) {
			t.Errorf("insert %q is missing %s", inserted, col)
		}
	}
	if strings.Contains(inserted, "backed_up_at") {
		t.Errorf("insert writes backed_up_at, which is not a column of namespace_sqlite_backups: %s", inserted)
	}
}

// stubIPFS answers Add and nothing else. Backup only adds the file.
type stubIPFS struct{}

func (stubIPFS) Add(_ context.Context, r io.Reader, name string) (*ipfs.AddResponse, error) {
	_, _ = io.Copy(io.Discard, r)
	return &ipfs.AddResponse{Cid: "bafyproof", Name: name, Size: 6}, nil
}
func (s stubIPFS) AddLocal(ctx context.Context, r io.Reader, name string) (*ipfs.AddResponse, error) {
	return s.Add(ctx, r, name)
}
func (stubIPFS) AddDirectory(context.Context, string) (*ipfs.AddResponse, error) { return nil, nil }
func (stubIPFS) Pin(context.Context, string, string, int) (*ipfs.PinResponse, error) {
	return nil, nil
}
func (stubIPFS) PinStatus(context.Context, string) (*ipfs.PinStatus, error) { return nil, nil }
func (stubIPFS) Get(context.Context, string, string) (io.ReadCloser, error) { return nil, nil }
func (stubIPFS) GetStored(context.Context, string, string) (io.ReadCloser, error) {
	return nil, nil
}
func (stubIPFS) Unpin(context.Context, string) error             { return nil }
func (stubIPFS) EvictLocal(context.Context, string) (int, error) { return 0, nil }
func (stubIPFS) Health(context.Context) error                    { return nil }
func (stubIPFS) GetPeerCount(context.Context) (int, error)       { return 0, nil }
func (stubIPFS) Close(context.Context) error                     { return nil }
