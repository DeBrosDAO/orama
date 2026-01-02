package serverless

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"
)

// Ensure HostFunctions implements HostServices interface.
var _ HostServices = (*HostFunctions)(nil)

// HostFunctions provides the bridge between WASM functions and Orama services.
// It implements the HostServices interface and is injected into the execution context.
type HostFunctions struct {
	db          rqlite.Client
	cacheClient olriclib.Client
	storage     ipfs.IPFSClient
	ipfsAPIURL  string
	pubsub      *pubsub.ClientAdapter
	wsManager   WebSocketManager
	secrets     SecretsManager
	httpClient  *http.Client
	logger      *zap.Logger

	// Current invocation context (set per-execution)
	invCtx     *InvocationContext
	invCtxLock sync.RWMutex

	// Captured logs for this invocation
	logs     []LogEntry
	logsLock sync.Mutex
}

// HostFunctionsConfig holds configuration for HostFunctions.
type HostFunctionsConfig struct {
	IPFSAPIURL  string
	HTTPTimeout time.Duration
}

// NewHostFunctions creates a new HostFunctions instance.
func NewHostFunctions(
	db rqlite.Client,
	cacheClient olriclib.Client,
	storage ipfs.IPFSClient,
	pubsubAdapter *pubsub.ClientAdapter,
	wsManager WebSocketManager,
	secrets SecretsManager,
	cfg HostFunctionsConfig,
	logger *zap.Logger,
) *HostFunctions {
	httpTimeout := cfg.HTTPTimeout
	if httpTimeout == 0 {
		httpTimeout = 30 * time.Second
	}

	return &HostFunctions{
		db:          db,
		cacheClient: cacheClient,
		storage:     storage,
		ipfsAPIURL:  cfg.IPFSAPIURL,
		pubsub:      pubsubAdapter,
		wsManager:   wsManager,
		secrets:     secrets,
		httpClient:  tlsutil.NewHTTPClient(httpTimeout),
		logger:      logger,
		logs:        make([]LogEntry, 0),
	}
}

// SetInvocationContext sets the current invocation context.
// Must be called before executing a function.
func (h *HostFunctions) SetInvocationContext(invCtx *InvocationContext) {
	h.invCtxLock.Lock()
	defer h.invCtxLock.Unlock()
	h.invCtx = invCtx
	h.logs = make([]LogEntry, 0) // Reset logs for new invocation
}

// GetLogs returns the captured logs for the current invocation.
func (h *HostFunctions) GetLogs() []LogEntry {
	h.logsLock.Lock()
	defer h.logsLock.Unlock()
	logsCopy := make([]LogEntry, len(h.logs))
	copy(logsCopy, h.logs)
	return logsCopy
}

// ClearContext clears the invocation context after execution.
func (h *HostFunctions) ClearContext() {
	h.invCtxLock.Lock()
	defer h.invCtxLock.Unlock()
	h.invCtx = nil
}

// -----------------------------------------------------------------------------
// Database Operations
// -----------------------------------------------------------------------------

// DBQuery executes a SELECT query and returns JSON-encoded results.
func (h *HostFunctions) DBQuery(ctx context.Context, query string, args []interface{}) ([]byte, error) {
	if h.db == nil {
		return nil, &HostFunctionError{Function: "db_query", Cause: ErrDatabaseUnavailable}
	}

	var results []map[string]interface{}
	if err := h.db.Query(ctx, &results, query, args...); err != nil {
		return nil, &HostFunctionError{Function: "db_query", Cause: err}
	}

	data, err := json.Marshal(results)
	if err != nil {
		return nil, &HostFunctionError{Function: "db_query", Cause: fmt.Errorf("failed to marshal results: %w", err)}
	}

	return data, nil
}

// DBExecute executes an INSERT/UPDATE/DELETE query and returns affected rows.
func (h *HostFunctions) DBExecute(ctx context.Context, query string, args []interface{}) (int64, error) {
	if h.db == nil {
		return 0, &HostFunctionError{Function: "db_execute", Cause: ErrDatabaseUnavailable}
	}

	result, err := h.db.Exec(ctx, query, args...)
	if err != nil {
		return 0, &HostFunctionError{Function: "db_execute", Cause: err}
	}

	affected, _ := result.RowsAffected()
	return affected, nil
}

// -----------------------------------------------------------------------------
// Cache Operations
// -----------------------------------------------------------------------------

const cacheDMapName = "serverless_cache"

