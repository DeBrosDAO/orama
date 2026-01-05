package serverless

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	db := NewMockRQLite()
	ipfs := NewMockIPFSClient()
	logger := zap.NewNop()

	registry := NewRegistry(db, ipfs, RegistryConfig{IPFSAPIURL: "http://localhost:5001"}, logger)

	ctx := context.Background()
	fnDef := &FunctionDefinition{
		Name:      "test-func",
		Namespace: "test-ns",
		IsPublic:  true,
	}
	wasmBytes := []byte("mock wasm")

	_, err := registry.Register(ctx, fnDef, wasmBytes)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Since MockRQLite doesn't fully implement Query scanning yet,
	// we won't be able to test Get() effectively without more work.
	// But we can check if wasm was uploaded.
	wasm, err := registry.GetWASMBytes(ctx, "cid-test-func.wasm")
	if err != nil {
		t.Fatalf("GetWASMBytes failed: %v", err)
	}
	if string(wasm) != "mock wasm" {
		t.Errorf("expected 'mock wasm', got %q", string(wasm))
	}
}
