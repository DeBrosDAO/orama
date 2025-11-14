package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStorageClientImpl_Upload(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		expectedCID := "QmUpload123"
		expectedName := "test.txt"
		expectedSize := int64(100)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/storage/upload" {
				t.Errorf("Expected path '/v1/storage/upload', got %s", r.URL.Path)
			}

			// Verify multipart form
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				t.Errorf("Failed to parse multipart form: %v", err)
				return
			}

			file, header, err := r.FormFile("file")
			if err != nil {
				t.Errorf("Failed to get file: %v", err)
				return
			}
			defer file.Close()

			if header.Filename != expectedName {
				t.Errorf("Expected filename %s, got %s", expectedName, header.Filename)
			}

			response := StorageUploadResult{
				Cid:  expectedCID,
				Name: expectedName,
				Size: expectedSize,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := &ClientConfig{
			GatewayURL: server.URL,
			AppName:    "test-app",
			APIKey:     "ak_test:test-app", // Required for requireAccess check
		}
		client := &Client{config: cfg}
		storage := &StorageClientImpl{client: client}

		reader := strings.NewReader("test content")
		result, err := storage.Upload(context.Background(), reader, expectedName)
		if err != nil {
			t.Fatalf("Failed to upload: %v", err)
		}

		if result.Cid != expectedCID {
			t.Errorf("Expected CID %s, got %s", expectedCID, result.Cid)
		}
		if result.Name != expectedName {
			t.Errorf("Expected name %s, got %s", expectedName, result.Name)
		}
		if result.Size != expectedSize {
			t.Errorf("Expected size %d, got %d", expectedSize, result.Size)
		}
	})

	t.Run("server_error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal error"))
		}))
		defer server.Close()

		cfg := &ClientConfig{
			GatewayURL: server.URL,
			AppName:    "test-app",
		}
		client := &Client{config: cfg}
		storage := &StorageClientImpl{client: client}

		reader := strings.NewReader("test")
		_, err := storage.Upload(context.Background(), reader, "test.txt")
		if err == nil {
			t.Error("Expected error for server error")
		}
	})

	t.Run("missing_credentials", func(t *testing.T) {
		cfg := &ClientConfig{
			GatewayURL: "http://localhost:6001",
			// No AppName, JWT, or APIKey
		}
		client := &Client{config: cfg}
		storage := &StorageClientImpl{client: client}

		reader := strings.NewReader("test")
		_, err := storage.Upload(context.Background(), reader, "test.txt")
		if err == nil {
			t.Error("Expected error for missing credentials")
		}
	})
}

func TestStorageClientImpl_Pin(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		expectedCID := "QmPin123"
		expectedName := "pinned-file"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/storage/pin" {
				t.Errorf("Expected path '/v1/storage/pin', got %s", r.URL.Path)
			}

			var reqBody map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				t.Errorf("Failed to decode request: %v", err)
				return
			}

			if reqBody["cid"] != expectedCID {
				t.Errorf("Expected CID %s, got %v", expectedCID, reqBody["cid"])
			}

			response := StoragePinResult{
				Cid:  expectedCID,
				Name: expectedName,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := &ClientConfig{
			GatewayURL: server.URL,
			AppName:    "test-app",
			APIKey:     "ak_test:test-app", // Required for requireAccess check
		}
		client := &Client{config: cfg}
		storage := &StorageClientImpl{client: client}

		result, err := storage.Pin(context.Background(), expectedCID, expectedName)
		if err != nil {
			t.Fatalf("Failed to pin: %v", err)
		}

		if result.Cid != expectedCID {
			t.Errorf("Expected CID %s, got %s", expectedCID, result.Cid)
		}
		if result.Name != expectedName {
			t.Errorf("Expected name %s, got %s", expectedName, result.Name)
		}
	})
}

func TestStorageClientImpl_Status(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		expectedCID := "QmStatus123"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/v1/storage/status/") {
				t.Errorf("Expected path '/v1/storage/status/', got %s", r.URL.Path)
			}

			response := StorageStatus{
				Cid:               expectedCID,
				Name:              "test-file",
				Status:            "pinned",
				ReplicationMin:    3,
				ReplicationMax:    3,
				ReplicationFactor: 3,
				Peers:             []string{"peer1", "peer2", "peer3"},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := &ClientConfig{
			GatewayURL: server.URL,
			AppName:    "test-app",
			APIKey:     "ak_test:test-app", // Required for requireAccess check
		}
		client := &Client{config: cfg}
		storage := &StorageClientImpl{client: client}

		status, err := storage.Status(context.Background(), expectedCID)
		if err != nil {
			t.Fatalf("Failed to get status: %v", err)
		}

		if status.Cid != expectedCID {
			t.Errorf("Expected CID %s, got %s", expectedCID, status.Cid)
		}
		if status.Status != "pinned" {
			t.Errorf("Expected status 'pinned', got %s", status.Status)
		}
	})
}

