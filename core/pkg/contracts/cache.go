package contracts

import (
	"context"
)

// CacheProvider defines the interface for distributed cache operations.
// Implementations provide a distributed key-value store with eventual consistency.
type CacheProvider interface {
	// Health checks if the cache service is operational.
	// Returns an error if the service is unavailable or cannot be reached.
	Health(ctx context.Context) error

	// Close gracefully shuts down the cache client and releases resources.
	Close(ctx context.Context) error
}

// CacheClient provides extended cache operations beyond basic connectivity.
// This interface is intentionally kept minimal as cache operations are
// typically accessed through the underlying client's DMap API.
type CacheClient interface {
	CacheProvider

	// UnderlyingClient returns the native cache client for advanced operations.
	// The returned client can be used to access DMap operations like Get, Put, Delete, etc.
	// Return type is interface{} to avoid leaking concrete implementation details.
	UnderlyingClient() interface{}
}
