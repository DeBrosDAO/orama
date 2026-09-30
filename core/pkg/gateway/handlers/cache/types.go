package cache

import (
	"bytes"
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

// storedValueMarker starts every value this API stores. Olric keeps bytes with
// no type: the string "123", the number 123 and true (stored as 1) are
// indistinguishable there, which is how a value used to come back as something
// other than what was put. A stored value is therefore the marker followed by
// the value's JSON, and the JSON carries the type. The marker's NUL byte and
// version keep it from being taken for a value written before values were
// typed.
const storedValueMarker = "\x00orama.json.v1\x00"

// encodeStoredValue is the bytes a value is stored as: its JSON, typed.
func encodeStoredValue(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the value as JSON: %w", err)
	}
	return append([]byte(storedValueMarker), body...), nil
}

// decodeValueFromOlric returns the value exactly as it was put.
//
// A value without the marker was written before values were typed and holds
// raw text. Its type was never recorded, so it is read as what it looks like:
// JSON when it parses (objects and arrays were always stored as JSON, and a
// number as its digits), else a string. Only those entries are ambiguous; every
// entry written since is exact.
func decodeValueFromOlric(gr *olriclib.GetResponse) (any, error) {
	var raw []byte
	if err := gr.Scan(&raw); err != nil {
		return nil, fmt.Errorf("failed to read the stored value: %w", err)
	}
	if body, typed := bytes.CutPrefix(raw, []byte(storedValueMarker)); typed {
		var value any
		if err := json.Unmarshal(body, &value); err != nil {
			return nil, fmt.Errorf("the stored value is not valid JSON: %w", err)
		}
		return value, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw), nil
	}
	return value, nil
}