// CacheGet retrieves a value from the cache.
func (h *HostFunctions) CacheGet(ctx context.Context, key string) ([]byte, error) {
	if h.cacheClient == nil {
		return nil, &HostFunctionError{Function: "cache_get", Cause: ErrCacheUnavailable}
	}

	dm, err := h.cacheClient.NewDMap(cacheDMapName)
	if err != nil {
		return nil, &HostFunctionError{Function: "cache_get", Cause: fmt.Errorf("failed to get DMap: %w", err)}
	}

	result, err := dm.Get(ctx, key)
	if err != nil {
		return nil, &HostFunctionError{Function: "cache_get", Cause: err}
	}

	value, err := result.Byte()
	if err != nil {
		return nil, &HostFunctionError{Function: "cache_get", Cause: fmt.Errorf("failed to decode value: %w", err)}
	}

	return value, nil
}

// CacheSet stores a value in the cache with optional TTL.
// Note: TTL is currently not supported by the underlying Olric DMap.Put method.
// Values are stored indefinitely until explicitly deleted.
func (h *HostFunctions) CacheSet(ctx context.Context, key string, value []byte, ttlSeconds int64) error {
	if h.cacheClient == nil {
		return &HostFunctionError{Function: "cache_set", Cause: ErrCacheUnavailable}
	}

	dm, err := h.cacheClient.NewDMap(cacheDMapName)
	if err != nil {
		return &HostFunctionError{Function: "cache_set", Cause: fmt.Errorf("failed to get DMap: %w", err)}
	}

	// Note: Olric DMap.Put doesn't support TTL in the basic API
	// For TTL support, consider using Olric's Expire API separately
	if err := dm.Put(ctx, key, value); err != nil {
		return &HostFunctionError{Function: "cache_set", Cause: err}
	}

	return nil
}

// CacheDelete removes a value from the cache.
func (h *HostFunctions) CacheDelete(ctx context.Context, key string) error {
	if h.cacheClient == nil {
		return &HostFunctionError{Function: "cache_delete", Cause: ErrCacheUnavailable}
	}

	dm, err := h.cacheClient.NewDMap(cacheDMapName)
	if err != nil {
		return &HostFunctionError{Function: "cache_delete", Cause: fmt.Errorf("failed to get DMap: %w", err)}
	}

	if _, err := dm.Delete(ctx, key); err != nil {
		return &HostFunctionError{Function: "cache_delete", Cause: err}
	}

	return nil
}

// -----------------------------------------------------------------------------
// Storage Operations
// -----------------------------------------------------------------------------

// StoragePut uploads data to IPFS and returns the CID.
func (h *HostFunctions) StoragePut(ctx context.Context, data []byte) (string, error) {
	if h.storage == nil {
		return "", &HostFunctionError{Function: "storage_put", Cause: ErrStorageUnavailable}
	}

	reader := bytes.NewReader(data)
	resp, err := h.storage.Add(ctx, reader, "function-data")
	if err != nil {
		return "", &HostFunctionError{Function: "storage_put", Cause: err}
	}

	return resp.Cid, nil
}

// StorageGet retrieves data from IPFS by CID.
func (h *HostFunctions) StorageGet(ctx context.Context, cid string) ([]byte, error) {
	if h.storage == nil {
		return nil, &HostFunctionError{Function: "storage_get", Cause: ErrStorageUnavailable}
	}

	reader, err := h.storage.Get(ctx, cid, h.ipfsAPIURL)
	if err != nil {
		return nil, &HostFunctionError{Function: "storage_get", Cause: err}
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, &HostFunctionError{Function: "storage_get", Cause: fmt.Errorf("failed to read data: %w", err)}
	}

	return data, nil
}

// -----------------------------------------------------------------------------
// PubSub Operations
// -----------------------------------------------------------------------------

// PubSubPublish publishes a message to a topic.
func (h *HostFunctions) PubSubPublish(ctx context.Context, topic string, data []byte) error {
	if h.pubsub == nil {
		return &HostFunctionError{Function: "pubsub_publish", Cause: fmt.Errorf("pubsub not available")}
	}

	// The pubsub adapter handles namespacing internally
	if err := h.pubsub.Publish(ctx, topic, data); err != nil {
		return &HostFunctionError{Function: "pubsub_publish", Cause: err}
	}

	return nil
}

// -----------------------------------------------------------------------------
// WebSocket Operations
// -----------------------------------------------------------------------------

// WSSend sends data to a specific WebSocket client.
func (h *HostFunctions) WSSend(ctx context.Context, clientID string, data []byte) error {
	if h.wsManager == nil {
		return &HostFunctionError{Function: "ws_send", Cause: ErrWSNotAvailable}
	}

	// If no clientID provided, use the current invocation's client
	if clientID == "" {
		h.invCtxLock.RLock()
		if h.invCtx != nil && h.invCtx.WSClientID != "" {
			clientID = h.invCtx.WSClientID
		}
		h.invCtxLock.RUnlock()
	}

	if clientID == "" {
		return &HostFunctionError{Function: "ws_send", Cause: ErrWSNotAvailable}
	}

	if err := h.wsManager.Send(clientID, data); err != nil {
		return &HostFunctionError{Function: "ws_send", Cause: err}
	}

	return nil
}

