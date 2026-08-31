package ipfs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
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

// TestClient_PinStatus_aggregation asserts PinStatus reports the cluster-wide
// status HONESTLY from the peer_map (bugboard #137): "pinned" ONLY when every
// peer is pinned, errors win over in-progress, and an empty peer_map is
// "unknown" — never the old optimistic "pinned" default.
func TestClient_PinStatus_aggregation(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name       string
		peerMap    map[string]interface{}
		wantStatus string
		wantPinned int
		wantTotal  int
	}{
		{
			name: "all_pinned",
			peerMap: map[string]interface{}{
				"peer1": map[string]interface{}{"status": "pinned"},
				"peer2": map[string]interface{}{"status": "pinned"},
				"peer3": map[string]interface{}{"status": "pinned"},
			},
			wantStatus: "pinned",
			wantPinned: 3,
			wantTotal:  3,
		},
		{
			name: "one_pinning_rest_pinned",
			peerMap: map[string]interface{}{
				"peer1": map[string]interface{}{"status": "pinned"},
				"peer2": map[string]interface{}{"status": "pinning"},
				"peer3": map[string]interface{}{"status": "pinned"},
			},
			wantStatus: "pinning",
			wantPinned: 2,
			wantTotal:  3,
		},
		{
			name: "pin_error_wins",
			peerMap: map[string]interface{}{
				"peer1": map[string]interface{}{"status": "pinned"},
				"peer2": map[string]interface{}{"status": "pinning"},
				"peer3": map[string]interface{}{"status": "pin_error", "error": "boom"},
			},
			wantStatus: "error",
			wantPinned: 1,
			wantTotal:  3,
		},
		{
			name:       "empty_peer_map_unknown",
			peerMap:    map[string]interface{}{},
			wantStatus: "unknown",
			wantPinned: 0,
			wantTotal:  0,
		},
		{
			name: "no_optimistic_pinned_default",
			peerMap: map[string]interface{}{
				"peer1": map[string]interface{}{"status": "remote"},
				"peer2": map[string]interface{}{"status": "remote"},
			},
			wantStatus: "remote",
			wantPinned: 0,
			wantTotal:  2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := map[string]interface{}{
					"cid":      "QmAgg",
					"name":     "agg-file",
					"peer_map": tc.peerMap,
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()

			client, err := NewClient(Config{ClusterAPIURL: server.URL}, logger)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}

			status, err := client.PinStatus(context.Background(), "QmAgg")
			if err != nil {
				t.Fatalf("PinStatus: %v", err)
			}
			if status.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", status.Status, tc.wantStatus)
			}
			if status.PinnedPeers != tc.wantPinned {
				t.Errorf("PinnedPeers = %d, want %d", status.PinnedPeers, tc.wantPinned)
			}
			if status.TotalPeers != tc.wantTotal {
				t.Errorf("TotalPeers = %d, want %d", status.TotalPeers, tc.wantTotal)
			}
		})
	}
}

// TestClient_PinStatus_numericStatus asserts the per-peer status is normalized
// correctly when the cluster API encodes TrackerStatus as a number rather than
// a string.
func TestClient_PinStatus_numericStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"cid": "QmNum",
			"peer_map": map[string]interface{}{
				// Numeric status that is NOT "pinned" must not be treated as pinned.
				"peer1": map[string]interface{}{"status": 5},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client, err := NewClient(Config{ClusterAPIURL: server.URL}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	status, err := client.PinStatus(context.Background(), "QmNum")
	if err != nil {
		t.Fatalf("PinStatus: %v", err)
	}
	if status.Status == "pinned" {
		t.Errorf("numeric non-pinned status must not aggregate to 'pinned', got %q", status.Status)
	}
	if status.PinnedPeers != 0 {
		t.Errorf("PinnedPeers = %d, want 0", status.PinnedPeers)
	}
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

// --- EvictLocal (bugboard #153) -----------------------------------------------

// newEvictTestClient points a Client's kubo API at a stub daemon.
func newEvictTestClient(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	// Every eviction first waits for this node's kubo to drop its own pin.
	// Unless a test overrides it, answer "not pinned" so the wait completes
	// immediately and the test exercises the removal path it cares about.
	inner := handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pin/ls") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"Message":"path 'x' is not pinned","Code":0,"Type":"error"}`))
			return
		}
		inner(w, r)
	}))
	c, err := NewClient(Config{IPFSAPIURL: srv.URL, Timeout: 5 * time.Second}, zap.NewNop())
	if err != nil {
		srv.Close()
		t.Fatalf("NewClient: %v", err)
	}
	return c, srv.Close
}

