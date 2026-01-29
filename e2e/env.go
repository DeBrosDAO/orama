//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/gorilla/websocket"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"gopkg.in/yaml.v2"
)

var (
	gatewayURLCache  string
	apiKeyCache      string
	bootstrapCache   []string
	rqliteCache      []string
	ipfsClusterCache string
	ipfsAPICache     string
	cacheMutex       sync.RWMutex
)

// createAPIKeyWithProvisioning creates an API key for a namespace, handling async provisioning
// For non-default namespaces, this may trigger cluster provisioning and wait for it to complete.
func createAPIKeyWithProvisioning(gatewayURL, wallet, namespace string, timeout time.Duration) (string, error) {
	httpClient := NewHTTPClient(10 * time.Second)

	makeRequest := func() (*http.Response, []byte, error) {
		reqBody := map[string]string{
			"wallet":    wallet,
			"namespace": namespace,
		}
		bodyBytes, _ := json.Marshal(reqBody)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "POST", gatewayURL+"/v1/auth/simple-key", bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("request failed: %w", err)
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, respBody, nil
	}

	startTime := time.Now()
	for {
		if time.Since(startTime) > timeout {
			return "", fmt.Errorf("timeout waiting for namespace provisioning")
		}

		resp, respBody, err := makeRequest()
		if err != nil {
			return "", err
		}

		// If we got 200, extract the API key
		if resp.StatusCode == http.StatusOK {
			var apiKeyResp map[string]interface{}
			if err := json.Unmarshal(respBody, &apiKeyResp); err != nil {
				return "", fmt.Errorf("failed to decode API key response: %w", err)
			}
			apiKey, ok := apiKeyResp["api_key"].(string)
			if !ok || apiKey == "" {
				return "", fmt.Errorf("API key not found in response")
			}
			return apiKey, nil
		}

		// If we got 202 Accepted, provisioning is in progress
		if resp.StatusCode == http.StatusAccepted {
			// Wait and retry - the cluster is being provisioned
			time.Sleep(5 * time.Second)
			continue
		}

		// Any other status is an error
		return "", fmt.Errorf("API key creation failed with status %d: %s", resp.StatusCode, string(respBody))
	}
}

// loadGatewayConfig loads gateway configuration from ~/.orama/gateway.yaml
func loadGatewayConfig() (map[string]interface{}, error) {
	configPath, err := config.DefaultPath("gateway.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to get gateway config path: %w", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read gateway config: %w", err)
	}

	var cfg map[string]interface{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse gateway config: %w", err)
	}

	return cfg, nil
}

