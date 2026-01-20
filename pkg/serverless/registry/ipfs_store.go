package registry

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"go.uber.org/zap"
)

// IPFSStore handles IPFS storage operations for WASM bytecode.
type IPFSStore struct {
	ipfs       ipfs.IPFSClient
	ipfsAPIURL string
	logger     *zap.Logger
}

// NewIPFSStore creates a new IPFS store.
func NewIPFSStore(ipfsClient ipfs.IPFSClient, ipfsAPIURL string, logger *zap.Logger) *IPFSStore {
	return &IPFSStore{
		ipfs:       ipfsClient,
		ipfsAPIURL: ipfsAPIURL,
		logger:     logger,
	}
}

// Upload uploads WASM bytecode to IPFS and returns the CID.
func (s *IPFSStore) Upload(ctx context.Context, wasmBytes []byte, name string) (string, error) {
	reader := bytes.NewReader(wasmBytes)
	resp, err := s.ipfs.Add(ctx, reader, name+".wasm")
	if err != nil {
		return "", fmt.Errorf("failed to upload WASM to IPFS: %w", err)
	}
	return resp.Cid, nil
}

// Get retrieves WASM bytecode from IPFS by CID.
func (s *IPFSStore) Get(ctx context.Context, wasmCID string) ([]byte, error) {
	if wasmCID == "" {
		return nil, fmt.Errorf("wasmCID cannot be empty")
	}

	reader, err := s.ipfs.Get(ctx, wasmCID, s.ipfsAPIURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get WASM from IPFS: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read WASM data: %w", err)
	}

	return data, nil
}
