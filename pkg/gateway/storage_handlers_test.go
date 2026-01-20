package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/storage"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// mockIPFSClient is a mock implementation of ipfs.IPFSClient for testing
type mockIPFSClient struct {
	addFunc          func(ctx context.Context, reader io.Reader, name string) (*ipfs.AddResponse, error)
	pinFunc          func(ctx context.Context, cid string, name string, replicationFactor int) (*ipfs.PinResponse, error)
	pinStatusFunc    func(ctx context.Context, cid string) (*ipfs.PinStatus, error)
	getFunc          func(ctx context.Context, cid string, ipfsAPIURL string) (io.ReadCloser, error)
	unpinFunc        func(ctx context.Context, cid string) error
	getPeerCountFunc func(ctx context.Context) (int, error)
}

func (m *mockIPFSClient) Add(ctx context.Context, reader io.Reader, name string) (*ipfs.AddResponse, error) {
	if m.addFunc != nil {
		return m.addFunc(ctx, reader, name)
	}
	return &ipfs.AddResponse{Cid: "QmTest123", Name: name, Size: 100}, nil
}

func (m *mockIPFSClient) Pin(ctx context.Context, cid string, name string, replicationFactor int) (*ipfs.PinResponse, error) {
	if m.pinFunc != nil {
		return m.pinFunc(ctx, cid, name, replicationFactor)
	}
	return &ipfs.PinResponse{Cid: cid, Name: name}, nil
}

func (m *mockIPFSClient) PinStatus(ctx context.Context, cid string) (*ipfs.PinStatus, error) {
	if m.pinStatusFunc != nil {
		return m.pinStatusFunc(ctx, cid)
	}
	return &ipfs.PinStatus{
		Cid:               cid,
		Name:              "test",
		Status:            "pinned",
		ReplicationMin:    3,
		ReplicationMax:    3,
		ReplicationFactor: 3,
		Peers:             []string{"peer1", "peer2", "peer3"},
	}, nil
}

func (m *mockIPFSClient) Get(ctx context.Context, cid string, ipfsAPIURL string) (io.ReadCloser, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, cid, ipfsAPIURL)
	}
	return io.NopCloser(strings.NewReader("test content")), nil
}

func (m *mockIPFSClient) Unpin(ctx context.Context, cid string) error {
	if m.unpinFunc != nil {
		return m.unpinFunc(ctx, cid)
	}
	return nil
}

func (m *mockIPFSClient) Health(ctx context.Context) error {
	return nil
}

func (m *mockIPFSClient) GetPeerCount(ctx context.Context) (int, error) {
	if m.getPeerCountFunc != nil {
		return m.getPeerCountFunc(ctx)
	}
	return 3, nil
}

func (m *mockIPFSClient) Close(ctx context.Context) error {
	return nil
}

func newTestGatewayWithIPFS(t *testing.T, ipfsClient ipfs.IPFSClient) *Gateway {
	logger, err := logging.NewColoredLogger(logging.ComponentGeneral, true)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	cfg := &Config{
		ListenAddr:            ":6001",
		ClientNamespace:       "test",
		IPFSReplicationFactor: 3,
		IPFSEnableEncryption:  true,
		IPFSAPIURL:            "http://localhost:5001",
	}

	gw := &Gateway{
		logger: logger,
		cfg:    cfg,
	}

	if ipfsClient != nil {
		gw.ipfsClient = ipfsClient
		// Initialize storage handlers with the IPFS client
		gw.storageHandlers = storage.New(ipfsClient, logger, storage.Config{
			IPFSReplicationFactor: cfg.IPFSReplicationFactor,
			IPFSAPIURL:            cfg.IPFSAPIURL,
		})
	}

	return gw
}

func TestStorageUploadHandler_MissingIPFSClient(t *testing.T) {
	logger, err := logging.NewColoredLogger(logging.ComponentGeneral, true)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	// Create storage handlers with nil IPFS client
	handlers := storage.New(nil, logger, storage.Config{
		IPFSReplicationFactor: 3,
		IPFSAPIURL:            "http://localhost:5001",
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", nil)
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "test-ns")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handlers.UploadHandler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}
}