// loadNodeConfig loads node configuration from ~/.orama/node-*.yaml
func loadNodeConfig(filename string) (map[string]interface{}, error) {
	configPath, err := config.DefaultPath(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to get config path: %w", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg map[string]interface{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return cfg, nil
}

// GetGatewayURL returns the gateway base URL from config
func GetGatewayURL() string {
	cacheMutex.RLock()
	if gatewayURLCache != "" {
		defer cacheMutex.RUnlock()
		return gatewayURLCache
	}
	cacheMutex.RUnlock()

	// Check environment variables first (ORAMA_GATEWAY_URL takes precedence)
	if envURL := os.Getenv("ORAMA_GATEWAY_URL"); envURL != "" {
		cacheMutex.Lock()
		gatewayURLCache = envURL
		cacheMutex.Unlock()
		return envURL
	}
	if envURL := os.Getenv("GATEWAY_URL"); envURL != "" {
		cacheMutex.Lock()
		gatewayURLCache = envURL
		cacheMutex.Unlock()
		return envURL
	}

	// Try to load from gateway config
	gwCfg, err := loadGatewayConfig()
	if err == nil {
		if server, ok := gwCfg["server"].(map[interface{}]interface{}); ok {
			if port, ok := server["port"].(int); ok {
				url := fmt.Sprintf("http://localhost:%d", port)
				cacheMutex.Lock()
				gatewayURLCache = url
				cacheMutex.Unlock()
				return url
			}
		}
	}

	// Default fallback
	return "http://localhost:6001"
}

// GetRQLiteNodes returns rqlite endpoint addresses from config
func GetRQLiteNodes() []string {
	cacheMutex.RLock()
	if len(rqliteCache) > 0 {
		defer cacheMutex.RUnlock()
		return rqliteCache
	}
	cacheMutex.RUnlock()

	// Try all node config files
	for _, cfgFile := range []string{"node-1.yaml", "node-2.yaml", "node-3.yaml", "node-4.yaml", "node-5.yaml"} {
		nodeCfg, err := loadNodeConfig(cfgFile)
		if err != nil {
			continue
		}

		if db, ok := nodeCfg["database"].(map[interface{}]interface{}); ok {
			if rqlitePort, ok := db["rqlite_port"].(int); ok {
				nodes := []string{fmt.Sprintf("http://localhost:%d", rqlitePort)}
				cacheMutex.Lock()
				rqliteCache = nodes
				cacheMutex.Unlock()
				return nodes
			}
		}
	}

	// Default fallback
	return []string{"http://localhost:5001"}
}

// queryAPIKeyFromRQLite queries the SQLite database directly for an API key
func queryAPIKeyFromRQLite() (string, error) {
	// 1. Check environment variable first
	if envKey := os.Getenv("DEBROS_API_KEY"); envKey != "" {
		return envKey, nil
	}

	// 2. If ORAMA_GATEWAY_URL is set (production mode), query the remote RQLite HTTP API
	if gatewayURL := os.Getenv("ORAMA_GATEWAY_URL"); gatewayURL != "" {
		apiKey, err := queryAPIKeyFromRemoteRQLite(gatewayURL)
		if err == nil && apiKey != "" {
			return apiKey, nil
		}
		// Fall through to local database check if remote fails
	}

	// 3. Build database path from bootstrap/node config (for local development)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	// Try all node data directories (both production and development paths)
	dbPaths := []string{
		// Development paths (~/.orama/node-x/...)
		filepath.Join(homeDir, ".orama", "node-1", "rqlite", "db.sqlite"),
		filepath.Join(homeDir, ".orama", "node-2", "rqlite", "db.sqlite"),
		filepath.Join(homeDir, ".orama", "node-3", "rqlite", "db.sqlite"),
		filepath.Join(homeDir, ".orama", "node-4", "rqlite", "db.sqlite"),
		filepath.Join(homeDir, ".orama", "node-5", "rqlite", "db.sqlite"),
		// Production paths (~/.orama/data/node-x/...)
		filepath.Join(homeDir, ".orama", "data", "node-1", "rqlite", "db.sqlite"),
		filepath.Join(homeDir, ".orama", "data", "node-2", "rqlite", "db.sqlite"),
		filepath.Join(homeDir, ".orama", "data", "node-3", "rqlite", "db.sqlite"),
		filepath.Join(homeDir, ".orama", "data", "node-4", "rqlite", "db.sqlite"),
		filepath.Join(homeDir, ".orama", "data", "node-5", "rqlite", "db.sqlite"),
	}

	for _, dbPath := range dbPaths {
		// Check if database file exists
		if _, err := os.Stat(dbPath); err != nil {
			continue
		}

		// Open SQLite database
		db, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			continue
		}
		defer db.Close()

		// Set timeout for connection
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Query the api_keys table
		row := db.QueryRowContext(ctx, "SELECT key FROM api_keys ORDER BY id LIMIT 1")
		var apiKey string
		if err := row.Scan(&apiKey); err != nil {
			if err == sql.ErrNoRows {
				continue // Try next database
			}
			continue // Skip this database on error
		}

		if apiKey != "" {
			return apiKey, nil
		}
	}

	return "", fmt.Errorf("failed to retrieve API key from any SQLite database")
}

