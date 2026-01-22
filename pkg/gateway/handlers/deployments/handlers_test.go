package deployments

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"go.uber.org/zap"
)

// createMinimalTarball creates a minimal valid .tar.gz file for testing
func createMinimalTarball(t *testing.T) *bytes.Buffer {
	buf := &bytes.Buffer{}
	gzw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gzw)

	// Add a simple index.html file
	content := []byte("<html><body>Test</body></html>")
	header := &tar.Header{
		Name: "index.html",
		Mode: 0644,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("Failed to write tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("Failed to write tar content: %v", err)
	}

	tw.Close()
	gzw.Close()
	return buf
}

// TestStaticHandler_Upload tests uploading a static site tarball to IPFS
func TestStaticHandler_Upload(t *testing.T) {
	// Create mock IPFS client
	mockIPFS := &mockIPFSClient{
		AddDirectoryFunc: func(ctx context.Context, dirPath string) (*ipfs.AddResponse, error) {
			return &ipfs.AddResponse{Cid: "QmTestCID123456789"}, nil
		},
	}

	// Create mock RQLite client with basic implementations
	mockDB := &mockRQLiteClient{
		QueryFunc: func(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
			// For dns_nodes query, return mock active node
			if strings.Contains(query, "dns_nodes") {
				// Use reflection to set the slice
				destValue := reflect.ValueOf(dest)
				if destValue.Kind() == reflect.Ptr {
					sliceValue := destValue.Elem()
					if sliceValue.Kind() == reflect.Slice {
						// Create one element
						elemType := sliceValue.Type().Elem()
						newElem := reflect.New(elemType).Elem()
						// Set ID field
						idField := newElem.FieldByName("ID")
						if idField.IsValid() && idField.CanSet() {
							idField.SetString("node-test123")
						}
						// Append to slice
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

	// Create port allocator and home node manager with mock DB
	portAlloc := deployments.NewPortAllocator(mockDB, zap.NewNop())
	homeNodeMgr := deployments.NewHomeNodeManager(mockDB, portAlloc, zap.NewNop())

	// Create handler
	service := &DeploymentService{
		db:              mockDB,
		homeNodeManager: homeNodeMgr,
		portAllocator:   portAlloc,
		logger:          zap.NewNop(),
	}
	handler := NewStaticDeploymentHandler(service, mockIPFS, zap.NewNop())

	// Create a valid minimal tarball
	tarballBuf := createMinimalTarball(t)

	// Create multipart form with tarball
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add name field
	writer.WriteField("name", "test-app")

	// Add namespace field
	writer.WriteField("namespace", "test-namespace")

	// Add tarball file
	part, err := writer.CreateFormFile("tarball", "app.tar.gz")
	if err != nil {
		t.Fatalf("Failed to create form file: %v", err)
	}
	part.Write(tarballBuf.Bytes())

	writer.Close()

	// Create request
	req := httptest.NewRequest("POST", "/v1/deployments/static/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "test-namespace")
	req = req.WithContext(ctx)

	// Create response recorder
	rr := httptest.NewRecorder()

	// Call handler
	handler.HandleUpload(rr, req)

	// Check response
	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Errorf("Expected status 200 or 201, got %d", rr.Code)
		t.Logf("Response body: %s", rr.Body.String())
	}
}

// TestStaticHandler_Upload_InvalidTarball tests that malformed tarballs are rejected
func TestStaticHandler_Upload_InvalidTarball(t *testing.T) {
	// Create mock IPFS client
	mockIPFS := &mockIPFSClient{}

	// Create mock RQLite client
	mockDB := &mockRQLiteClient{}

	// Create port allocator and home node manager with mock DB
	portAlloc := deployments.NewPortAllocator(mockDB, zap.NewNop())
	homeNodeMgr := deployments.NewHomeNodeManager(mockDB, portAlloc, zap.NewNop())

	// Create handler
	service := &DeploymentService{
		db:              mockDB,
		homeNodeManager: homeNodeMgr,
		portAllocator:   portAlloc,
		logger:          zap.NewNop(),
	}
	handler := NewStaticDeploymentHandler(service, mockIPFS, zap.NewNop())

	// Create request without tarball field
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("name", "test-app")
	writer.Close()

	req := httptest.NewRequest("POST", "/v1/deployments/static/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "test-namespace")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	// Call handler
	handler.HandleUpload(rr, req)

	// Should return error (400 or 500)
	if rr.Code == http.StatusOK || rr.Code == http.StatusCreated {
		t.Errorf("Expected error status, got %d", rr.Code)
	}
}

// TestStaticHandler_Serve tests serving static files from IPFS
func TestStaticHandler_Serve(t *testing.T) {
	testContent := "<html><body>Test</body></html>"

	// Create mock IPFS client that returns test content
	mockIPFS := &mockIPFSClient{
		GetFunc: func(ctx context.Context, path, ipfsAPIURL string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(testContent)), nil
		},
	}

	// Create mock RQLite client
	mockDB := &mockRQLiteClient{}

	// Create port allocator and home node manager with mock DB
	portAlloc := deployments.NewPortAllocator(mockDB, zap.NewNop())
	homeNodeMgr := deployments.NewHomeNodeManager(mockDB, portAlloc, zap.NewNop())

	// Create handler
	service := &DeploymentService{
		db:              mockDB,
		homeNodeManager: homeNodeMgr,
		portAllocator:   portAlloc,
		logger:          zap.NewNop(),
	}
	handler := NewStaticDeploymentHandler(service, mockIPFS, zap.NewNop())

	// Create test deployment
	deployment := &deployments.Deployment{
		ID:         "test-id",
		ContentCID: "QmTestCID",
		Type:       deployments.DeploymentTypeStatic,
		Status:     deployments.DeploymentStatusActive,
		Name:       "test-app",
		Namespace:  "test-namespace",
	}

	// Create request
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "test-app.orama.network"

	rr := httptest.NewRecorder()

	// Call handler
	handler.HandleServe(rr, req, deployment)

	// Check response
	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	// Check content
	body := rr.Body.String()
	if body != testContent {
		t.Errorf("Expected %q, got %q", testContent, body)
	}
}

// TestStaticHandler_Serve_CSS tests that CSS files get correct Content-Type
func TestStaticHandler_Serve_CSS(t *testing.T) {
	testContent := "body { color: red; }"

	mockIPFS := &mockIPFSClient{
		GetFunc: func(ctx context.Context, path, ipfsAPIURL string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(testContent)), nil
		},
	}

	mockDB := &mockRQLiteClient{}

	service := &DeploymentService{
		db:     mockDB,
		logger: zap.NewNop(),
	}
	handler := NewStaticDeploymentHandler(service, mockIPFS, zap.NewNop())

	deployment := &deployments.Deployment{
		ID:         "test-id",
		ContentCID: "QmTestCID",
		Type:       deployments.DeploymentTypeStatic,
		Status:     deployments.DeploymentStatusActive,
		Name:       "test-app",
		Namespace:  "test-namespace",
	}

	req := httptest.NewRequest("GET", "/style.css", nil)
	req.Host = "test-app.orama.network"

	rr := httptest.NewRecorder()

	handler.HandleServe(rr, req, deployment)

	// Check Content-Type header
	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/css") {
		t.Errorf("Expected Content-Type to contain 'text/css', got %q", contentType)
	}
}

// TestStaticHandler_Serve_JS tests that JavaScript files get correct Content-Type
func TestStaticHandler_Serve_JS(t *testing.T) {
	testContent := "console.log('test');"

	mockIPFS := &mockIPFSClient{
		GetFunc: func(ctx context.Context, path, ipfsAPIURL string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(testContent)), nil
		},
	}

	mockDB := &mockRQLiteClient{}

	service := &DeploymentService{
		db:     mockDB,
		logger: zap.NewNop(),
	}
	handler := NewStaticDeploymentHandler(service, mockIPFS, zap.NewNop())

	deployment := &deployments.Deployment{
		ID:         "test-id",
		ContentCID: "QmTestCID",
		Type:       deployments.DeploymentTypeStatic,
		Status:     deployments.DeploymentStatusActive,
		Name:       "test-app",
		Namespace:  "test-namespace",
	}

	req := httptest.NewRequest("GET", "/app.js", nil)
	req.Host = "test-app.orama.network"

	rr := httptest.NewRecorder()

	handler.HandleServe(rr, req, deployment)

	// Check Content-Type header
	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/javascript") {
		t.Errorf("Expected Content-Type to contain 'application/javascript', got %q", contentType)
	}
}

// TestStaticHandler_Serve_SPAFallback tests that unknown paths fall back to index.html
func TestStaticHandler_Serve_SPAFallback(t *testing.T) {
	indexContent := "<html><body>SPA</body></html>"
	callCount := 0

	mockIPFS := &mockIPFSClient{
		GetFunc: func(ctx context.Context, path, ipfsAPIURL string) (io.ReadCloser, error) {
			callCount++
			// First call: return error for /unknown-route
			// Second call: return index.html
			if callCount == 1 {
				return nil, io.EOF // Simulate file not found
			}
			return io.NopCloser(strings.NewReader(indexContent)), nil
		},
	}

	mockDB := &mockRQLiteClient{}

	service := &DeploymentService{
		db:     mockDB,
		logger: zap.NewNop(),
	}
	handler := NewStaticDeploymentHandler(service, mockIPFS, zap.NewNop())

	deployment := &deployments.Deployment{
		ID:         "test-id",
		ContentCID: "QmTestCID",
		Type:       deployments.DeploymentTypeStatic,
		Status:     deployments.DeploymentStatusActive,
		Name:       "test-app",
		Namespace:  "test-namespace",
	}

	req := httptest.NewRequest("GET", "/unknown-route", nil)
	req.Host = "test-app.orama.network"

	rr := httptest.NewRecorder()

	handler.HandleServe(rr, req, deployment)

	// Should return index.html content
	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if body != indexContent {
		t.Errorf("Expected index.html content, got %q", body)
	}

	// Verify IPFS was called twice (first for route, then for index.html)
	if callCount < 2 {
		t.Errorf("Expected at least 2 IPFS calls for SPA fallback, got %d", callCount)
	}
}

// TestListHandler_AllDeployments tests listing all deployments for a namespace
func TestListHandler_AllDeployments(t *testing.T) {
	mockDB := &mockRQLiteClient{
		QueryFunc: func(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
			// The handler uses a local deploymentRow struct type, not deployments.Deployment
			// So we just return nil and let the test verify basic flow
			return nil
		},
	}

	// Create port allocator and home node manager with mock DB
	portAlloc := deployments.NewPortAllocator(mockDB, zap.NewNop())
	homeNodeMgr := deployments.NewHomeNodeManager(mockDB, portAlloc, zap.NewNop())

	service := &DeploymentService{
		db:              mockDB,
		homeNodeManager: homeNodeMgr,
		portAllocator:   portAlloc,
		logger:          zap.NewNop(),
	}
	handler := NewListHandler(service, zap.NewNop())

	req := httptest.NewRequest("GET", "/v1/deployments/list", nil)
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "test-namespace")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	handler.HandleList(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
		t.Logf("Response body: %s", rr.Body.String())
	}

	// Check that response is valid JSON
	body := rr.Body.String()
	if !strings.Contains(body, "namespace") || !strings.Contains(body, "deployments") {
		t.Errorf("Expected response to contain namespace and deployments fields, got: %s", body)
	}
}