func TestStorageClientImpl_Get(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		expectedCID := "QmGet123"
		expectedContent := "test content"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/v1/storage/get/") {
				t.Errorf("Expected path '/v1/storage/get/', got %s", r.URL.Path)
			}
			w.Write([]byte(expectedContent))
		}))
		defer server.Close()

		cfg := &ClientConfig{
			GatewayURL: server.URL,
			AppName:    "test-app",
			APIKey:     "ak_test:test-app", // Required for requireAccess check
		}
		client := &Client{config: cfg}
		storage := &StorageClientImpl{client: client}

		reader, err := storage.Get(context.Background(), expectedCID)
		if err != nil {
			t.Fatalf("Failed to get content: %v", err)
		}
		defer reader.Close()

		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("Failed to read content: %v", err)
		}

		if string(data) != expectedContent {
			t.Errorf("Expected content %s, got %s", expectedContent, string(data))
		}
	})
}

func TestStorageClientImpl_Unpin(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		expectedCID := "QmUnpin123"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/v1/storage/unpin/") {
				t.Errorf("Expected path '/v1/storage/unpin/', got %s", r.URL.Path)
			}
			if r.Method != "DELETE" {
				t.Errorf("Expected method DELETE, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		cfg := &ClientConfig{
			GatewayURL: server.URL,
			AppName:    "test-app",
			APIKey:     "ak_test:test-app", // Required for requireAccess check
		}
		client := &Client{config: cfg}
		storage := &StorageClientImpl{client: client}

		err := storage.Unpin(context.Background(), expectedCID)
		if err != nil {
			t.Fatalf("Failed to unpin: %v", err)
		}
	})
}

func TestStorageClientImpl_getGatewayURL(t *testing.T) {
	storage := &StorageClientImpl{}

	t.Run("from_config", func(t *testing.T) {
		cfg := &ClientConfig{GatewayURL: "http://custom:6001"}
		client := &Client{config: cfg}
		storage.client = client

		url := storage.getGatewayURL()
		if url != "http://custom:6001" {
			t.Errorf("Expected 'http://custom:6001', got %s", url)
		}
	})

	t.Run("default", func(t *testing.T) {
		cfg := &ClientConfig{}
		client := &Client{config: cfg}
		storage.client = client

		url := storage.getGatewayURL()
		if url != "http://localhost:6001" {
			t.Errorf("Expected 'http://localhost:6001', got %s", url)
		}
	})

	t.Run("nil_config", func(t *testing.T) {
		client := &Client{config: nil}
		storage.client = client

		url := storage.getGatewayURL()
		if url != "http://localhost:6001" {
			t.Errorf("Expected 'http://localhost:6001', got %s", url)
		}
	})
}

func TestStorageClientImpl_addAuthHeaders(t *testing.T) {
	t.Run("jwt_preferred", func(t *testing.T) {
		cfg := &ClientConfig{
			JWT:    "test-jwt-token",
			APIKey: "test-api-key",
		}
		client := &Client{config: cfg}
		storage := &StorageClientImpl{client: client}

		req := httptest.NewRequest("POST", "/test", nil)
		storage.addAuthHeaders(req)

		auth := req.Header.Get("Authorization")
		if auth != "Bearer test-jwt-token" {
			t.Errorf("Expected JWT in Authorization header, got %s", auth)
		}
	})

	t.Run("apikey_fallback", func(t *testing.T) {
		cfg := &ClientConfig{
			APIKey: "test-api-key",
		}
		client := &Client{config: cfg}
		storage := &StorageClientImpl{client: client}

		req := httptest.NewRequest("POST", "/test", nil)
		storage.addAuthHeaders(req)

		auth := req.Header.Get("Authorization")
		if auth != "Bearer test-api-key" {
			t.Errorf("Expected API key in Authorization header, got %s", auth)
		}

		apiKey := req.Header.Get("X-API-Key")
		if apiKey != "test-api-key" {
			t.Errorf("Expected API key in X-API-Key header, got %s", apiKey)
		}
	})

	t.Run("no_auth", func(t *testing.T) {
		cfg := &ClientConfig{}
		client := &Client{config: cfg}
		storage := &StorageClientImpl{client: client}

		req := httptest.NewRequest("POST", "/test", nil)
		storage.addAuthHeaders(req)

		auth := req.Header.Get("Authorization")
		if auth != "" {
			t.Errorf("Expected no Authorization header, got %s", auth)
		}
	})

	t.Run("nil_config", func(t *testing.T) {
		client := &Client{config: nil}
		storage := &StorageClientImpl{client: client}

		req := httptest.NewRequest("POST", "/test", nil)
		storage.addAuthHeaders(req)

		auth := req.Header.Get("Authorization")
		if auth != "" {
			t.Errorf("Expected no Authorization header, got %s", auth)
		}
	})
}