// queryAPIKeyFromRemoteRQLite queries the remote RQLite HTTP API for an API key
func queryAPIKeyFromRemoteRQLite(gatewayURL string) (string, error) {
	// Parse the gateway URL to extract the host
	parsed, err := url.Parse(gatewayURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse gateway URL: %w", err)
	}

	// RQLite HTTP API runs on port 5001 (not the gateway port 6001)
	rqliteURL := fmt.Sprintf("http://%s:5001/db/query", parsed.Hostname())

	// Create request body
	reqBody := `["SELECT key FROM api_keys LIMIT 1"]`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rqliteURL, strings.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to query rqlite: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("rqlite returned status %d", resp.StatusCode)
	}

	// Parse response
	var result struct {
		Results []struct {
			Columns []string        `json:"columns"`
			Values  [][]interface{} `json:"values"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(result.Results) > 0 && len(result.Results[0].Values) > 0 && len(result.Results[0].Values[0]) > 0 {
		if apiKey, ok := result.Results[0].Values[0][0].(string); ok && apiKey != "" {
			return apiKey, nil
		}
	}

	return "", fmt.Errorf("no API key found in rqlite")
}

// GetAPIKey returns the gateway API key from rqlite or cache
func GetAPIKey() string {
	cacheMutex.RLock()
	if apiKeyCache != "" {
		defer cacheMutex.RUnlock()
		return apiKeyCache
	}
	cacheMutex.RUnlock()

	// Query rqlite for API key
	apiKey, err := queryAPIKeyFromRQLite()
	if err != nil {
		return ""
	}

	cacheMutex.Lock()
	apiKeyCache = apiKey
	cacheMutex.Unlock()

	return apiKey
}

// GetJWT returns the gateway JWT token (currently not auto-discovered)
func GetJWT() string {
	return ""
}

// GetBootstrapPeers returns bootstrap peer addresses from config
func GetBootstrapPeers() []string {
	cacheMutex.RLock()
	if len(bootstrapCache) > 0 {
		defer cacheMutex.RUnlock()
		return bootstrapCache
	}
	cacheMutex.RUnlock()

	configFiles := []string{"node-1.yaml", "node-2.yaml", "node-3.yaml", "node-4.yaml", "node-5.yaml"}
	seen := make(map[string]struct{})
	var peers []string

	for _, cfgFile := range configFiles {
		nodeCfg, err := loadNodeConfig(cfgFile)
		if err != nil {
			continue
		}
		discovery, ok := nodeCfg["discovery"].(map[interface{}]interface{})
		if !ok {
			continue
		}
		rawPeers, ok := discovery["bootstrap_peers"].([]interface{})
		if !ok {
			continue
		}
		for _, v := range rawPeers {
			peerStr, ok := v.(string)
			if !ok || peerStr == "" {
				continue
			}
			if _, exists := seen[peerStr]; exists {
				continue
			}
			seen[peerStr] = struct{}{}
			peers = append(peers, peerStr)
		}
	}

	if len(peers) == 0 {
		return nil
	}

	cacheMutex.Lock()
	bootstrapCache = peers
	cacheMutex.Unlock()

	return peers
}

// GetIPFSClusterURL returns the IPFS cluster API URL from config
func GetIPFSClusterURL() string {
	cacheMutex.RLock()
	if ipfsClusterCache != "" {
		defer cacheMutex.RUnlock()
		return ipfsClusterCache
	}
	cacheMutex.RUnlock()

	// Try to load from node config
	for _, cfgFile := range []string{"node-1.yaml", "node-2.yaml", "node-3.yaml", "node-4.yaml", "node-5.yaml"} {
		nodeCfg, err := loadNodeConfig(cfgFile)
		if err != nil {
			continue
		}

		if db, ok := nodeCfg["database"].(map[interface{}]interface{}); ok {
			if ipfs, ok := db["ipfs"].(map[interface{}]interface{}); ok {
				if url, ok := ipfs["cluster_api_url"].(string); ok && url != "" {
					cacheMutex.Lock()
					ipfsClusterCache = url
					cacheMutex.Unlock()
					return url
				}
			}
		}
	}

	// Default fallback
	return "http://localhost:9094"
}

// GetIPFSAPIURL returns the IPFS API URL from config
func GetIPFSAPIURL() string {
	cacheMutex.RLock()
	if ipfsAPICache != "" {
		defer cacheMutex.RUnlock()
		return ipfsAPICache
	}
	cacheMutex.RUnlock()

	// Try to load from node config
	for _, cfgFile := range []string{"node-1.yaml", "node-2.yaml", "node-3.yaml", "node-4.yaml", "node-5.yaml"} {
		nodeCfg, err := loadNodeConfig(cfgFile)
		if err != nil {
			continue
		}

		if db, ok := nodeCfg["database"].(map[interface{}]interface{}); ok {
			if ipfs, ok := db["ipfs"].(map[interface{}]interface{}); ok {
				if url, ok := ipfs["api_url"].(string); ok && url != "" {
					cacheMutex.Lock()
					ipfsAPICache = url
					cacheMutex.Unlock()
					return url
				}
			}
		}
	}

	// Default fallback
	return "http://localhost:5001"
}

// GetClientNamespace returns the test client namespace from config
func GetClientNamespace() string {
	// Try to load from node config
	for _, cfgFile := range []string{"node-1.yaml", "node-2.yaml", "node-3.yaml", "node-4.yaml", "node-5.yaml"} {
		nodeCfg, err := loadNodeConfig(cfgFile)
		if err != nil {
			continue
		}

		if discovery, ok := nodeCfg["discovery"].(map[interface{}]interface{}); ok {
			if ns, ok := discovery["node_namespace"].(string); ok && ns != "" {
				return ns
			}
		}
	}

	return "default"
}

// SkipIfMissingGateway skips the test if gateway is not accessible or API key not available
func SkipIfMissingGateway(t *testing.T) {
	t.Helper()
	apiKey := GetAPIKey()
	if apiKey == "" {
		t.Skip("API key not available from rqlite; gateway tests skipped")
	}

	// Verify gateway is accessible
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GetGatewayURL()+"/v1/health", nil)
	if err != nil {
		t.Skip("Gateway not accessible; tests skipped")
		return
	}

	resp, err := NewHTTPClient(5 * time.Second).Do(req)
	if err != nil {
		t.Skip("Gateway not accessible; tests skipped")
		return
	}
	resp.Body.Close()
}

// IsGatewayReady checks if the gateway is accessible and healthy
func IsGatewayReady(ctx context.Context) bool {
	gatewayURL := GetGatewayURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayURL+"/v1/health", nil)
	if err != nil {
		return false
	}
	resp, err := NewHTTPClient(5 * time.Second).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// NewHTTPClient creates an authenticated HTTP client for gateway requests
func NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	// Skip TLS verification for testing against self-signed certificates
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// HTTPRequest is a helper for making authenticated HTTP requests
type HTTPRequest struct {
	Method   string
	URL      string
	Body     interface{}
	Headers  map[string]string
	Timeout  time.Duration
	SkipAuth bool
}

// Do executes an HTTP request and returns the response body
func (hr *HTTPRequest) Do(ctx context.Context) ([]byte, int, error) {
	if hr.Timeout == 0 {
		hr.Timeout = 30 * time.Second
	}

	var reqBody io.Reader
	if hr.Body != nil {
		data, err := json.Marshal(hr.Body)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, hr.Method, hr.URL, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers
	if hr.Headers != nil {
		for k, v := range hr.Headers {
			req.Header.Set(k, v)
		}
	}

	// Add JSON content type if body is present
	if hr.Body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	// Add auth headers
	if !hr.SkipAuth {
		if apiKey := GetAPIKey(); apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
			req.Header.Set("X-API-Key", apiKey)
		}
	}

	client := NewHTTPClient(hr.Timeout)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

// DecodeJSON unmarshals response body into v
func DecodeJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// NewNetworkClient creates a network client configured for e2e tests
func NewNetworkClient(t *testing.T) client.NetworkClient {
	t.Helper()

	namespace := GetClientNamespace()
	cfg := client.DefaultClientConfig(namespace)
	cfg.APIKey = GetAPIKey()
	cfg.QuietMode = true // Suppress debug logs in tests

	if jwt := GetJWT(); jwt != "" {
		cfg.JWT = jwt
	}

	if peers := GetBootstrapPeers(); len(peers) > 0 {
		cfg.BootstrapPeers = peers
	}

	if nodes := GetRQLiteNodes(); len(nodes) > 0 {
		cfg.DatabaseEndpoints = nodes
	}

	c, err := client.NewClient(cfg)
	if err != nil {
		t.Fatalf("failed to create network client: %v", err)
	}

	return c
}

// GenerateUniqueID generates a unique identifier for test resources
func GenerateUniqueID(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), rand.Intn(10000))
}

// GenerateTableName generates a unique table name for database tests
func GenerateTableName() string {
	return GenerateUniqueID("e2e_test")
}

// GenerateDMapName generates a unique dmap name for cache tests
func GenerateDMapName() string {
	return GenerateUniqueID("test_dmap")
}

// GenerateTopic generates a unique topic name for pubsub tests
func GenerateTopic() string {
	return GenerateUniqueID("e2e_topic")
}

// Delay pauses execution for the specified duration
func Delay(ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

// WaitForCondition waits for a condition with exponential backoff
func WaitForCondition(maxWait time.Duration, check func() bool) error {
	deadline := time.Now().Add(maxWait)
	backoff := 100 * time.Millisecond

	for {
		if check() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("condition not met within %v", maxWait)
		}
		time.Sleep(backoff)
		if backoff < 2*time.Second {
			backoff = backoff * 2
		}
	}
}

// NewTestLogger creates a test logger for debugging
func NewTestLogger(t *testing.T) *zap.Logger {
	t.Helper()
	config := zap.NewDevelopmentConfig()
	config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	logger, err := config.Build()
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	return logger
}

// CleanupDatabaseTable drops a table from the database after tests
func CleanupDatabaseTable(t *testing.T, tableName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Query rqlite to drop the table
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Logf("warning: failed to get home directory for cleanup: %v", err)
		return
	}

	dbPath := filepath.Join(homeDir, ".orama", "data", "node-1", "rqlite", "db.sqlite")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Logf("warning: failed to open database for cleanup: %v", err)
		return
	}
	defer db.Close()

	dropSQL := fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName)
	if _, err := db.ExecContext(ctx, dropSQL); err != nil {
		t.Logf("warning: failed to drop table %s: %v", tableName, err)
	}
}

// CleanupDMapCache deletes a dmap from the cache after tests
func CleanupDMapCache(t *testing.T, dmapName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &HTTPRequest{
		Method:  http.MethodDelete,
		URL:     GetGatewayURL() + "/v1/cache/dmap/" + dmapName,
		Timeout: 10 * time.Second,
	}

	_, status, err := req.Do(ctx)
	if err != nil {
		t.Logf("warning: failed to delete dmap %s: %v", dmapName, err)
		return
	}

	if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusNotFound {
		t.Logf("warning: delete dmap returned status %d", status)
	}
}

// CleanupIPFSFile unpins a file from IPFS after tests
func CleanupIPFSFile(t *testing.T, cid string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := NewTestLogger(t)
	cfg := &ipfs.Config{
		ClusterAPIURL: GetIPFSClusterURL(),
		Timeout:       30 * time.Second,
	}

	client, err := ipfs.NewClient(*cfg, logger)
	if err != nil {
		t.Logf("warning: failed to create IPFS client for cleanup: %v", err)
		return
	}

	if err := client.Unpin(ctx, cid); err != nil {
		t.Logf("warning: failed to unpin file %s: %v", cid, err)
	}
}

// CleanupCacheEntry deletes a cache entry after tests
func CleanupCacheEntry(t *testing.T, dmapName, key string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &HTTPRequest{
		Method:  http.MethodDelete,
		URL:     GetGatewayURL() + "/v1/cache/dmap/" + dmapName + "/key/" + key,
		Timeout: 10 * time.Second,
	}

	_, status, err := req.Do(ctx)
	if err != nil {
		t.Logf("warning: failed to delete cache entry: %v", err)
		return
	}

	if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusNotFound {
		t.Logf("warning: delete cache entry returned status %d", status)
	}
}

// ============================================================================
// WebSocket PubSub Client for E2E Tests
// ============================================================================

// WSPubSubClient is a WebSocket-based PubSub client that connects to the gateway
type WSPubSubClient struct {
	t        *testing.T
	conn     *websocket.Conn
	topic    string
	handlers []func(topic string, data []byte) error
	msgChan  chan []byte
	doneChan chan struct{}
	mu       sync.RWMutex
	writeMu  sync.Mutex // Protects concurrent writes to WebSocket
	closed   bool
}

// WSPubSubMessage represents a message received from the gateway
type WSPubSubMessage struct {
	Data      string `json:"data"`      // base64 encoded
	Timestamp int64  `json:"timestamp"` // unix milliseconds
	Topic     string `json:"topic"`
}

// NewWSPubSubClient creates a new WebSocket PubSub client connected to a topic
func NewWSPubSubClient(t *testing.T, topic string) (*WSPubSubClient, error) {
	t.Helper()

	// Build WebSocket URL
	gatewayURL := GetGatewayURL()
	wsURL := strings.Replace(gatewayURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)

	u, err := url.Parse(wsURL + "/v1/pubsub/ws")
	if err != nil {
		return nil, fmt.Errorf("failed to parse WebSocket URL: %w", err)
	}
	q := u.Query()
	q.Set("topic", topic)
	u.RawQuery = q.Encode()

	// Set up headers with authentication
	headers := http.Header{}
	if apiKey := GetAPIKey(); apiKey != "" {
		headers.Set("Authorization", "Bearer "+apiKey)
	}

	// Connect to WebSocket
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, resp, err := dialer.Dial(u.String(), headers)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("websocket dial failed (status %d): %w - body: %s", resp.StatusCode, err, string(body))
		}
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}

	client := &WSPubSubClient{
		t:        t,
		conn:     conn,
		topic:    topic,
		handlers: make([]func(topic string, data []byte) error, 0),
		msgChan:  make(chan []byte, 128),
		doneChan: make(chan struct{}),
	}

	// Start reader goroutine
	go client.readLoop()

	return client, nil
}

// NewWSPubSubPresenceClient creates a new WebSocket PubSub client with presence parameters
func NewWSPubSubPresenceClient(t *testing.T, topic, memberID string, meta map[string]interface{}) (*WSPubSubClient, error) {
	t.Helper()

	// Build WebSocket URL
	gatewayURL := GetGatewayURL()
	wsURL := strings.Replace(gatewayURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)

	u, err := url.Parse(wsURL + "/v1/pubsub/ws")
	if err != nil {
		return nil, fmt.Errorf("failed to parse WebSocket URL: %w", err)
	}
	q := u.Query()
	q.Set("topic", topic)
	q.Set("presence", "true")
	q.Set("member_id", memberID)
	if meta != nil {
		metaJSON, _ := json.Marshal(meta)
		q.Set("member_meta", string(metaJSON))
	}
	u.RawQuery = q.Encode()

	// Set up headers with authentication
	headers := http.Header{}
	if apiKey := GetAPIKey(); apiKey != "" {
		headers.Set("Authorization", "Bearer "+apiKey)
	}

	// Connect to WebSocket
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, resp, err := dialer.Dial(u.String(), headers)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("websocket dial failed (status %d): %w - body: %s", resp.StatusCode, err, string(body))
		}
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}

	client := &WSPubSubClient{
		t:        t,
		conn:     conn,
		topic:    topic,
		handlers: make([]func(topic string, data []byte) error, 0),
		msgChan:  make(chan []byte, 128),
		doneChan: make(chan struct{}),
	}

	// Start reader goroutine
	go client.readLoop()

	return client, nil
}

// readLoop reads messages from the WebSocket and dispatches to handlers
func (c *WSPubSubClient) readLoop() {
	defer close(c.doneChan)

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			c.mu.RLock()
			closed := c.closed
			c.mu.RUnlock()
			if !closed {
				// Only log if not intentionally closed
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					c.t.Logf("websocket read error: %v", err)
				}
			}
			return
		}

		// Parse the message envelope
		var msg WSPubSubMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			c.t.Logf("failed to unmarshal message: %v", err)
			continue
		}

		// Decode base64 data
		data, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil {
			c.t.Logf("failed to decode base64 data: %v", err)
			continue
		}

		// Send to message channel
		select {
		case c.msgChan <- data:
		default:
			c.t.Logf("message channel full, dropping message")
		}

		// Dispatch to handlers
		c.mu.RLock()
		handlers := make([]func(topic string, data []byte) error, len(c.handlers))
		copy(handlers, c.handlers)
		c.mu.RUnlock()

		for _, handler := range handlers {
			if err := handler(msg.Topic, data); err != nil {
				c.t.Logf("handler error: %v", err)
			}
		}
	}
}

// Subscribe adds a message handler
func (c *WSPubSubClient) Subscribe(handler func(topic string, data []byte) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers = append(c.handlers, handler)
}

// Publish sends a message to the topic
func (c *WSPubSubClient) Publish(data []byte) error {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()

	if closed {
		return fmt.Errorf("client is closed")
	}

	// Protect concurrent writes to WebSocket
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// ReceiveWithTimeout waits for a message with timeout
func (c *WSPubSubClient) ReceiveWithTimeout(timeout time.Duration) ([]byte, error) {
	select {
	case msg := <-c.msgChan:
		return msg, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for message")
	case <-c.doneChan:
		return nil, fmt.Errorf("connection closed")
	}
}

// Close closes the WebSocket connection
func (c *WSPubSubClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	// Send close message
	_ = c.conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))

	// Close connection
	return c.conn.Close()
}

// Topic returns the topic this client is subscribed to
func (c *WSPubSubClient) Topic() string {
	return c.topic
}

// WSPubSubClientPair represents a publisher and subscriber pair for testing
type WSPubSubClientPair struct {
	Publisher  *WSPubSubClient
	Subscriber *WSPubSubClient
	Topic      string
}

// NewWSPubSubClientPair creates a publisher and subscriber pair for a topic
func NewWSPubSubClientPair(t *testing.T, topic string) (*WSPubSubClientPair, error) {
	t.Helper()

	// Create subscriber first
	sub, err := NewWSPubSubClient(t, topic)
	if err != nil {
		return nil, fmt.Errorf("failed to create subscriber: %w", err)
	}

	// Small delay to ensure subscriber is registered
	time.Sleep(100 * time.Millisecond)

	// Create publisher
	pub, err := NewWSPubSubClient(t, topic)
	if err != nil {
		sub.Close()
		return nil, fmt.Errorf("failed to create publisher: %w", err)
	}

	return &WSPubSubClientPair{
		Publisher:  pub,
		Subscriber: sub,
		Topic:      topic,
	}, nil
}

// Close closes both publisher and subscriber
func (p *WSPubSubClientPair) Close() {
	if p.Publisher != nil {
		p.Publisher.Close()
	}
	if p.Subscriber != nil {
		p.Subscriber.Close()
	}
}

// ============================================================================
// Deployment Testing Helpers
// ============================================================================

// E2ETestEnv holds the environment configuration for deployment E2E tests
type E2ETestEnv struct {
	GatewayURL  string
	APIKey      string
	Namespace   string
	BaseDomain  string       // Domain for deployment routing (e.g., "dbrs.space")
	Config      *E2EConfig   // Full E2E configuration (for production tests)
	HTTPClient  *http.Client
	SkipCleanup bool
}

// BuildDeploymentDomain returns the full domain for a deployment name
// Format: {name}.{baseDomain} (e.g., "myapp.dbrs.space")
func (env *E2ETestEnv) BuildDeploymentDomain(deploymentName string) string {
	return fmt.Sprintf("%s.%s", deploymentName, env.BaseDomain)
}

// LoadTestEnv loads the test environment from environment variables and config file
// If ORAMA_API_KEY is not set, it creates a fresh API key for the default test namespace
func LoadTestEnv() (*E2ETestEnv, error) {
	// Load E2E config (for base_domain and production settings)
	cfg, err := LoadE2EConfig()
	if err != nil {
		// If config loading fails in production mode, that's an error
		if IsProductionMode() {
			return nil, fmt.Errorf("failed to load e2e config: %w", err)
		}
		// For local mode, use defaults
		cfg = DefaultConfig()
	}

	gatewayURL := os.Getenv("ORAMA_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = GetGatewayURL()
	}

	// Check if API key is provided via environment variable or config
	apiKey := os.Getenv("ORAMA_API_KEY")
	if apiKey == "" && cfg.APIKey != "" {
		apiKey = cfg.APIKey
	}
	namespace := os.Getenv("ORAMA_NAMESPACE")

	// If no API key provided, create a fresh one for a default test namespace
	if apiKey == "" {
		if namespace == "" {
			namespace = "default-test-ns"
		}

		// Generate a unique wallet address for this namespace
		wallet := fmt.Sprintf("0x%x", []byte(namespace+fmt.Sprintf("%d", time.Now().UnixNano())))
		if len(wallet) < 42 {
			wallet = wallet + strings.Repeat("0", 42-len(wallet))
		}
		if len(wallet) > 42 {
			wallet = wallet[:42]
		}

		// Create an API key for this namespace (handles async provisioning for non-default namespaces)
		var err error
		apiKey, err = createAPIKeyWithProvisioning(gatewayURL, wallet, namespace, 2*time.Minute)
		if err != nil {
			return nil, fmt.Errorf("failed to create API key for namespace %s: %w", namespace, err)
		}
	} else if namespace == "" {
		namespace = GetClientNamespace()
	}

	skipCleanup := os.Getenv("ORAMA_SKIP_CLEANUP") == "true"

	return &E2ETestEnv{
		GatewayURL:  gatewayURL,
		APIKey:      apiKey,
		Namespace:   namespace,
		BaseDomain:  cfg.BaseDomain,
		Config:      cfg,
		HTTPClient:  NewHTTPClient(30 * time.Second),
		SkipCleanup: skipCleanup,
	}, nil
}

// LoadTestEnvWithNamespace loads test environment with a specific namespace
// It creates a new API key for the specified namespace to ensure proper isolation
func LoadTestEnvWithNamespace(namespace string) (*E2ETestEnv, error) {
	// Load E2E config (for base_domain and production settings)
	cfg, err := LoadE2EConfig()
	if err != nil {
		cfg = DefaultConfig()
	}

	gatewayURL := os.Getenv("ORAMA_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = GetGatewayURL()
	}

	skipCleanup := os.Getenv("ORAMA_SKIP_CLEANUP") == "true"

	// Generate a unique wallet address for this namespace
	// Using namespace as part of the wallet address for uniqueness
	wallet := fmt.Sprintf("0x%x", []byte(namespace+fmt.Sprintf("%d", time.Now().UnixNano())))
	if len(wallet) < 42 {
		wallet = wallet + strings.Repeat("0", 42-len(wallet))
	}
	if len(wallet) > 42 {
		wallet = wallet[:42]
	}

	// Create an API key for this namespace (handles async provisioning for non-default namespaces)
	apiKey, err := createAPIKeyWithProvisioning(gatewayURL, wallet, namespace, 2*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to create API key for namespace %s: %w", namespace, err)
	}

	return &E2ETestEnv{
		GatewayURL:  gatewayURL,
		APIKey:      apiKey,
		Namespace:   namespace,
		BaseDomain:  cfg.BaseDomain,
		Config:      cfg,
		HTTPClient:  NewHTTPClient(30 * time.Second),
		SkipCleanup: skipCleanup,
	}, nil
}

// CreateTestDeployment creates a test deployment and returns its ID
func CreateTestDeployment(t *testing.T, env *E2ETestEnv, name, tarballPath string) string {
	t.Helper()

	file, err := os.Open(tarballPath)
	if err != nil {
		t.Fatalf("failed to open tarball: %v", err)
	}
	defer file.Close()

	// Create multipart form
	body := &bytes.Buffer{}
	boundary := "----WebKitFormBoundary7MA4YWxkTrZu0gW"

	// Write name field
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"name\"\r\n\r\n")
	body.WriteString(name + "\r\n")

	// NOTE: We intentionally do NOT send subdomain field
	// This ensures only node-specific domains are created: {name}.node-{id}.domain
	// Subdomain should only be sent if explicitly requested for custom domains

	// Write tarball file
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"tarball\"; filename=\"app.tar.gz\"\r\n")
	body.WriteString("Content-Type: application/gzip\r\n\r\n")

	fileData, _ := io.ReadAll(file)
	body.Write(fileData)
	body.WriteString("\r\n--" + boundary + "--\r\n")

	req, err := http.NewRequest("POST", env.GatewayURL+"/v1/deployments/static/upload", body)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("failed to upload deployment: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("deployment upload failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Try both "id" and "deployment_id" field names
	if id, ok := result["deployment_id"].(string); ok {
		return id
	}
	if id, ok := result["id"].(string); ok {
		return id
	}
	t.Fatalf("deployment response missing id field: %+v", result)
	return ""
}

// DeleteDeployment deletes a deployment by ID
func DeleteDeployment(t *testing.T, env *E2ETestEnv, deploymentID string) {
	t.Helper()

	req, _ := http.NewRequest("DELETE", env.GatewayURL+"/v1/deployments/delete?id="+deploymentID, nil)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	if err != nil {
		t.Logf("warning: failed to delete deployment: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Logf("warning: delete deployment returned status %d", resp.StatusCode)
	}
}

// GetDeployment retrieves deployment metadata by ID
func GetDeployment(t *testing.T, env *E2ETestEnv, deploymentID string) map[string]interface{} {
	t.Helper()

	req, _ := http.NewRequest("GET", env.GatewayURL+"/v1/deployments/get?id="+deploymentID, nil)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("get deployment failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var deployment map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&deployment); err != nil {
		t.Fatalf("failed to decode deployment: %v", err)
	}

	return deployment
}

// CreateSQLiteDB creates a SQLite database for a namespace
func CreateSQLiteDB(t *testing.T, env *E2ETestEnv, dbName string) {
	t.Helper()

	reqBody := map[string]string{"database_name": dbName}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", env.GatewayURL+"/v1/db/sqlite/create", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+env.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := env.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("create database failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}
}

// DeleteSQLiteDB deletes a SQLite database
func DeleteSQLiteDB(t *testing.T, env *E2ETestEnv, dbName string) {
	t.Helper()

	reqBody := map[string]string{"database_name": dbName}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("DELETE", env.GatewayURL+"/v1/db/sqlite/delete", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+env.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := env.HTTPClient.Do(req)
	if err != nil {
		t.Logf("warning: failed to delete database: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Logf("warning: delete database returned status %d", resp.StatusCode)
	}
}

// ExecuteSQLQuery executes a SQL query on a database
func ExecuteSQLQuery(t *testing.T, env *E2ETestEnv, dbName, query string) map[string]interface{} {
	t.Helper()

	reqBody := map[string]interface{}{
		"database_name": dbName,
		"query":         query,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", env.GatewayURL+"/v1/db/sqlite/query", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+env.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := env.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("failed to execute query: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode query response: %v", err)
	}

	if errMsg, ok := result["error"].(string); ok && errMsg != "" {
		t.Fatalf("SQL query failed: %s", errMsg)
	}

	return result
}

// QuerySQLite executes a SELECT query and returns rows
func QuerySQLite(t *testing.T, env *E2ETestEnv, dbName, query string) []map[string]interface{} {
	t.Helper()

	result := ExecuteSQLQuery(t, env, dbName, query)

	rows, ok := result["rows"].([]interface{})
	if !ok {
		return []map[string]interface{}{}
	}

	columns, _ := result["columns"].([]interface{})

	var results []map[string]interface{}
	for _, row := range rows {
		rowData, ok := row.([]interface{})
		if !ok {
			continue
		}

		rowMap := make(map[string]interface{})
		for i, col := range columns {
			if i < len(rowData) {
				rowMap[col.(string)] = rowData[i]
			}
		}
		results = append(results, rowMap)
	}

	return results
}

// UploadTestFile uploads a file to IPFS and returns the CID
func UploadTestFile(t *testing.T, env *E2ETestEnv, filename, content string) string {
	t.Helper()

	body := &bytes.Buffer{}
	boundary := "----WebKitFormBoundary7MA4YWxkTrZu0gW"

	body.WriteString("--" + boundary + "\r\n")
	body.WriteString(fmt.Sprintf("Content-Disposition: form-data; name=\"file\"; filename=\"%s\"\r\n", filename))
	body.WriteString("Content-Type: text/plain\r\n\r\n")
	body.WriteString(content)
	body.WriteString("\r\n--" + boundary + "--\r\n")

	req, _ := http.NewRequest("POST", env.GatewayURL+"/v1/storage/upload", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Authorization", "Bearer "+env.APIKey)

	resp, err := env.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("failed to upload file: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload file failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode upload response: %v", err)
	}

	cid, ok := result["cid"].(string)
	if !ok {
		t.Fatalf("CID not found in response")
	}

	return cid
}

// UnpinFile unpins a file from IPFS
func UnpinFile(t *testing.T, env *E2ETestEnv, cid string) {
	t.Helper()

	reqBody := map[string]string{"cid": cid}
	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", env.GatewayURL+"/v1/storage/unpin", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+env.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := env.HTTPClient.Do(req)
	if err != nil {
		t.Logf("warning: failed to unpin file: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Logf("warning: unpin file returned status %d", resp.StatusCode)
	}
}

// TestDeploymentWithHostHeader tests a deployment by setting the Host header
func TestDeploymentWithHostHeader(t *testing.T, env *E2ETestEnv, host, path string) *http.Response {
	t.Helper()

	req, err := http.NewRequest("GET", env.GatewayURL+path, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Host = host

	resp, err := env.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("failed to test deployment: %v", err)
	}

	return resp
}

// PutToOlric stores a key-value pair in Olric via the gateway HTTP API
func PutToOlric(gatewayURL, apiKey, dmap, key, value string) error {
	reqBody := map[string]interface{}{
		"dmap":  dmap,
		"key":   key,
		"value": value,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", gatewayURL+"/v1/cache/put", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("put failed with status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// GetFromOlric retrieves a value from Olric via the gateway HTTP API
func GetFromOlric(gatewayURL, apiKey, dmap, key string) (string, error) {
	reqBody := map[string]interface{}{
		"dmap": dmap,
		"key":  key,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", gatewayURL+"/v1/cache/get", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("key not found")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("get failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if value, ok := result["value"].(string); ok {
		return value, nil
	}
	if value, ok := result["value"]; ok {
		return fmt.Sprintf("%v", value), nil
	}
	return "", fmt.Errorf("value not found in response")
}

// WaitForHealthy waits for a deployment to become healthy
func WaitForHealthy(t *testing.T, env *E2ETestEnv, deploymentID string, timeout time.Duration) bool {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		deployment := GetDeployment(t, env, deploymentID)

		if status, ok := deployment["status"].(string); ok && status == "active" {
			return true
		}

		time.Sleep(1 * time.Second)
	}

	return false
}
