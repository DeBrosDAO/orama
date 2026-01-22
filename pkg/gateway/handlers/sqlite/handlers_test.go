package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// Mock implementations

type mockRQLiteClient struct {
	QueryFunc    func(ctx context.Context, dest interface{}, query string, args ...interface{}) error
	ExecFunc     func(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	FindByFunc   func(ctx context.Context, dest interface{}, table string, criteria map[string]interface{}, opts ...rqlite.FindOption) error
	FindOneFunc  func(ctx context.Context, dest interface{}, table string, criteria map[string]interface{}, opts ...rqlite.FindOption) error
	SaveFunc     func(ctx context.Context, entity interface{}) error
	RemoveFunc   func(ctx context.Context, entity interface{}) error
	RepoFunc     func(table string) interface{}
	CreateQBFunc func(table string) *rqlite.QueryBuilder
	TxFunc       func(ctx context.Context, fn func(tx rqlite.Tx) error) error
}

func (m *mockRQLiteClient) Query(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	if m.QueryFunc != nil {
		return m.QueryFunc(ctx, dest, query, args...)
	}
	return nil
}

func (m *mockRQLiteClient) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if m.ExecFunc != nil {
		return m.ExecFunc(ctx, query, args...)
	}
	return nil, nil
}

func (m *mockRQLiteClient) FindBy(ctx context.Context, dest interface{}, table string, criteria map[string]interface{}, opts ...rqlite.FindOption) error {
	if m.FindByFunc != nil {
		return m.FindByFunc(ctx, dest, table, criteria, opts...)
	}
	return nil
}

func (m *mockRQLiteClient) FindOneBy(ctx context.Context, dest interface{}, table string, criteria map[string]interface{}, opts ...rqlite.FindOption) error {
	if m.FindOneFunc != nil {
		return m.FindOneFunc(ctx, dest, table, criteria, opts...)
	}
	return nil
}

func (m *mockRQLiteClient) Save(ctx context.Context, entity interface{}) error {
	if m.SaveFunc != nil {
		return m.SaveFunc(ctx, entity)
	}
	return nil
}

func (m *mockRQLiteClient) Remove(ctx context.Context, entity interface{}) error {
	if m.RemoveFunc != nil {
		return m.RemoveFunc(ctx, entity)
	}
	return nil
}

func (m *mockRQLiteClient) Repository(table string) interface{} {
	if m.RepoFunc != nil {
		return m.RepoFunc(table)
	}
	return nil
}

func (m *mockRQLiteClient) CreateQueryBuilder(table string) *rqlite.QueryBuilder {
	if m.CreateQBFunc != nil {
		return m.CreateQBFunc(table)
	}
	return nil
}

func (m *mockRQLiteClient) Tx(ctx context.Context, fn func(tx rqlite.Tx) error) error {
	if m.TxFunc != nil {
		return m.TxFunc(ctx, fn)
	}
	return nil
}

type mockIPFSClient struct {
	AddFunc          func(ctx context.Context, r io.Reader, filename string) (*ipfs.AddResponse, error)
	AddDirectoryFunc func(ctx context.Context, dirPath string) (*ipfs.AddResponse, error)
	GetFunc          func(ctx context.Context, path, ipfsAPIURL string) (io.ReadCloser, error)
	PinFunc          func(ctx context.Context, cid, name string, replicationFactor int) (*ipfs.PinResponse, error)
	PinStatusFunc    func(ctx context.Context, cid string) (*ipfs.PinStatus, error)
	UnpinFunc        func(ctx context.Context, cid string) error
	HealthFunc       func(ctx context.Context) error
	GetPeerFunc      func(ctx context.Context) (int, error)
	CloseFunc        func(ctx context.Context) error
}

func (m *mockIPFSClient) Add(ctx context.Context, r io.Reader, filename string) (*ipfs.AddResponse, error) {
	if m.AddFunc != nil {
		return m.AddFunc(ctx, r, filename)
	}
	return &ipfs.AddResponse{Cid: "QmTestCID123456789"}, nil
}