// WSBroadcast sends data to all WebSocket clients subscribed to a topic.
func (h *HostFunctions) WSBroadcast(ctx context.Context, topic string, data []byte) error {
	if h.wsManager == nil {
		return &HostFunctionError{Function: "ws_broadcast", Cause: ErrWSNotAvailable}
	}

	if err := h.wsManager.Broadcast(topic, data); err != nil {
		return &HostFunctionError{Function: "ws_broadcast", Cause: err}
	}

	return nil
}

// -----------------------------------------------------------------------------
// HTTP Operations
// -----------------------------------------------------------------------------

// HTTPFetch makes an outbound HTTP request.
func (h *HostFunctions) HTTPFetch(ctx context.Context, method, url string, headers map[string]string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		h.logger.Error("http_fetch request creation error", zap.Error(err), zap.String("url", url))
		errorResp := map[string]interface{}{
			"error":  "failed to create request: " + err.Error(),
			"status": 0,
		}
		return json.Marshal(errorResp)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.logger.Error("http_fetch transport error", zap.Error(err), zap.String("url", url))
		errorResp := map[string]interface{}{
			"error":  err.Error(),
			"status": 0, // Transport error
		}
		return json.Marshal(errorResp)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		h.logger.Error("http_fetch response read error", zap.Error(err), zap.String("url", url))
		errorResp := map[string]interface{}{
			"error":  "failed to read response: " + err.Error(),
			"status": resp.StatusCode,
		}
		return json.Marshal(errorResp)
	}

	// Encode response with status code
	response := map[string]interface{}{
		"status":  resp.StatusCode,
		"headers": resp.Header,
		"body":    string(respBody),
	}

	data, err := json.Marshal(response)
	if err != nil {
		return nil, &HostFunctionError{Function: "http_fetch", Cause: fmt.Errorf("failed to marshal response: %w", err)}
	}

	return data, nil
}

// -----------------------------------------------------------------------------
// Context Operations
// -----------------------------------------------------------------------------

// GetEnv retrieves an environment variable for the function.
func (h *HostFunctions) GetEnv(ctx context.Context, key string) (string, error) {
	h.invCtxLock.RLock()
	defer h.invCtxLock.RUnlock()

	if h.invCtx == nil || h.invCtx.EnvVars == nil {
		return "", nil
	}

	return h.invCtx.EnvVars[key], nil
}

// GetSecret retrieves a decrypted secret.
func (h *HostFunctions) GetSecret(ctx context.Context, name string) (string, error) {
	if h.secrets == nil {
		return "", &HostFunctionError{Function: "get_secret", Cause: fmt.Errorf("secrets manager not available")}
	}

	h.invCtxLock.RLock()
	namespace := ""
	if h.invCtx != nil {
		namespace = h.invCtx.Namespace
	}
	h.invCtxLock.RUnlock()

	value, err := h.secrets.Get(ctx, namespace, name)
	if err != nil {
		return "", &HostFunctionError{Function: "get_secret", Cause: err}
	}

	return value, nil
}

// GetRequestID returns the current request ID.
func (h *HostFunctions) GetRequestID(ctx context.Context) string {
	h.invCtxLock.RLock()
	defer h.invCtxLock.RUnlock()

	if h.invCtx == nil {
		return ""
	}
	return h.invCtx.RequestID
}

// GetCallerWallet returns the wallet address of the caller.
func (h *HostFunctions) GetCallerWallet(ctx context.Context) string {
	h.invCtxLock.RLock()
	defer h.invCtxLock.RUnlock()

	if h.invCtx == nil {
		return ""
	}
	return h.invCtx.CallerWallet
}

// -----------------------------------------------------------------------------
// Job Operations
// -----------------------------------------------------------------------------

// EnqueueBackground queues a function for background execution.
func (h *HostFunctions) EnqueueBackground(ctx context.Context, functionName string, payload []byte) (string, error) {
	// This will be implemented when JobManager is integrated
	// For now, return an error indicating it's not yet available
	return "", &HostFunctionError{Function: "enqueue_background", Cause: fmt.Errorf("background jobs not yet implemented")}
}

// ScheduleOnce schedules a function to run once at a specific time.
func (h *HostFunctions) ScheduleOnce(ctx context.Context, functionName string, runAt time.Time, payload []byte) (string, error) {
	// This will be implemented when Scheduler is integrated
	return "", &HostFunctionError{Function: "schedule_once", Cause: fmt.Errorf("timers not yet implemented")}
}

