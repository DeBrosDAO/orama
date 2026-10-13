package cache

import (
	"encoding/json"
	"fmt"

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

// encodeStoredValue is the bytes a value is stored as: its JSON. Olric keeps
// bytes with no type, and values used to be stored as their text, so the string
// "123", the number 123 and true were indistinguishable there and a value came
// back as something other than what was put. The JSON carries the type: the
// string is stored as "123", with its quotes.
//
// Plain JSON rather than a tagged format on purpose: a gateway from before this
// change reads a value by parsing it as JSON, so it reads these exactly too,
// and the gateways of a rolling upgrade (or a rollback) agree on every entry.
func encodeStoredValue(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the value as JSON: %w", err)
	}
	return body, nil
}

// decodeValueFromOlric returns the value as it was put.
//
// Every value written since values were typed is JSON and reads back exactly.
// A value written before then holds raw text whose type was never recorded: it
// is read as what it looks like, JSON when it parses, else a string. Only those
// older entries are ambiguous.
func decodeValueFromOlric(gr *olriclib.GetResponse) (any, error) {
	var raw []byte
	if err := gr.Scan(&raw); err != nil {
		return nil, fmt.Errorf("failed to read the stored value: %w", err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw), nil
	}
	return value, nil
}
