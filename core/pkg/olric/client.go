package olric

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	olriclib "github.com/olric-data/olric"
	"github.com/olric-data/olric/config"
	"go.uber.org/zap"
)

// OperationTimeout bounds every network round trip of the cluster client: dial
// aside, a read or a write that gets no answer within it fails.
//
// It is an I/O deadline because that is the only bound the client honours. The
// context a caller passes to Put or Get is not applied to the socket (olric
// v0.7.4 config/client.go builds its redis options without
// ContextTimeoutEnabled), so against an Olric that accepts the connection and
// never answers (a frozen process, a partition that drops packets) a call
// waited for the read timeout, twice with a retry, however short the caller's
// context was. Handlers' own 10s contexts are at least this long.
const OperationTimeout = 10 * time.Second

// Client wraps an Olric cluster client for distributed cache operations
type Client struct {
	client olriclib.Client
	logger *zap.Logger
}

// Config holds configuration for the Olric client
type Config struct {
	// Servers is a list of Olric server addresses (e.g., ["localhost:10102"]).
	// If empty, defaults to the index Olric on localhost.
	Servers []string

	// Timeout is the timeout for client operations. Zero, or anything above
	// OperationTimeout, means OperationTimeout.
	Timeout time.Duration
}

// NewClient creates a new Olric client wrapper
func NewClient(cfg Config, logger *zap.Logger) (*Client, error) {
	servers := cfg.Servers
	if len(servers) == 0 {
		servers = []string{fmt.Sprintf("localhost:%d", constants.OlricHTTPPort)}
	}

	clientCfg := clientConfig(cfg.Timeout)

	client, err := olriclib.NewClusterClient(servers, olriclib.WithConfig(clientCfg))
	if err != nil {
		return nil, fmt.Errorf("failed to create Olric cluster client: %w", err)
	}

	return &Client{
		client: client,
		logger: logger,
	}, nil
}

// clientConfig is the cluster client's configuration for a requested timeout.
// MaxRetries is -1 (none): a retry would double the wait on an unreachable
// member, and the gateway answers 503 and lets the caller retry.
func clientConfig(timeout time.Duration) *config.Client {
	if timeout <= 0 || timeout > OperationTimeout {
		timeout = OperationTimeout
	}
	return &config.Client{
		DialTimeout:    5 * time.Second,
		ReadTimeout:    timeout,
		WriteTimeout:   timeout,
		MaxRetries:     -1,
		Authentication: &config.Authentication{}, // Initialize to prevent nil pointer
	}
}

// UnderlyingClient returns the underlying olriclib.Client for advanced usage.
// This is useful when you need to pass the client to other packages that expect
// the raw olric client interface.
func (c *Client) UnderlyingClient() olriclib.Client {
	return c.client
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
