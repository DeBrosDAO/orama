package serverless

import (
	"context"
	"os"
	"testing"

	"go.uber.org/zap"
)

func TestEngine_Execute(t *testing.T) {
	logger := zap.NewNop()
	registry := NewMockRegistry()
	hostServices := NewMockHostServices()

	cfg := DefaultConfig()
	cfg.ModuleCacheSize = 2

	engine, err := NewEngine(cfg, registry, hostServices, logger)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Close(context.Background())

	// Use a minimal valid WASM module that exports _start (WASI)
	// This is just 'nop' in WASM
	wasmBytes := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x0a, 0x01, 0x06, 0x5f, 0x73, 0x74, 0x61, 0x72, 0x74, 0x00, 0x00,
		0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
	}

	fnDef := &FunctionDefinition{
		Name:           "test-func",
		Namespace:      "test-ns",
		MemoryLimitMB:  64,
		TimeoutSeconds: 5,
	}

	err = registry.Register(context.Background(), fnDef, wasmBytes)
	if err != nil {
		t.Fatalf("failed to register function: %v", err)
	}

	fn, err := registry.Get(context.Background(), "test-ns", "test-func", 0)
	if err != nil {
		t.Fatalf("failed to get function: %v", err)
	}

	// Execute function
	ctx := context.Background()
	output, err := engine.Execute(ctx, fn, []byte("input"), nil)
	if err != nil {
		t.Errorf("failed to execute function: %v", err)
	}

	// Our minimal WASM doesn't write to stdout, so output should be empty
	if len(output) != 0 {
		t.Errorf("expected empty output, got %d bytes", len(output))
	}

	// Test cache stats
	size, capacity := engine.GetCacheStats()
	if size != 1 {
		t.Errorf("expected cache size 1, got %d", size)
	}
	if capacity != 2 {
		t.Errorf("expected cache capacity 2, got %d", capacity)
	}

	// Test Invalidate
	engine.Invalidate(fn.WASMCID)
	size, _ = engine.GetCacheStats()
	if size != 0 {
		t.Errorf("expected cache size 0 after invalidation, got %d", size)
	}
}

func TestEngine_Precompile(t *testing.T) {
	logger := zap.NewNop()
	registry := NewMockRegistry()
	hostServices := NewMockHostServices()
	engine, _ := NewEngine(nil, registry, hostServices, logger)
	defer engine.Close(context.Background())

	wasmBytes := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x0a, 0x01, 0x06, 0x5f, 0x73, 0x74, 0x61, 0x72, 0x74, 0x00, 0x00,
		0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
	}

	err := engine.Precompile(context.Background(), "test-cid", wasmBytes)
	if err != nil {
		t.Fatalf("failed to precompile: %v", err)
	}

	size, _ := engine.GetCacheStats()
	if size != 1 {
		t.Errorf("expected cache size 1, got %d", size)
	}
}

func TestEngine_Timeout(t *testing.T) {
	logger := zap.NewNop()
	registry := NewMockRegistry()
	hostServices := NewMockHostServices()
	engine, _ := NewEngine(nil, registry, hostServices, logger)
	defer engine.Close(context.Background())

	wasmBytes := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x0a, 0x01, 0x06, 0x5f, 0x73, 0x74, 0x61, 0x72, 0x74, 0x00, 0x00,
		0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
	}

	fn, _ := registry.Get(context.Background(), "test", "timeout", 0)
	if fn == nil {
		_ = registry.Register(context.Background(), &FunctionDefinition{Name: "timeout", Namespace: "test"}, wasmBytes)
		fn, _ = registry.Get(context.Background(), "test", "timeout", 0)
	}
	fn.TimeoutSeconds = 1

	// Test with already canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := engine.Execute(ctx, fn, nil, nil)
	if err == nil {
		t.Error("expected error for canceled context, got nil")
	}
}

func TestEngine_MemoryLimit(t *testing.T) {
	logger := zap.NewNop()
	registry := NewMockRegistry()
	hostServices := NewMockHostServices()
	engine, _ := NewEngine(nil, registry, hostServices, logger)
	defer engine.Close(context.Background())

	wasmBytes := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x0a, 0x01, 0x06, 0x5f, 0x73, 0x74, 0x61, 0x72, 0x74, 0x00, 0x00,
		0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
	}

	_ = registry.Register(context.Background(), &FunctionDefinition{Name: "memory", Namespace: "test", MemoryLimitMB: 1, TimeoutSeconds: 5}, wasmBytes)
	fn, _ := registry.Get(context.Background(), "test", "memory", 0)

	// This should pass because the minimal WASM doesn't use much memory
	_, err := engine.Execute(context.Background(), fn, nil, nil)
	if err != nil {
		t.Errorf("expected success for minimal WASM within memory limit, got error: %v", err)
	}
}

func TestEngine_RealWASM(t *testing.T) {
	wasmPath := "../../examples/functions/bin/hello.wasm"
	if _, err := os.Stat(wasmPath); os.IsNotExist(err) {
		t.Skip("hello.wasm not found")
	}

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("failed to read hello.wasm: %v", err)
	}

	logger := zap.NewNop()
	registry := NewMockRegistry()
	hostServices := NewMockHostServices()
	engine, _ := NewEngine(nil, registry, hostServices, logger)
	defer engine.Close(context.Background())

	fnDef := &FunctionDefinition{
		Name:           "hello",
		Namespace:      "examples",
		TimeoutSeconds: 10,
	}
	_ = registry.Register(context.Background(), fnDef, wasmBytes)
	fn, _ := registry.Get(context.Background(), "examples", "hello", 0)

	output, err := engine.Execute(context.Background(), fn, []byte(`{"name": "Tester"}`), nil)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	expected := "Hello, Tester!"
	if !contains(string(output), expected) {
		t.Errorf("output %q does not contain %q", string(output), expected)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr))
}