// -----------------------------------------------------------------------------
// Logging Operations
// -----------------------------------------------------------------------------

// LogInfo logs an info message.
func (h *HostFunctions) LogInfo(ctx context.Context, message string) {
	h.logsLock.Lock()
	defer h.logsLock.Unlock()

	h.logs = append(h.logs, LogEntry{
		Level:     "info",
		Message:   message,
		Timestamp: time.Now(),
	})

	h.logger.Info(message,
		zap.String("request_id", h.GetRequestID(ctx)),
		zap.String("level", "function"),
	)
}

// LogError logs an error message.
func (h *HostFunctions) LogError(ctx context.Context, message string) {
	h.logsLock.Lock()
	defer h.logsLock.Unlock()

	h.logs = append(h.logs, LogEntry{
		Level:     "error",
		Message:   message,
		Timestamp: time.Now(),
	})

	h.logger.Error(message,
		zap.String("request_id", h.GetRequestID(ctx)),
		zap.String("level", "function"),
	)
}

// -----------------------------------------------------------------------------
// Secrets Manager Implementation (built-in)
// -----------------------------------------------------------------------------

// DBSecretsManager implements SecretsManager using the database.
type DBSecretsManager struct {
	db            rqlite.Client
	encryptionKey []byte // 32-byte AES-256 key
	logger        *zap.Logger
}

// Ensure DBSecretsManager implements SecretsManager.
var _ SecretsManager = (*DBSecretsManager)(nil)

// NewDBSecretsManager creates a secrets manager backed by the database.
func NewDBSecretsManager(db rqlite.Client, encryptionKeyHex string, logger *zap.Logger) (*DBSecretsManager, error) {
	var key []byte
	if encryptionKeyHex != "" {
		var err error
		key, err = hex.DecodeString(encryptionKeyHex)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("invalid encryption key: must be 32 bytes hex-encoded")
		}
	} else {
		// Generate a random key if none provided
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("failed to generate encryption key: %w", err)
		}
		logger.Warn("Generated random secrets encryption key - secrets will not persist across restarts")
	}

	return &DBSecretsManager{
		db:            db,
		encryptionKey: key,
		logger:        logger,
	}, nil
}

// Set stores an encrypted secret.
func (s *DBSecretsManager) Set(ctx context.Context, namespace, name, value string) error {
	encrypted, err := s.encrypt([]byte(value))
	if err != nil {
		return fmt.Errorf("failed to encrypt secret: %w", err)
	}

	// Upsert the secret
	query := `
		INSERT INTO function_secrets (id, namespace, name, encrypted_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(namespace, name) DO UPDATE SET
			encrypted_value = excluded.encrypted_value,
			updated_at = excluded.updated_at
	`

	id := fmt.Sprintf("%s:%s", namespace, name)
	now := time.Now()
	if _, err := s.db.Exec(ctx, query, id, namespace, name, encrypted, now, now); err != nil {
		return fmt.Errorf("failed to save secret: %w", err)
	}

	return nil
}

// Get retrieves a decrypted secret.
func (s *DBSecretsManager) Get(ctx context.Context, namespace, name string) (string, error) {
	query := `SELECT encrypted_value FROM function_secrets WHERE namespace = ? AND name = ?`

	var rows []struct {
		EncryptedValue []byte `db:"encrypted_value"`
	}
	if err := s.db.Query(ctx, &rows, query, namespace, name); err != nil {
		return "", fmt.Errorf("failed to query secret: %w", err)
	}

	if len(rows) == 0 {
		return "", ErrSecretNotFound
	}

	decrypted, err := s.decrypt(rows[0].EncryptedValue)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt secret: %w", err)
	}

	return string(decrypted), nil
}

// List returns all secret names for a namespace.
func (s *DBSecretsManager) List(ctx context.Context, namespace string) ([]string, error) {
	query := `SELECT name FROM function_secrets WHERE namespace = ? ORDER BY name`

	var rows []struct {
		Name string `db:"name"`
	}
	if err := s.db.Query(ctx, &rows, query, namespace); err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row.Name
	}

	return names, nil
}

// Delete removes a secret.
func (s *DBSecretsManager) Delete(ctx context.Context, namespace, name string) error {
	query := `DELETE FROM function_secrets WHERE namespace = ? AND name = ?`

	result, err := s.db.Exec(ctx, query, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrSecretNotFound
	}

	return nil
}

// encrypt encrypts data using AES-256-GCM.
func (s *DBSecretsManager) encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt decrypts data using AES-256-GCM.
func (s *DBSecretsManager) decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
