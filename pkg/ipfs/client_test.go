package ipfs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewClient(t *testing.T) {
	logger := zap.NewNop()

	t.Run("default_config", func(t *testing.T) {
		cfg := Config{}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		if client.apiURL != "http://localhost:9094" {
			t.Errorf("Expected default API URL 'http://localhost:9094', got %s", client.apiURL)
		}

		if client.httpClient.Timeout != 60*time.Second {
			t.Errorf("Expected default timeout 60s, got %v", client.httpClient.Timeout)
		}
	})

	t.Run("custom_config", func(t *testing.T) {
		cfg := Config{
			ClusterAPIURL: "http://custom:9094",
			Timeout:       30 * time.Second,
		}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		if client.apiURL != "http://custom:9094" {
			t.Errorf("Expected API URL 'http://custom:9094', got %s", client.apiURL)
		}

		if client.httpClient.Timeout != 30*time.Second {
			t.Errorf("Expected timeout 30s, got %v", client.httpClient.Timeout)
		}
	})
}

func TestClient_Add(t *testing.T) {
	logger := zap.NewNop()

	t.Run("success", func(t *testing.T) {
		expectedCID := "QmTest123"
		expectedName := "test.txt"
		testContent := "test content"
		expectedSize := int64(len(testContent)) // Client overrides server size with actual content length

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/add" {
				t.Errorf("Expected path '/add', got %s", r.URL.Path)
			}
			if r.Method != "POST" {
				t.Errorf("Expected method POST, got %s", r.Method)
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

			// Read file content
			_, _ = io.ReadAll(file)

			// Return a different size to verify the client correctly overrides it
			response := AddResponse{
				Cid:  expectedCID,
				Name: expectedName,
				Size: 999, // Client will override this with actual content size
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: server.URL}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		reader := strings.NewReader(testContent)
		resp, err := client.Add(context.Background(), reader, expectedName)
		if err != nil {
			t.Fatalf("Failed to add content: %v", err)
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
	})

	t.Run("server_error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal error"))
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: server.URL}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		reader := strings.NewReader("test")
		_, err = client.Add(context.Background(), reader, "test.txt")
		if err == nil {
			t.Error("Expected error for server error")
		}
	})
}

func TestClient_Pin(t *testing.T) {
	logger := zap.NewNop()

	t.Run("success", func(t *testing.T) {
		expectedCID := "QmPin123"
		expectedName := "pinned-file"
		expectedReplicationFactor := 3

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/pins/") {
				t.Errorf("Expected path '/pins/', got %s", r.URL.Path)
			}
			if r.Method != "POST" {
				t.Errorf("Expected method POST, got %s", r.Method)
			}

			if cid := strings.TrimPrefix(r.URL.Path, "/pins/"); cid != expectedCID {
				t.Errorf("Expected CID %s in path, got %s", expectedCID, cid)
			}

			query := r.URL.Query()
			if got := query.Get("replication-min"); got != strconv.Itoa(expectedReplicationFactor) {
				t.Errorf("Expected replication-min %d, got %s", expectedReplicationFactor, got)
			}
			if got := query.Get("replication-max"); got != strconv.Itoa(expectedReplicationFactor) {
				t.Errorf("Expected replication-max %d, got %s", expectedReplicationFactor, got)
			}
			if got := query.Get("name"); got != expectedName {
				t.Errorf("Expected name %s, got %s", expectedName, got)
			}

			response := PinResponse{
				Cid:  expectedCID,
				Name: expectedName,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: server.URL}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		resp, err := client.Pin(context.Background(), expectedCID, expectedName, expectedReplicationFactor)
		if err != nil {
			t.Fatalf("Failed to pin: %v", err)
		}

		if resp.Cid != expectedCID {
			t.Errorf("Expected CID %s, got %s", expectedCID, resp.Cid)
		}
		if resp.Name != expectedName {
			t.Errorf("Expected name %s, got %s", expectedName, resp.Name)
		}
	})

	t.Run("accepted_status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			response := PinResponse{Cid: "QmTest", Name: "test"}
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: server.URL}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		_, err = client.Pin(context.Background(), "QmTest", "test", 3)
		if err != nil {
			t.Errorf("Expected success for Accepted status, got error: %v", err)
		}
	})
}

