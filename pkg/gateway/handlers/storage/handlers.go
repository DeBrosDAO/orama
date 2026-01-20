package storage

import (
	"context"
	"io"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// IPFSClient defines the interface for interacting with IPFS.
// This interface matches the ipfs.IPFSClient implementation.
type IPFSClient interface {
	Add(ctx context.Context, reader io.Reader, name string) (*ipfs.AddResponse, error)
	Pin(ctx context.Context, cid string, name string, replicationFactor int) (*ipfs.PinResponse, error)
	PinStatus(ctx context.Context, cid string) (*ipfs.PinStatus, error)
	Get(ctx context.Context, cid string, ipfsAPIURL string) (io.ReadCloser, error)
	Unpin(ctx context.Context, cid string) error
}

// Config holds configuration values needed by storage handlers.
type Config struct {
	// IPFSReplicationFactor is the desired number of replicas for pinned content
	IPFSReplicationFactor int
	// IPFSAPIURL is the IPFS API endpoint URL
	IPFSAPIURL string
}

// Handlers provides HTTP handlers for IPFS storage operations.
// It manages file uploads, downloads, pinning, and status checking.
type Handlers struct {
	ipfsClient IPFSClient
	logger     *logging.ColoredLogger
	config     Config
}

// New creates a new storage handlers instance with the provided dependencies.
func New(ipfsClient IPFSClient, logger *logging.ColoredLogger, config Config) *Handlers {
	return &Handlers{
		ipfsClient: ipfsClient,
		logger:     logger,
		config:     config,
	}
}

// getNamespaceFromContext retrieves the namespace from the request context.
func (h *Handlers) getNamespaceFromContext(ctx context.Context) string {
	if v := ctx.Value(ctxkeys.NamespaceOverride); v != nil {
		if ns, ok := v.(string); ok {
			return ns
		}
	}
	return ""
}
