//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
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

// loadNodeConfig loads node configuration from ~/.orama/node.yaml or bootstrap.yaml
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

	// Try bootstrap.yaml first, then all node variants
	for _, cfgFile := range []string{"bootstrap.yaml", "bootstrap2.yaml", "node.yaml", "node2.yaml", "node3.yaml", "node4.yaml"} {
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
	// Build database path from bootstrap/node config
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	// Try bootstrap first, then all nodes
	dbPaths := []string{
		filepath.Join(homeDir, ".orama", "bootstrap", "rqlite", "db.sqlite"),
		filepath.Join(homeDir, ".orama", "bootstrap2", "rqlite", "db.sqlite"),
		filepath.Join(homeDir, ".orama", "node2", "rqlite", "db.sqlite"),
		filepath.Join(homeDir, ".orama", "node3", "rqlite", "db.sqlite"),
		filepath.Join(homeDir, ".orama", "node4", "rqlite", "db.sqlite"),
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

	configFiles := []string{"bootstrap.yaml", "bootstrap2.yaml", "node.yaml", "node2.yaml", "node3.yaml", "node4.yaml"}
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
	for _, cfgFile := range []string{"bootstrap.yaml", "bootstrap2.yaml", "node.yaml", "node2.yaml", "node3.yaml", "node4.yaml"} {
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
	for _, cfgFile := range []string{"bootstrap.yaml", "bootstrap2.yaml", "node.yaml", "node2.yaml", "node3.yaml", "node4.yaml"} {
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
	for _, cfgFile := range []string{"bootstrap.yaml", "bootstrap2.yaml", "node.yaml", "node2.yaml", "node3.yaml", "node4.yaml"} {
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

	resp, err := http.DefaultClient.Do(req)
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
	resp, err := http.DefaultClient.Do(req)
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
	return &http.Client{Timeout: timeout}
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

	dbPath := filepath.Join(homeDir, ".orama", "bootstrap", "rqlite", "db.sqlite")
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
