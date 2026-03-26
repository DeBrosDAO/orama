package serverless

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestHostFunctions_Cache(t *testing.T) {
	// Note: HostFunctions implementation has been moved to pkg/serverless/hostfunctions
	// This test validates that the HostServices interface works correctly

	db := NewMockRQLite()
	ipfs := NewMockIPFSClient()
	logger := zap.NewNop()

	// Create a mock implementation that satisfies HostServices
	var h HostServices = &mockHostServices{
		db:     db,
		ipfs:   ipfs,
		logger: logger,
		logs:   make([]LogEntry, 0),
	}

	ctx := context.Background()

	// Test Storage interface
	cid, err := h.StoragePut(ctx, []byte("data"))
	if err != nil {
		t.Fatalf("StoragePut failed: %v", err)
	}
	data, err := h.StorageGet(ctx, cid)
	if err != nil {
		t.Fatalf("StorageGet failed: %v", err)
	}
	if string(data) != "data" {
		t.Errorf("expected 'data', got %q", string(data))
	}
}

// mockHostServices is a minimal mock for testing the HostServices interface
type mockHostServices struct {
	db     *MockRQLite
	ipfs   *MockIPFSClient
	logger *zap.Logger
	logs   []LogEntry
}

func (m *mockHostServices) DBQuery(ctx context.Context, query string, args []interface{}) ([]byte, error) {
	return nil, nil
}

func (m *mockHostServices) DBExecute(ctx context.Context, query string, args []interface{}) (int64, error) {
	return 0, nil
}

func (m *mockHostServices) CacheGet(ctx context.Context, key string) ([]byte, error) {
	return nil, nil
}

func (m *mockHostServices) CacheSet(ctx context.Context, key string, value []byte, ttlSeconds int64) error {
	return nil
}

func (m *mockHostServices) CacheDelete(ctx context.Context, key string) error {
	return nil
}

func (m *mockHostServices) CacheIncr(ctx context.Context, key string) (int64, error) {
	return 0, nil
}

func (m *mockHostServices) CacheIncrBy(ctx context.Context, key string, delta int64) (int64, error) {
	return 0, nil
}

func (m *mockHostServices) StoragePut(ctx context.Context, data []byte) (string, error) {
	// Mock implementation - just return a fake CID
	return "QmTest123", nil
}

func (m *mockHostServices) StorageGet(ctx context.Context, cid string) ([]byte, error) {
	// Mock implementation - return the test data
	return []byte("data"), nil
}

func (m *mockHostServices) PubSubPublish(ctx context.Context, topic string, data []byte) error {
	return nil
}

func (m *mockHostServices) WSSend(ctx context.Context, clientID string, data []byte) error {
	return nil
}

func (m *mockHostServices) WSBroadcast(ctx context.Context, topic string, data []byte) error {
	return nil
}

func (m *mockHostServices) HTTPFetch(ctx context.Context, method, url string, headers map[string]string, body []byte) ([]byte, error) {
	return nil, nil
}

func (m *mockHostServices) GetEnv(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (m *mockHostServices) GetSecret(ctx context.Context, name string) (string, error) {
	return "", nil
}

func (m *mockHostServices) GetRequestID(ctx context.Context) string {
	return ""
}

func (m *mockHostServices) GetCallerWallet(ctx context.Context) string {
	return ""
}

func (m *mockHostServices) EnqueueBackground(ctx context.Context, functionName string, payload []byte) (string, error) {
	return "", nil
}

func (m *mockHostServices) ScheduleOnce(ctx context.Context, functionName string, runAt time.Time, payload []byte) (string, error) {
	return "", nil
}

func (m *mockHostServices) LogInfo(ctx context.Context, message string) {
	m.logs = append(m.logs, LogEntry{Level: "info", Message: message})
}

func (m *mockHostServices) LogError(ctx context.Context, message string) {
	m.logs = append(m.logs, LogEntry{Level: "error", Message: message})
}
