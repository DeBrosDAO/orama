package hostfunctions

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// StoragePut uploads data to IPFS and returns the CID.
func (h *HostFunctions) StoragePut(ctx context.Context, data []byte) (string, error) {
	if h.storage == nil {
		return "", &serverless.HostFunctionError{Function: "storage_put", Cause: serverless.ErrStorageUnavailable}
	}

	reader := bytes.NewReader(data)
	resp, err := h.storage.Add(ctx, reader, "function-data")
	if err != nil {
		return "", &serverless.HostFunctionError{Function: "storage_put", Cause: err}
	}

	return resp.Cid, nil
}

// StorageGet retrieves data from IPFS by CID.
func (h *HostFunctions) StorageGet(ctx context.Context, cid string) ([]byte, error) {
	if h.storage == nil {
		return nil, &serverless.HostFunctionError{Function: "storage_get", Cause: serverless.ErrStorageUnavailable}
	}

	reader, err := h.storage.Get(ctx, cid, h.ipfsAPIURL)
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "storage_get", Cause: err}
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "storage_get", Cause: fmt.Errorf("failed to read data: %w", err)}
	}

	return data, nil
}
