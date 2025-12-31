package serverless

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// MockRegistry is a mock implementation of FunctionRegistry
type MockRegistry struct {
	mu        sync.RWMutex
	functions map[string]*Function
	wasm      map[string][]byte
}

func NewMockRegistry() *MockRegistry {
	return &MockRegistry{
		functions: make(map[string]*Function),
		wasm:      make(map[string][]byte),
	}
}

func (m *MockRegistry) Register(ctx context.Context, fn *FunctionDefinition, wasmBytes []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := fn.Namespace + "/" + fn.Name
	wasmCID := "cid-" + id
	m.functions[id] = &Function{
		ID:             id,
		Name:           fn.Name,
		Namespace:      fn.Namespace,
		WASMCID:        wasmCID,
		MemoryLimitMB:  fn.MemoryLimitMB,
		TimeoutSeconds: fn.TimeoutSeconds,
		Status:         FunctionStatusActive,
	}
	m.wasm[wasmCID] = wasmBytes
	return nil
}

func (m *MockRegistry) Get(ctx context.Context, namespace, name string, version int) (*Function, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	fn, ok := m.functions[namespace+"/"+name]
	if !ok {
		return nil, ErrFunctionNotFound
	}
	return fn, nil
}

func (m *MockRegistry) List(ctx context.Context, namespace string) ([]*Function, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var res []*Function
	for _, fn := range m.functions {
		if fn.Namespace == namespace {
			res = append(res, fn)
		}
	}
	return res, nil
}

func (m *MockRegistry) Delete(ctx context.Context, namespace, name string, version int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.functions, namespace+"/"+name)
	return nil
}

func (m *MockRegistry) GetWASMBytes(ctx context.Context, wasmCID string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.wasm[wasmCID]
	if !ok {
		return nil, ErrFunctionNotFound
	}
	return data, nil
}

// MockHostServices is a mock implementation of HostServices
type MockHostServices struct {
	mu      sync.RWMutex
	cache   map[string][]byte
	storage map[string][]byte
	logs    []string
}

func NewMockHostServices() *MockHostServices {
	return &MockHostServices{
		cache:   make(map[string][]byte),
		storage: make(map[string][]byte),
	}
}

func (m *MockHostServices) DBQuery(ctx context.Context, query string, args []interface{}) ([]byte, error) {
	return []byte("[]"), nil
}

func (m *MockHostServices) DBExecute(ctx context.Context, query string, args []interface{}) (int64, error) {
	return 0, nil
}

func (m *MockHostServices) CacheGet(ctx context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cache[key], nil
}

func (m *MockHostServices) CacheSet(ctx context.Context, key string, value []byte, ttl int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache[key] = value
	return nil
}

func (m *MockHostServices) CacheDelete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, key)
	return nil
}

func (m *MockHostServices) StoragePut(ctx context.Context, data []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cid := "cid-" + time.Now().String()
	m.storage[cid] = data
	return cid, nil
}

func (m *MockHostServices) StorageGet(ctx context.Context, cid string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.storage[cid], nil
}

func (m *MockHostServices) PubSubPublish(ctx context.Context, topic string, data []byte) error {
	return nil
}

func (m *MockHostServices) WSSend(ctx context.Context, clientID string, data []byte) error {
	return nil
}

func (m *MockHostServices) WSBroadcast(ctx context.Context, topic string, data []byte) error {
	return nil
}

func (m *MockHostServices) HTTPFetch(ctx context.Context, method, url string, headers map[string]string, body []byte) ([]byte, error) {
	return nil, nil
}

func (m *MockHostServices) GetEnv(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (m *MockHostServices) GetSecret(ctx context.Context, name string) (string, error) {
	return "", nil
}

func (m *MockHostServices) GetRequestID(ctx context.Context) string {
	return "req-123"
}

func (m *MockHostServices) GetCallerWallet(ctx context.Context) string {
	return "wallet-123"
}

func (m *MockHostServices) EnqueueBackground(ctx context.Context, functionName string, payload []byte) (string, error) {
	return "job-123", nil
}

func (m *MockHostServices) ScheduleOnce(ctx context.Context, functionName string, runAt time.Time, payload []byte) (string, error) {
	return "timer-123", nil
}

func (m *MockHostServices) LogInfo(ctx context.Context, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, "INFO: "+message)
}

func (m *MockHostServices) LogError(ctx context.Context, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, "ERROR: "+message)
}

// MockIPFSClient is a mock for ipfs.IPFSClient
type MockIPFSClient struct {
	data map[string][]byte
}

func NewMockIPFSClient() *MockIPFSClient {
	return &MockIPFSClient{data: make(map[string][]byte)}
}

func (m *MockIPFSClient) Add(ctx context.Context, reader io.Reader, filename string) (*ipfs.AddResponse, error) {
	data, _ := io.ReadAll(reader)
	cid := "cid-" + filename
	m.data[cid] = data
	return &ipfs.AddResponse{Cid: cid, Name: filename}, nil
}

func (m *MockIPFSClient) Pin(ctx context.Context, cid string, name string, replicationFactor int) (*ipfs.PinResponse, error) {
	return &ipfs.PinResponse{Cid: cid, Name: name}, nil
}