func TestClient_PinStatus(t *testing.T) {
	logger := zap.NewNop()

	t.Run("success", func(t *testing.T) {
		expectedCID := "QmStatus123"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/pins/") {
				t.Errorf("Expected path '/pins/', got %s", r.URL.Path)
			}
			if r.Method != "GET" {
				t.Errorf("Expected method GET, got %s", r.Method)
			}

			response := map[string]interface{}{
				"cid":  expectedCID,
				"name": "test-file",
				"peer_map": map[string]interface{}{
					"peer1": map[string]interface{}{"status": "pinned"},
					"peer2": map[string]interface{}{"status": "pinned"},
					"peer3": map[string]interface{}{"status": "pinned"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: server.URL}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		status, err := client.PinStatus(context.Background(), expectedCID)
		if err != nil {
			t.Fatalf("Failed to get pin status: %v", err)
		}

		if status.Cid != expectedCID {
			t.Errorf("Expected CID %s, got %s", expectedCID, status.Cid)
		}
		if status.Status != "pinned" {
			t.Errorf("Expected status 'pinned', got %s", status.Status)
		}
		if len(status.Peers) != 3 {
			t.Errorf("Expected 3 peers, got %d", len(status.Peers))
		}
	})

	t.Run("not_found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: server.URL}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		_, err = client.PinStatus(context.Background(), "QmNotFound")
		if err == nil {
			t.Error("Expected error for not found")
		}
	})
}

func TestClient_Unpin(t *testing.T) {
	logger := zap.NewNop()

	t.Run("success", func(t *testing.T) {
		expectedCID := "QmUnpin123"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/pins/") {
				t.Errorf("Expected path '/pins/', got %s", r.URL.Path)
			}
			if r.Method != "DELETE" {
				t.Errorf("Expected method DELETE, got %s", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: server.URL}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		err = client.Unpin(context.Background(), expectedCID)
		if err != nil {
			t.Fatalf("Failed to unpin: %v", err)
		}
	})

	t.Run("accepted_status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: server.URL}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		err = client.Unpin(context.Background(), "QmTest")
		if err != nil {
			t.Errorf("Expected success for Accepted status, got error: %v", err)
		}
	})
}

func TestClient_Get(t *testing.T) {
	logger := zap.NewNop()

	t.Run("success", func(t *testing.T) {
		expectedCID := "QmGet123"
		expectedContent := "test content from IPFS"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.URL.Path, "/api/v0/cat") {
				t.Errorf("Expected path containing '/api/v0/cat', got %s", r.URL.Path)
			}
			if r.Method != "POST" {
				t.Errorf("Expected method POST, got %s", r.Method)
			}

			// Verify CID parameter
			if !strings.Contains(r.URL.RawQuery, expectedCID) {
				t.Errorf("Expected CID %s in query, got %s", expectedCID, r.URL.RawQuery)
			}

			w.Write([]byte(expectedContent))
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: "http://localhost:9094"}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		reader, err := client.Get(context.Background(), expectedCID, server.URL)
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

	t.Run("not_found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: "http://localhost:9094"}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		_, err = client.Get(context.Background(), "QmNotFound", server.URL)
		if err == nil {
			t.Error("Expected error for not found")
		}
	})

	t.Run("default_ipfs_api_url", func(t *testing.T) {
		expectedCID := "QmDefault"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("content"))
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: "http://localhost:9094"}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		// Test with empty IPFS API URL (should use default)
		// Note: This will fail because we're using a test server, but it tests the logic
		_, err = client.Get(context.Background(), expectedCID, "")
		// We expect an error here because default localhost:5001 won't exist
		if err == nil {
			t.Error("Expected error when using default localhost:5001")
		}
	})
}

func TestClient_Health(t *testing.T) {
	logger := zap.NewNop()

	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/id" {
				t.Errorf("Expected path '/id', got %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id": "test"}`))
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: server.URL}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		err = client.Health(context.Background())
		if err != nil {
			t.Fatalf("Failed health check: %v", err)
		}
	})

	t.Run("unhealthy", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		cfg := Config{ClusterAPIURL: server.URL}
		client, err := NewClient(cfg, logger)
		if err != nil {
			t.Fatalf("Failed to create client: %v", err)
		}

		err = client.Health(context.Background())
		if err == nil {
			t.Error("Expected error for unhealthy status")
		}
	})
}

func TestClient_Close(t *testing.T) {
	logger := zap.NewNop()

	cfg := Config{ClusterAPIURL: "http://localhost:9094"}
	client, err := NewClient(cfg, logger)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Close should not error
	err = client.Close(context.Background())
	if err != nil {
		t.Errorf("Close should not error, got: %v", err)
	}
}
