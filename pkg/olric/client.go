package olric

import (
	"context"
	"fmt"
	"time"

	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"
)

// Client wraps an Olric cluster client for distributed cache operations
type Client struct {
	client olriclib.Client
	logger *zap.Logger
}

// Config holds configuration for the Olric client
type Config struct {
	// Servers is a list of Olric server addresses (e.g., ["localhost:3320"])
	// If empty, defaults to ["localhost:3320"]
	Servers []string

	// Timeout is the timeout for client operations
	// If zero, defaults to 10 seconds
	Timeout time.Duration
}

// NewClient creates a new Olric client wrapper
func NewClient(cfg Config, logger *zap.Logger) (*Client, error) {
	servers := cfg.Servers
	if len(servers) == 0 {
		servers = []string{"localhost:3320"}
	}

	client, err := olriclib.NewClusterClient(servers)
	if err != nil {
		return nil, fmt.Errorf("failed to create Olric cluster client: %w", err)
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	return &Client{
		client: client,
		logger: logger,
	}, nil
}

// Health checks if the Olric client is healthy
func (c *Client) Health(ctx context.Context) error {
	// Create a DMap to test connectivity
	dm, err := c.client.NewDMap("_health_check")
	if err != nil {
		return fmt.Errorf("failed to create DMap for health check: %w", err)
	}

	// Try a simple put/get operation
	testKey := fmt.Sprintf("_health_%d", time.Now().UnixNano())
	testValue := "ok"

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err = dm.Put(ctx, testKey, testValue)
	if err != nil {
		return fmt.Errorf("health check put failed: %w", err)
	}

	gr, err := dm.Get(ctx, testKey)
	if err != nil {
		return fmt.Errorf("health check get failed: %w", err)
	}

	val, err := gr.String()
	if err != nil {
		return fmt.Errorf("health check value decode failed: %w", err)
	}

	if val != testValue {
		return fmt.Errorf("health check value mismatch: expected %q, got %q", testValue, val)
	}

	// Clean up test key
	_, _ = dm.Delete(ctx, testKey)

	return nil
}

// Close closes the Olric client connection
func (c *Client) Close(ctx context.Context) error {
	if c.client == nil {
		return nil
	}
	return c.client.Close(ctx)
}

// GetClient returns the underlying Olric client
func (c *Client) GetClient() olriclib.Client {
	return c.client
}