func TestStorageUploadHandler_MethodNotAllowed(t *testing.T) {
	gw := newTestGatewayWithIPFS(t, &mockIPFSClient{})

	req := httptest.NewRequest(http.MethodGet, "/v1/storage/upload", nil)
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "test-ns")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	gw.storageHandlers.UploadHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestStorageUploadHandler_MissingNamespace(t *testing.T) {
	gw := newTestGatewayWithIPFS(t, &mockIPFSClient{})

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", nil)
	w := httptest.NewRecorder()

	gw.storageHandlers.UploadHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestStorageUploadHandler_MultipartUpload(t *testing.T) {
	expectedCID := "QmTest456"
	expectedName := "test.txt"
	expectedSize := int64(200)

	mockClient := &mockIPFSClient{
		addFunc: func(ctx context.Context, reader io.Reader, name string) (*ipfs.AddResponse, error) {
			// Read and verify content
			data, _ := io.ReadAll(reader)
			if len(data) == 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return &ipfs.AddResponse{
				Cid:  expectedCID,
				Name: name,
				Size: expectedSize,
			}, nil
		},
	}

	gw := newTestGatewayWithIPFS(t, mockClient)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", expectedName)
	part.Write([]byte("test file content"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "test-ns")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	gw.storageHandlers.UploadHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp storage.StorageUploadResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Cid != expectedCID {
		t.Errorf("Expected CID %s, got %s", expectedCID, resp.Cid)
	}
	if resp.Name != expectedName {
		t.Errorf("Expected name %s, got %s", expectedName, resp.Name)
	}
	if resp.Size != expectedSize {
		t.Errorf("Expected size %d, got %d", expectedSize, resp.Size)
	}
}

func TestStorageUploadHandler_JSONUpload(t *testing.T) {
	expectedCID := "QmTest789"
	expectedName := "test.json"
	testData := []byte("test json data")
	base64Data := base64.StdEncoding.EncodeToString(testData)

	mockClient := &mockIPFSClient{
		addFunc: func(ctx context.Context, reader io.Reader, name string) (*ipfs.AddResponse, error) {
			data, _ := io.ReadAll(reader)
			if string(data) != string(testData) {
				return nil, io.ErrUnexpectedEOF
			}
			return &ipfs.AddResponse{
				Cid:  expectedCID,
				Name: name,
				Size: int64(len(testData)),
			}, nil
		},
	}

	gw := newTestGatewayWithIPFS(t, mockClient)

	reqBody := storage.StorageUploadRequest{
		Name: expectedName,
		Data: base64Data,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "test-ns")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	gw.storageHandlers.UploadHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp storage.StorageUploadResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Cid != expectedCID {
		t.Errorf("Expected CID %s, got %s", expectedCID, resp.Cid)
	}
}

func TestStorageUploadHandler_InvalidBase64(t *testing.T) {
	gw := newTestGatewayWithIPFS(t, &mockIPFSClient{})

	reqBody := storage.StorageUploadRequest{
		Name: "test.txt",
		Data: "invalid base64!!!",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "test-ns")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	gw.storageHandlers.UploadHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestStorageUploadHandler_IPFSError(t *testing.T) {
	mockClient := &mockIPFSClient{
		addFunc: func(ctx context.Context, reader io.Reader, name string) (*ipfs.AddResponse, error) {
			return nil, io.ErrUnexpectedEOF
		},
	}

	gw := newTestGatewayWithIPFS(t, mockClient)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("test"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "test-ns")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	gw.storageHandlers.UploadHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, w.Code)
	}
}

func TestStoragePinHandler_Success(t *testing.T) {
	expectedCID := "QmPin123"
	expectedName := "pinned-file"

	mockClient := &mockIPFSClient{
		pinFunc: func(ctx context.Context, cid string, name string, replicationFactor int) (*ipfs.PinResponse, error) {
			if cid != expectedCID {
				return nil, io.ErrUnexpectedEOF
			}
			if replicationFactor != 3 {
				return nil, io.ErrUnexpectedEOF
			}
			return &ipfs.PinResponse{Cid: cid, Name: name}, nil
		},
	}

	gw := newTestGatewayWithIPFS(t, mockClient)

	reqBody := storage.StoragePinRequest{
		Cid:  expectedCID,
		Name: expectedName,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/pin", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	gw.storageHandlers.PinHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp storage.StoragePinResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Cid != expectedCID {
		t.Errorf("Expected CID %s, got %s", expectedCID, resp.Cid)
	}
	if resp.Name != expectedName {
		t.Errorf("Expected name %s, got %s", expectedName, resp.Name)
	}
}

func TestStoragePinHandler_MissingCID(t *testing.T) {
	gw := newTestGatewayWithIPFS(t, &mockIPFSClient{})

	reqBody := storage.StoragePinRequest{}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/pin", bytes.NewReader(bodyBytes))
	w := httptest.NewRecorder()

	gw.storageHandlers.PinHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestStorageStatusHandler_Success(t *testing.T) {
	expectedCID := "QmStatus123"
	mockClient := &mockIPFSClient{
		pinStatusFunc: func(ctx context.Context, cid string) (*ipfs.PinStatus, error) {
			return &ipfs.PinStatus{
				Cid:               cid,
				Name:              "test-file",
				Status:            "pinned",
				ReplicationMin:    3,
				ReplicationMax:    3,
				ReplicationFactor: 3,
				Peers:             []string{"peer1", "peer2", "peer3"},
			}, nil
		},
	}

	gw := newTestGatewayWithIPFS(t, mockClient)

	req := httptest.NewRequest(http.MethodGet, "/v1/storage/status/"+expectedCID, nil)
	w := httptest.NewRecorder()

	gw.storageHandlers.StatusHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp storage.StorageStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Cid != expectedCID {
		t.Errorf("Expected CID %s, got %s", expectedCID, resp.Cid)
	}
	if resp.Status != "pinned" {
		t.Errorf("Expected status 'pinned', got %s", resp.Status)
	}
	if resp.ReplicationFactor != 3 {
		t.Errorf("Expected replication factor 3, got %d", resp.ReplicationFactor)
	}
}

func TestStorageStatusHandler_MissingCID(t *testing.T) {
	gw := newTestGatewayWithIPFS(t, &mockIPFSClient{})

	req := httptest.NewRequest(http.MethodGet, "/v1/storage/status/", nil)
	w := httptest.NewRecorder()

	gw.storageHandlers.StatusHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestStorageGetHandler_Success(t *testing.T) {
	expectedCID := "QmGet123"
	expectedContent := "test content from IPFS"

	mockClient := &mockIPFSClient{
		getFunc: func(ctx context.Context, cid string, ipfsAPIURL string) (io.ReadCloser, error) {
			if cid != expectedCID {
				return nil, io.ErrUnexpectedEOF
			}
			return io.NopCloser(strings.NewReader(expectedContent)), nil
		},
	}

	gw := newTestGatewayWithIPFS(t, mockClient)

	req := httptest.NewRequest(http.MethodGet, "/v1/storage/get/"+expectedCID, nil)
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "test-ns")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	gw.storageHandlers.DownloadHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	if w.Body.String() != expectedContent {
		t.Errorf("Expected content %s, got %s", expectedContent, w.Body.String())
	}

	if w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Errorf("Expected Content-Type 'application/octet-stream', got %s", w.Header().Get("Content-Type"))
	}
}

func TestStorageGetHandler_MissingNamespace(t *testing.T) {
	gw := newTestGatewayWithIPFS(t, &mockIPFSClient{})

	req := httptest.NewRequest(http.MethodGet, "/v1/storage/get/QmTest123", nil)
	w := httptest.NewRecorder()

	gw.storageHandlers.DownloadHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}
}

func TestStorageUnpinHandler_Success(t *testing.T) {
	expectedCID := "QmUnpin123"

	mockClient := &mockIPFSClient{
		unpinFunc: func(ctx context.Context, cid string) error {
			if cid != expectedCID {
				return io.ErrUnexpectedEOF
			}
			return nil
		},
	}

	gw := newTestGatewayWithIPFS(t, mockClient)

	req := httptest.NewRequest(http.MethodDelete, "/v1/storage/unpin/"+expectedCID, nil)
	w := httptest.NewRecorder()

	gw.storageHandlers.UnpinHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["cid"] != expectedCID {
		t.Errorf("Expected CID %s, got %v", expectedCID, resp["cid"])
	}
}

func TestStorageUnpinHandler_MissingCID(t *testing.T) {
	gw := newTestGatewayWithIPFS(t, &mockIPFSClient{})

	req := httptest.NewRequest(http.MethodDelete, "/v1/storage/unpin/", nil)
	w := httptest.NewRecorder()

	gw.storageHandlers.UnpinHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

// Helper function tests removed - base64Decode and getNamespaceFromContext
// are now private methods in the storage package and are tested indirectly
// through the handler tests.