func (m *mockIPFSClient) AddDirectory(ctx context.Context, dirPath string) (*ipfs.AddResponse, error) {
	if m.AddDirectoryFunc != nil {
		return m.AddDirectoryFunc(ctx, dirPath)
	}
	return &ipfs.AddResponse{Cid: "QmTestDirCID123456789"}, nil
}

func (m *mockIPFSClient) Get(ctx context.Context, cid, ipfsAPIURL string) (io.ReadCloser, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, cid, ipfsAPIURL)
	}
	return io.NopCloser(nil), nil
}

func (m *mockIPFSClient) Pin(ctx context.Context, cid, name string, replicationFactor int) (*ipfs.PinResponse, error) {
	if m.PinFunc != nil {
		return m.PinFunc(ctx, cid, name, replicationFactor)
	}
	return &ipfs.PinResponse{}, nil
}

func (m *mockIPFSClient) PinStatus(ctx context.Context, cid string) (*ipfs.PinStatus, error) {
	if m.PinStatusFunc != nil {
		return m.PinStatusFunc(ctx, cid)
	}
	return &ipfs.PinStatus{}, nil
}

func (m *mockIPFSClient) Unpin(ctx context.Context, cid string) error {
	if m.UnpinFunc != nil {
		return m.UnpinFunc(ctx, cid)
	}
	return nil
}

func (m *mockIPFSClient) Health(ctx context.Context) error {
	if m.HealthFunc != nil {
		return m.HealthFunc(ctx)
	}
	return nil
}

func (m *mockIPFSClient) GetPeerCount(ctx context.Context) (int, error) {
	if m.GetPeerFunc != nil {
		return m.GetPeerFunc(ctx)
	}
	return 5, nil
}

func (m *mockIPFSClient) Close(ctx context.Context) error {
	if m.CloseFunc != nil {
		return m.CloseFunc(ctx)
	}
	return nil
}