// The DAG walk must be local-only. Without offline=true kubo treats a missing
// block as a cache miss and goes to the network, so enumerating a DAG this node
// does not hold blocks until the caller's deadline — which failed the whole
// cluster-wide eviction fan-out instead of reporting "nothing here".
func TestEvictLocal_refsAreOfflineOnly(t *testing.T) {
	var sawOffline string
	c, done := newEvictTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/refs") {
			sawOffline = r.URL.Query().Get("offline")
			_, _ = w.Write([]byte(`{"Ref":"QmChild","Err":""}` + "\n"))
			return
		}
		_, _ = w.Write([]byte(`{"Hash":"x","Error":""}` + "\n"))
	})
	defer done()

	if _, err := c.EvictLocal(context.Background(), "QmRoot"); err != nil {
		t.Fatalf("EvictLocal: %v", err)
	}
	if sawOffline != "true" {
		t.Errorf("refs offline param = %q, want \"true\"", sawOffline)
	}
}

// A node that does not hold the DAG has nothing to reclaim. That is the normal
// case for most nodes whenever the replication factor is below the cluster
// size, so it must not be reported as a failure — doing so would make a
// cluster-wide eviction permanently incomplete on any cluster larger than RF.
func TestEvictLocal_nodeWithoutTheDAGIsNotAFailure(t *testing.T) {
	c, done := newEvictTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/refs") {
			_, _ = w.Write([]byte(`{"Ref":"","Err":"block was not found locally (offline): ipld: could not find QmRoot"}` + "\n"))
			return
		}
		t.Error("block/rm must not be called when the DAG is absent")
	})
	defer done()

	removed, err := c.EvictLocal(context.Background(), "QmRoot")
	if err != nil {
		t.Fatalf("absent DAG must not be an error, got %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

// A real traversal failure must still surface — the not-found tolerance above
// must not swallow every refs error.
func TestEvictLocal_realRefsFailureStillErrors(t *testing.T) {
	c, done := newEvictTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/refs") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("datastore is corrupt"))
			return
		}
	})
	defer done()

	if _, err := c.EvictLocal(context.Background(), "QmRoot"); err == nil {
		t.Fatal("want an error for a genuine refs failure")
	}
}

// Every block of the DAG plus the root must be removed, and an already-absent
// block counts as success (idempotent reclaim).
func TestEvictLocal_removesEveryBlockIncludingRoot(t *testing.T) {
	var removed []string
	c, done := newEvictTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/refs") {
			_, _ = w.Write([]byte(`{"Ref":"QmA","Err":""}` + "\n" + `{"Ref":"QmB","Err":""}` + "\n"))
			return
		}
		arg := r.URL.Query().Get("arg")
		removed = append(removed, arg)
		if arg == "QmB" {
			_, _ = w.Write([]byte(`{"Hash":"QmB","Error":"blockstore: block not found"}` + "\n"))
			return
		}
		_, _ = w.Write([]byte(`{"Hash":"` + arg + `","Error":""}` + "\n"))
	})
	defer done()

	n, err := c.EvictLocal(context.Background(), "QmRoot")
	if err != nil {
		t.Fatalf("EvictLocal: %v", err)
	}
	if n != 3 {
		t.Errorf("removed = %d, want 3 (QmA, QmB already-absent, QmRoot)", n)
	}
	want := []string{"QmA", "QmB", "QmRoot"}
	if len(removed) != len(want) {
		t.Fatalf("block/rm called for %v, want %v", removed, want)
	}
	for i := range want {
		if removed[i] != want[i] {
			t.Errorf("block/rm[%d] = %q, want %q", i, removed[i], want[i])
		}
	}
}