func (m *MockIPFSClient) PinStatus(ctx context.Context, cid string) (*ipfs.PinStatus, error) {
	return &ipfs.PinStatus{Cid: cid, Status: "pinned"}, nil
}

func (m *MockIPFSClient) Get(ctx context.Context, cid, apiURL string) (io.ReadCloser, error) {
	data, ok := m.data[cid]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return io.NopCloser(strings.NewReader(string(data))), nil
}

func (m *MockIPFSClient) Unpin(ctx context.Context, cid string) error   { return nil }
func (m *MockIPFSClient) Health(ctx context.Context) error              { return nil }
func (m *MockIPFSClient) GetPeerCount(ctx context.Context) (int, error) { return 1, nil }
func (m *MockIPFSClient) Close(ctx context.Context) error               { return nil }

// MockRQLite is a mock implementation of rqlite.Client
type MockRQLite struct {
	mu     sync.Mutex
	tables map[string][]map[string]any
}

func NewMockRQLite() *MockRQLite {
	return &MockRQLite{
		tables: make(map[string][]map[string]any),
	}
}

func (m *MockRQLite) Query(ctx context.Context, dest any, query string, args ...any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Very limited mock query logic for scanning into structs
	if strings.Contains(query, "FROM functions") {
		rows := m.tables["functions"]
		filtered := rows
		if strings.Contains(query, "namespace = ? AND name = ?") {
			ns := args[0].(string)
			name := args[1].(string)
			filtered = nil
			for _, r := range rows {
				if r["namespace"] == ns && r["name"] == name {
					filtered = append(filtered, r)
				}
			}
		}

		destVal := reflect.ValueOf(dest).Elem()
		if destVal.Kind() == reflect.Slice {
			elemType := destVal.Type().Elem()
			for _, r := range filtered {
				newElem := reflect.New(elemType).Elem()
				// This is a simplified mapping
				if f := newElem.FieldByName("ID"); f.IsValid() {
					f.SetString(r["id"].(string))
				}
				if f := newElem.FieldByName("Name"); f.IsValid() {
					f.SetString(r["name"].(string))
				}
				if f := newElem.FieldByName("Namespace"); f.IsValid() {
					f.SetString(r["namespace"].(string))
				}
				destVal.Set(reflect.Append(destVal, newElem))
			}
		}
	}
	return nil
}

func (m *MockRQLite) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &mockResult{}, nil
}

func (m *MockRQLite) FindBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...rqlite.FindOption) error {
	return nil
}
func (m *MockRQLite) FindOneBy(ctx context.Context, dest any, table string, criteria map[string]any, opts ...rqlite.FindOption) error {
	return nil
}
func (m *MockRQLite) Save(ctx context.Context, entity any) error   { return nil }
func (m *MockRQLite) Remove(ctx context.Context, entity any) error { return nil }
func (m *MockRQLite) Repository(table string) any                  { return nil }

func (m *MockRQLite) CreateQueryBuilder(table string) *rqlite.QueryBuilder {
	return nil // Should return a valid QueryBuilder if needed by tests
}

func (m *MockRQLite) Tx(ctx context.Context, fn func(tx rqlite.Tx) error) error {
	return nil
}

type mockResult struct{}

func (m *mockResult) LastInsertId() (int64, error) { return 1, nil }
func (m *mockResult) RowsAffected() (int64, error) { return 1, nil }

// MockOlricClient is a mock for olriclib.Client
type MockOlricClient struct {
	dmaps map[string]*MockDMap
}

func NewMockOlricClient() *MockOlricClient {
	return &MockOlricClient{dmaps: make(map[string]*MockDMap)}
}

func (m *MockOlricClient) NewDMap(name string) (any, error) {
	if dm, ok := m.dmaps[name]; ok {
		return dm, nil
	}
	dm := &MockDMap{data: make(map[string][]byte)}
	m.dmaps[name] = dm
	return dm, nil
}

func (m *MockOlricClient) Close(ctx context.Context) error                     { return nil }
func (m *MockOlricClient) Stats(ctx context.Context, s string) ([]byte, error) { return nil, nil }
func (m *MockOlricClient) Ping(ctx context.Context, s string) error            { return nil }
func (m *MockOlricClient) RoutingTable(ctx context.Context) (map[uint64][]string, error) {
	return nil, nil
}

// MockDMap is a mock for olriclib.DMap
type MockDMap struct {
	data map[string][]byte
}

func (m *MockDMap) Get(ctx context.Context, key string) (any, error) {
	val, ok := m.data[key]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &MockGetResponse{val: val}, nil
}

func (m *MockDMap) Put(ctx context.Context, key string, value any) error {
	switch v := value.(type) {
	case []byte:
		m.data[key] = v
	case string:
		m.data[key] = []byte(v)
	}
	return nil
}

func (m *MockDMap) Delete(ctx context.Context, key string) (bool, error) {
	_, ok := m.data[key]
	delete(m.data, key)
	return ok, nil
}

type MockGetResponse struct {
	val []byte
}

func (m *MockGetResponse) Byte() ([]byte, error)   { return m.val, nil }
func (m *MockGetResponse) String() (string, error) { return string(m.val), nil }
