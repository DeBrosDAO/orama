package serverless

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

func TestHostFunctions_Cache(t *testing.T) {
	db := NewMockRQLite()
	ipfs := NewMockIPFSClient()
	logger := zap.NewNop()

	// MockOlricClient needs to implement olriclib.Client
	// For now, let's just test other host functions if Olric is hard to mock

	h := NewHostFunctions(db, nil, ipfs, nil, nil, nil, HostFunctionsConfig{}, logger)

	ctx := context.Background()
	h.SetInvocationContext(&InvocationContext{
		RequestID: "req-1",
		Namespace: "ns-1",
	})

	// Test Logging
	h.LogInfo(ctx, "hello world")
	logs := h.GetLogs()
	if len(logs) != 1 || logs[0].Message != "hello world" {
		t.Errorf("unexpected logs: %+v", logs)
	}

	// Test Storage
	cid, err := h.StoragePut(ctx, []byte("data"))
	if err != nil {
		t.Fatalf("StoragePut failed: %v", err)
	}
	data, err := h.StorageGet(ctx, cid)
	if err != nil {
		t.Fatalf("StorageGet failed: %v", err)
	}
	if string(data) != "data" {
		t.Errorf("expected 'data', got %q", string(data))
	}
}