// A block that is still part of another pinned DAG must be surfaced, not
// silently counted as reclaimed — that is the pin-safety guarantee.
func TestEvictLocal_stillPinnedBlockSurfaces(t *testing.T) {
	c, done := newEvictTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/refs") {
			_, _ = w.Write([]byte(`{"Ref":"QmA","Err":""}` + "\n"))
			return
		}
		_, _ = w.Write([]byte(`{"Hash":"QmA","Error":"pinned: pinned via QmOther"}` + "\n"))
	})
	defer done()

	if _, err := c.EvictLocal(context.Background(), "QmRoot"); err == nil {
		t.Fatal("a still-pinned block must be reported, not swallowed")
	}
}

// Bugboard #153 propagation race.
//
// IPFS-Cluster's unpin returns once the removal is committed to its consensus
// log; each peer's kubo then unpins asynchronously. The eviction fan-out fires
// immediately after that return, so on every node except the one that served
// the request the pin was typically still in place — and `block rm` without
// --force correctly refuses a pinned block. Observed live on devnet: two of
// three nodes reported an incomplete reclaim and kept the blocks.
func TestEvictLocal_waitsForTheLocalPinToClear(t *testing.T) {
	var pinChecks int32
	var blockRms int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/pin/ls"):
			// Still pinned for the first two polls, then the cluster unpin
			// lands and kubo reports it gone.
			if atomic.AddInt32(&pinChecks, 1) <= 2 {
				_, _ = w.Write([]byte(`{"Keys":{"QmRoot":{"Type":"recursive"}}}`))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"Message":"path 'QmRoot' is not pinned","Code":0,"Type":"error"}`))
		case strings.Contains(r.URL.Path, "/refs"):
			_, _ = w.Write([]byte(`{"Ref":"QmChild","Err":""}` + "\n"))
		default:
			atomic.AddInt32(&blockRms, 1)
			_, _ = w.Write([]byte(`{"Hash":"x","Error":""}` + "\n"))
		}
	}))
	defer srv.Close()

	c, err := NewClient(Config{IPFSAPIURL: srv.URL, Timeout: 10 * time.Second}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	removed, err := c.EvictLocal(context.Background(), "QmRoot")
	if err != nil {
		t.Fatalf("EvictLocal: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (child + root)", removed)
	}
	if got := atomic.LoadInt32(&pinChecks); got < 3 {
		t.Errorf("pin was polled %d times; the wait must re-check until the pin clears", got)
	}
	if atomic.LoadInt32(&blockRms) == 0 {
		t.Error("no block was removed after the pin cleared")
	}
}

// A node whose kubo never drops the pin must be reported, not silently counted
// as reclaimed — that is the difference between "the bytes are gone" and "the
// bytes are still here and we told the user otherwise".
func TestEvictLocal_stillPinnedAfterTheBoundIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pin/ls") {
			_, _ = w.Write([]byte(`{"Keys":{"QmRoot":{"Type":"recursive"}}}`))
			return
		}
		t.Error("block removal must not be attempted while the CID is still pinned")
	}))
	defer srv.Close()

	c, err := NewClient(Config{IPFSAPIURL: srv.URL, Timeout: 10 * time.Second}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// Bound the test by its own context rather than waiting the full
	// propagation timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	removed, err := c.EvictLocal(ctx, "QmRoot")
	if err == nil {
		t.Fatal("a CID still pinned locally must be reported as an incomplete reclaim")
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

// kubo answers an unpinned CID with a 500 and "is not pinned". Treating that as
// a transport failure would make every eviction fail.
func TestIsPinnedLocally_readsKuboResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"pinned recursively", 200, `{"Keys":{"QmRoot":{"Type":"recursive"}}}`, true},
		{"pinned indirectly", 200, `{"Keys":{"QmRoot":{"Type":"indirect"}}}`, true},
		{"not pinned", 500, `{"Message":"path 'QmRoot' is not pinned","Code":0,"Type":"error"}`, false},
		{"empty pinset", 200, `{"Keys":{}}`, false},
		{"a different cid", 200, `{"Keys":{"QmOther":{"Type":"recursive"}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c, err := NewClient(Config{IPFSAPIURL: srv.URL, Timeout: 5 * time.Second}, zap.NewNop())
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			got, err := c.isPinnedLocally(context.Background(), "QmRoot")
			if err != nil {
				t.Fatalf("isPinnedLocally: %v", err)
			}
			if got != tc.want {
				t.Errorf("isPinnedLocally = %v, want %v", got, tc.want)
			}
		})
	}
}
