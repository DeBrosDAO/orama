package cache

import (
	"encoding/json"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/olric"
	olriclib "github.com/olric-data/olric"
)

// CacheHandlers provides HTTP handlers for Olric distributed cache operations.
// It encapsulates all cache-related endpoints including GET, PUT, DELETE, and SCAN operations.
type CacheHandlers struct {
	logger      *logging.ColoredLogger
	olricClient *olric.Client
}

// NewCacheHandlers creates a new CacheHandlers instance with the provided logger and Olric client.
func NewCacheHandlers(logger *logging.ColoredLogger, olricClient *olric.Client) *CacheHandlers {
	return &CacheHandlers{
		logger:      logger,
		olricClient: olricClient,
	}
}

// GetRequest represents the request body for cache GET operations.
type GetRequest struct {
	DMap string `json:"dmap"` // Distributed map name
	Key  string `json:"key"`  // Key to retrieve
}

// MultiGetRequest represents the request body for cache multi-GET operations.
type MultiGetRequest struct {
	DMap string   `json:"dmap"` // Distributed map name
	Keys []string `json:"keys"` // Keys to retrieve
}

// PutRequest represents the request body for cache PUT operations.
type PutRequest struct {
	DMap  string `json:"dmap"`  // Distributed map name
	Key   string `json:"key"`   // Key to store
	Value any    `json:"value"` // Value to store (can be any JSON-serializable type)
	TTL   string `json:"ttl"`   // Optional TTL (duration string like "1h", "30m")
}

// DeleteRequest represents the request body for cache DELETE operations.
type DeleteRequest struct {
	DMap string `json:"dmap"` // Distributed map name
	Key  string `json:"key"`  // Key to delete
}

// ScanRequest represents the request body for cache SCAN operations.
type ScanRequest struct {
	DMap  string `json:"dmap"`  // Distributed map name
	Match string `json:"match"` // Optional regex pattern to match keys
}

// decodeValueFromOlric decodes a value from Olric GetResponse.
// Handles JSON-serialized complex types and basic types (string, number, bool).
// This function attempts multiple strategies to decode the value:
// 1. First tries to get as bytes and unmarshal as JSON
// 2. Falls back to string if JSON unmarshal fails
// 3. Finally attempts to scan as any type
func decodeValueFromOlric(gr *olriclib.GetResponse) (any, error) {
	var value any

	// First, try to get as bytes (for JSON-serialized complex types)
	var bytesVal []byte
	if err := gr.Scan(&bytesVal); err == nil && len(bytesVal) > 0 {
		// Try to deserialize as JSON
		var jsonVal any
		if err := json.Unmarshal(bytesVal, &jsonVal); err == nil {
			value = jsonVal
		} else {
			// If JSON unmarshal fails, treat as string
			value = string(bytesVal)
		}
	} else {
		// Try as string (for simple string values)
		if strVal, err := gr.String(); err == nil {
			value = strVal
		} else {
			// Fallback: try to scan as any type
			var anyVal any
			if err := gr.Scan(&anyVal); err == nil {
				value = anyVal
			} else {
				// Last resort: try String() again, ignoring error
				strVal, _ := gr.String()
				value = strVal
			}
		}
	}

	return value, nil
}