// TestCreateDatabase_Success tests creating a new database
func TestCreateDatabase_Success(t *testing.T) {
	mockDB := &mockRQLiteClient{
		QueryFunc: func(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
			// For dns_nodes query, return mock active node
			if strings.Contains(query, "dns_nodes") {
				destValue := reflect.ValueOf(dest)
				if destValue.Kind() == reflect.Ptr {
					sliceValue := destValue.Elem()
					if sliceValue.Kind() == reflect.Slice {
						elemType := sliceValue.Type().Elem()
						newElem := reflect.New(elemType).Elem()
						idField := newElem.FieldByName("ID")
						if idField.IsValid() && idField.CanSet() {
							idField.SetString("node-test123")
						}
						sliceValue.Set(reflect.Append(sliceValue, newElem))
					}
				}
			}
			// For database check, return empty (database doesn't exist)
			if strings.Contains(query, "namespace_sqlite_databases") && strings.Contains(query, "SELECT") {
				// Return empty result
			}
			return nil
		},
		ExecFunc: func(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
			return nil, nil
		},
	}

	// Create temp directory for test database
	tmpDir := t.TempDir()

	portAlloc := deployments.NewPortAllocator(mockDB, zap.NewNop())
	homeNodeMgr := deployments.NewHomeNodeManager(mockDB, portAlloc, zap.NewNop())

	handler := NewSQLiteHandler(mockDB, homeNodeMgr, zap.NewNop())
	handler.basePath = tmpDir

	reqBody := map[string]string{
		"database_name": "test-db",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/v1/db/sqlite/create", bytes.NewReader(bodyBytes))
	ctx := context.WithValue(req.Context(), "namespace", "test-namespace")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	handler.CreateDatabase(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", rr.Code)
		t.Logf("Response: %s", rr.Body.String())
	}

	// Verify database file was created
	dbPath := filepath.Join(tmpDir, "test-namespace", "test-db.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("Expected database file to be created at %s", dbPath)
	}

	// Verify response
	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["database_name"] != "test-db" {
		t.Errorf("Expected database_name 'test-db', got %v", resp["database_name"])
	}
}

// TestCreateDatabase_DuplicateName tests that duplicate database names are rejected
func TestCreateDatabase_DuplicateName(t *testing.T) {
	mockDB := &mockRQLiteClient{
		QueryFunc: func(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
			// For dns_nodes query
			if strings.Contains(query, "dns_nodes") {
				destValue := reflect.ValueOf(dest)
				if destValue.Kind() == reflect.Ptr {
					sliceValue := destValue.Elem()
					if sliceValue.Kind() == reflect.Slice {
						elemType := sliceValue.Type().Elem()
						newElem := reflect.New(elemType).Elem()
						idField := newElem.FieldByName("ID")
						if idField.IsValid() && idField.CanSet() {
							idField.SetString("node-test123")
						}
						sliceValue.Set(reflect.Append(sliceValue, newElem))
					}
				}
			}
			// For database check, return existing database
			if strings.Contains(query, "namespace_sqlite_databases") && strings.Contains(query, "SELECT") {
				destValue := reflect.ValueOf(dest)
				if destValue.Kind() == reflect.Ptr {
					sliceValue := destValue.Elem()
					if sliceValue.Kind() == reflect.Slice {
						elemType := sliceValue.Type().Elem()
						newElem := reflect.New(elemType).Elem()
						// Set ID field to indicate existing database
						idField := newElem.FieldByName("ID")
						if idField.IsValid() && idField.CanSet() {
							idField.SetString("existing-db-id")
						}
						sliceValue.Set(reflect.Append(sliceValue, newElem))
					}
				}
			}
			return nil
		},
	}

	tmpDir := t.TempDir()

	portAlloc := deployments.NewPortAllocator(mockDB, zap.NewNop())
	homeNodeMgr := deployments.NewHomeNodeManager(mockDB, portAlloc, zap.NewNop())

	handler := NewSQLiteHandler(mockDB, homeNodeMgr, zap.NewNop())
	handler.basePath = tmpDir

	reqBody := map[string]string{
		"database_name": "test-db",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/v1/db/sqlite/create", bytes.NewReader(bodyBytes))
	ctx := context.WithValue(req.Context(), "namespace", "test-namespace")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	handler.CreateDatabase(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("Expected status 409 (Conflict), got %d", rr.Code)
	}
}

// TestCreateDatabase_InvalidName tests that invalid database names are rejected
func TestCreateDatabase_InvalidName(t *testing.T) {
	mockDB := &mockRQLiteClient{}
	tmpDir := t.TempDir()

	portAlloc := deployments.NewPortAllocator(mockDB, zap.NewNop())
	homeNodeMgr := deployments.NewHomeNodeManager(mockDB, portAlloc, zap.NewNop())

	handler := NewSQLiteHandler(mockDB, homeNodeMgr, zap.NewNop())
	handler.basePath = tmpDir

	invalidNames := []string{
		"test db",       // Space
		"test@db",       // Special char
		"test/db",       // Slash
		"",              // Empty
		strings.Repeat("a", 100), // Too long
	}

	for _, name := range invalidNames {
		reqBody := map[string]string{
			"database_name": name,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req := httptest.NewRequest("POST", "/v1/db/sqlite/create", bytes.NewReader(bodyBytes))
		ctx := context.WithValue(req.Context(), "namespace", "test-namespace")
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()

		handler.CreateDatabase(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400 for invalid name %q, got %d", name, rr.Code)
		}
	}
}

// TestListDatabases tests listing all databases for a namespace
func TestListDatabases(t *testing.T) {
	mockDB := &mockRQLiteClient{
		QueryFunc: func(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
			// Return empty list
			return nil
		},
	}

	portAlloc := deployments.NewPortAllocator(mockDB, zap.NewNop())
	homeNodeMgr := deployments.NewHomeNodeManager(mockDB, portAlloc, zap.NewNop())

	handler := NewSQLiteHandler(mockDB, homeNodeMgr, zap.NewNop())

	req := httptest.NewRequest("GET", "/v1/db/sqlite/list", nil)
	ctx := context.WithValue(req.Context(), "namespace", "test-namespace")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	handler.ListDatabases(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)

	if _, ok := resp["databases"]; !ok {
		t.Error("Expected 'databases' field in response")
	}

	if _, ok := resp["count"]; !ok {
		t.Error("Expected 'count' field in response")
	}
}

// TestBackupDatabase tests backing up a database to IPFS
func TestBackupDatabase(t *testing.T) {
	// Create a temporary database file
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a real SQLite database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY)")
	db.Close()

	mockDB := &mockRQLiteClient{
		QueryFunc: func(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
			// Mock database record lookup - return struct with file_path
			if strings.Contains(query, "namespace_sqlite_databases") {
				destValue := reflect.ValueOf(dest)
				if destValue.Kind() == reflect.Ptr {
					sliceValue := destValue.Elem()
					if sliceValue.Kind() == reflect.Slice {
						elemType := sliceValue.Type().Elem()
						newElem := reflect.New(elemType).Elem()

						// Set fields
						idField := newElem.FieldByName("ID")
						if idField.IsValid() && idField.CanSet() {
							idField.SetString("test-db-id")
						}

						filePathField := newElem.FieldByName("FilePath")
						if filePathField.IsValid() && filePathField.CanSet() {
							filePathField.SetString(dbPath)
						}

						sliceValue.Set(reflect.Append(sliceValue, newElem))
					}
				}
			}
			return nil
		},
		ExecFunc: func(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
			return nil, nil
		},
	}

	mockIPFS := &mockIPFSClient{
		AddFunc: func(ctx context.Context, r io.Reader, filename string) (*ipfs.AddResponse, error) {
			// Verify data is being uploaded
			data, _ := io.ReadAll(r)
			if len(data) == 0 {
				t.Error("Expected non-empty database file upload")
			}
			return &ipfs.AddResponse{Cid: "QmBackupCID123"}, nil
		},
	}

	portAlloc := deployments.NewPortAllocator(mockDB, zap.NewNop())
	homeNodeMgr := deployments.NewHomeNodeManager(mockDB, portAlloc, zap.NewNop())

	sqliteHandler := NewSQLiteHandler(mockDB, homeNodeMgr, zap.NewNop())

	backupHandler := NewBackupHandler(sqliteHandler, mockIPFS, zap.NewNop())

	reqBody := map[string]string{
		"database_name": "test-db",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/v1/db/sqlite/backup", bytes.NewReader(bodyBytes))
	ctx := context.WithValue(req.Context(), "namespace", "test-namespace")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	backupHandler.BackupDatabase(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
		t.Logf("Response: %s", rr.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)

	if resp["backup_cid"] != "QmBackupCID123" {
		t.Errorf("Expected backup_cid 'QmBackupCID123', got %v", resp["backup_cid"])
	}
}

// TestIsValidDatabaseName tests database name validation
func TestIsValidDatabaseName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"valid_db", true},
		{"valid-db", true},
		{"ValidDB123", true},
		{"test_db_123", true},
		{"test db", false},      // Space
		{"test@db", false},      // Special char
		{"test/db", false},      // Slash
		{"", false},             // Empty
		{strings.Repeat("a", 65), false}, // Too long
	}

	for _, tt := range tests {
		result := isValidDatabaseName(tt.name)
		if result != tt.valid {
			t.Errorf("isValidDatabaseName(%q) = %v, expected %v", tt.name, result, tt.valid)
		}
	}
}

// TestIsWriteQuery tests SQL query classification
func TestIsWriteQuery(t *testing.T) {
	tests := []struct {
		query   string
		isWrite bool
	}{
		{"SELECT * FROM users", false},
		{"INSERT INTO users VALUES (1, 'test')", true},
		{"UPDATE users SET name = 'test'", true},
		{"DELETE FROM users WHERE id = 1", true},
		{"CREATE TABLE test (id INT)", true},
		{"DROP TABLE test", true},
		{"ALTER TABLE test ADD COLUMN name TEXT", true},
		{"  insert into users values (1)", true}, // Case insensitive with whitespace
		{"select * from users", false},
	}

	for _, tt := range tests {
		result := isWriteQuery(tt.query)
		if result != tt.isWrite {
			t.Errorf("isWriteQuery(%q) = %v, expected %v", tt.query, result, tt.isWrite)
		}
	}
}
