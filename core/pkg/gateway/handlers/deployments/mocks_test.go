package deployments

import (
	"context"
	"database/sql"
	"io"

	"github.com/DeBrosOfficial/network/pkg/deployments"
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

// mockProcessManager implements a mock process manager for testing
type mockProcessManager struct {
	StartFunc   func(ctx context.Context, deployment *deployments.Deployment, workDir string) error
	StopFunc    func(ctx context.Context, deployment *deployments.Deployment) error
	RestartFunc func(ctx context.Context, deployment *deployments.Deployment) error
	StatusFunc  func(ctx context.Context, deployment *deployments.Deployment) (string, error)
	GetLogsFunc func(ctx context.Context, deployment *deployments.Deployment, lines int, follow bool) ([]byte, error)
}

func (m *mockProcessManager) Start(ctx context.Context, deployment *deployments.Deployment, workDir string) error {
	if m.StartFunc != nil {
		return m.StartFunc(ctx, deployment, workDir)
	}
	return nil
}

func (m *mockProcessManager) Stop(ctx context.Context, deployment *deployments.Deployment) error {
	if m.StopFunc != nil {
		return m.StopFunc(ctx, deployment)
	}
	return nil
}

func (m *mockProcessManager) Restart(ctx context.Context, deployment *deployments.Deployment) error {
	if m.RestartFunc != nil {
		return m.RestartFunc(ctx, deployment)
	}
	return nil
}

func (m *mockProcessManager) Status(ctx context.Context, deployment *deployments.Deployment) (string, error) {
	if m.StatusFunc != nil {
		return m.StatusFunc(ctx, deployment)
	}
	return "active", nil
}

func (m *mockProcessManager) GetLogs(ctx context.Context, deployment *deployments.Deployment, lines int, follow bool) ([]byte, error) {
	if m.GetLogsFunc != nil {
		return m.GetLogsFunc(ctx, deployment, lines, follow)
	}
	return []byte("mock logs"), nil
}

// mockHomeNodeManager implements a mock home node manager for testing
type mockHomeNodeManager struct {
	AssignHomeNodeFunc func(ctx context.Context, namespace string) (string, error)
	GetHomeNodeFunc    func(ctx context.Context, namespace string) (string, error)
}

func (m *mockHomeNodeManager) AssignHomeNode(ctx context.Context, namespace string) (string, error) {
	if m.AssignHomeNodeFunc != nil {
		return m.AssignHomeNodeFunc(ctx, namespace)
	}
	return "node-test123", nil
}

func (m *mockHomeNodeManager) GetHomeNode(ctx context.Context, namespace string) (string, error) {
	if m.GetHomeNodeFunc != nil {
		return m.GetHomeNodeFunc(ctx, namespace)
	}
	return "node-test123", nil
}

// mockPortAllocator implements a mock port allocator for testing
type mockPortAllocator struct {
	AllocatePortFunc func(ctx context.Context, nodeID, deploymentID string) (int, error)
	ReleasePortFunc  func(ctx context.Context, nodeID string, port int) error
}

func (m *mockPortAllocator) AllocatePort(ctx context.Context, nodeID, deploymentID string) (int, error) {
	if m.AllocatePortFunc != nil {
		return m.AllocatePortFunc(ctx, nodeID, deploymentID)
	}
	return 10100, nil
}

func (m *mockPortAllocator) ReleasePort(ctx context.Context, nodeID string, port int) error {
	if m.ReleasePortFunc != nil {
		return m.ReleasePortFunc(ctx, nodeID, port)
	}
	return nil
}
