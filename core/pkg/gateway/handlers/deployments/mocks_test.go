package deployments

import (
	"context"
	"database/sql"
	"io"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// mockIPFSClient implements a mock IPFS client for testing
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

func (m *mockIPFSClient) GetStored(ctx context.Context, cid string, ipfsAPIURL string) (io.ReadCloser, error) {
	return m.Get(ctx, cid, ipfsAPIURL)
}

func (m *mockIPFSClient) EvictLocal(ctx context.Context, cid string) (int, error) {
	return 0, nil
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

// mockRQLiteClient implements a mock RQLite client for testing
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

func (m *mockRQLiteClient) Batch(ctx context.Context, ops []rqlite.BatchOp) (*rqlite.BatchResult, error) {
	return &rqlite.BatchResult{Committed: true, Results: make([]rqlite.OpResult, len(ops))}, nil
}

func (m *mockRQLiteClient) BatchWithSeq(ctx context.Context, namespace string, ops []rqlite.BatchOp) (*rqlite.BatchResult, int64, error) {
	res, err := m.Batch(ctx, ops)
	return res, 1, err
}

func (m *mockRQLiteClient) BatchQuery(ctx context.Context, ops []rqlite.BatchOp) ([]rqlite.OpResult, error) {
	out := make([]rqlite.OpResult, len(ops))
	for i := range ops {
		out[i] = rqlite.OpResult{Kind: rqlite.BatchOpQuery}
	}
	return out, nil
}
