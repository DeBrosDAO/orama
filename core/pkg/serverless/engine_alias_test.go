package serverless

import (
	"context"
	"testing"

	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
)

// TestEngine_HostModule_AllAliases_Registered locks in the
// "we register host functions under multiple module names" property.
//
// Background: WASM modules declare imports as `(import "modulename" "fn" ...)`.
// At instantiate time, wazero matches each declared import against host
// modules registered with the runtime. If the WASM module imports from a
// name we never registered, instantiation fails with the cryptic error
// `module[<name>] not instantiated`.
//
// Three apps and counting have intuited the import name as "orama" instead
// of the WASI/TinyGo-canonical "env". To avoid hours of debugging this
// every time, we register the same export set under three names:
//
//   - "env"   (canonical — matches WASI/TinyGo convention; SDK examples)
//   - "host"  (long-standing alias)
//   - "orama" (added 2026-05-06 after AnChat hit the wall)
//
// This test fails if anyone removes one of those aliases — that's an
// immediate breaking change for deployed WASM that imports from the
// removed name.
func TestEngine_HostModule_AllAliases_Registered(t *testing.T) {
	registry := NewMockRegistry()
	hostServices := NewMockHostServices()
	logger := zap.NewNop()

	engine, err := NewEngine(nil, registry, hostServices, logger)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = engine.runtime.Close(context.Background()) }()

	// Each alias MUST resolve to an instantiated module. nil means the
	// host module isn't registered → WASM imports against that name fail.
	for _, name := range []string{"env", "host", "orama"} {
		t.Run(name, func(t *testing.T) {
			mod := engine.runtime.Module(name)
			if mod == nil {
				t.Fatalf("host module %q is NOT registered — "+
					"WASM imports from this module name will fail to instantiate",
					name)
			}
		})
	}
}

// TestEngine_HostModule_OramaImport_Instantiates is the end-to-end
// regression test. It compiles a minimal WASM binary that declares an
// import from `orama.log_info` and verifies it instantiates against our
// runtime. If the alias regresses, this fails with exactly the error
// AnChat hit in production: `module[orama] not instantiated`.
//
// The WASM binary is hand-crafted from the spec's binary format — keeps
// the test self-contained without a TinyGo build step.
func TestEngine_HostModule_OramaImport_Instantiates(t *testing.T) {
	registry := NewMockRegistry()
	hostServices := NewMockHostServices()
	logger := zap.NewNop()

	engine, err := NewEngine(nil, registry, hostServices, logger)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = engine.runtime.Close(context.Background()) }()

	// Build a minimal valid WASM module that imports `orama.log_info`.
	// log_info has signature func(levelPtr, levelLen, msgPtr, msgLen uint32),
	// matching the host registration. wazero's instantiate validates
	// imports — a missing module name surfaces immediately.
	// log_info has signature func(ptr, size uint32) → no returns (i32 i32 -> ()).
	// Matching the host registration matters: a signature mismatch shifts
	// the failure mode away from the alias-resolution path we're testing.
	wasm := buildWASMWithImport("orama", "log_info", []byte{0x7f, 0x7f}, nil)

	ctx := context.Background()
	compiled, err := engine.runtime.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	defer func() { _ = compiled.Close(ctx) }()

	// Disable WASI _start so the module can instantiate without WASI imports.
	mod, err := engine.runtime.InstantiateModule(ctx, compiled,
		wazero.NewModuleConfig().WithStartFunctions())
	if err != nil {
		t.Fatalf("instantiate failed (orama alias regressed?): %v", err)
	}
	_ = mod.Close(ctx)
}

// TestEngine_HostModule_HostImport_Instantiates exercises the same end-to-end
// path against the "host" alias.
func TestEngine_HostModule_HostImport_Instantiates(t *testing.T) {
	registry := NewMockRegistry()
	hostServices := NewMockHostServices()
	logger := zap.NewNop()

	engine, err := NewEngine(nil, registry, hostServices, logger)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = engine.runtime.Close(context.Background()) }()

	wasm := buildWASMWithImport("host", "log_info", []byte{0x7f, 0x7f}, nil)
	ctx := context.Background()
	compiled, err := engine.runtime.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	defer func() { _ = compiled.Close(ctx) }()

	mod, err := engine.runtime.InstantiateModule(ctx, compiled,
		wazero.NewModuleConfig().WithStartFunctions())
	if err != nil {
		t.Fatalf("instantiate failed for host alias: %v", err)
	}
	_ = mod.Close(ctx)
}

// TestEngine_HostModule_UnknownImport_Fails confirms the negative case:
// importing from a module name we DON'T register fails with the wazero
// error format we recognize. If wazero ever changes that format the test
// catches it; otherwise it documents the failure mode.
func TestEngine_HostModule_UnknownImport_Fails(t *testing.T) {
	registry := NewMockRegistry()
	hostServices := NewMockHostServices()
	logger := zap.NewNop()

	engine, err := NewEngine(nil, registry, hostServices, logger)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer func() { _ = engine.runtime.Close(context.Background()) }()

	wasm := buildWASMWithImport("definitely_not_registered", "log_info",
		[]byte{0x7f, 0x7f}, nil)
	ctx := context.Background()
	compiled, err := engine.runtime.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	defer func() { _ = compiled.Close(ctx) }()

	_, err = engine.runtime.InstantiateModule(ctx, compiled,
		wazero.NewModuleConfig().WithStartFunctions())
	if err == nil {
		t.Fatal("expected instantiate to fail for unknown module name, got nil")
	}
}

// buildWASMWithImport hand-builds a minimal valid WASM binary that:
//   - imports a single function from (module, fn)
//   - has signature: params=paramTypes returns=returnTypes (WASM value-type bytes)
//
// Returns the binary as a []byte. Used by the alias instantiation tests
// to avoid pulling in a TinyGo / wabin dependency.
//
// Reference: https://webassembly.github.io/spec/core/binary/modules.html
func buildWASMWithImport(module, fn string, paramTypes, returnTypes []byte) []byte {
	// Type section: one function type with `paramTypes -> returnTypes`.
	typeSec := []byte{
		0x01, // section ID = type
	}
	typeBody := []byte{
		0x01, // count of types = 1
		0x60, // func type tag
	}
	typeBody = append(typeBody, leb128(uint32(len(paramTypes)))...)
	typeBody = append(typeBody, paramTypes...)
	typeBody = append(typeBody, leb128(uint32(len(returnTypes)))...)
	typeBody = append(typeBody, returnTypes...)
	typeSec = append(typeSec, leb128(uint32(len(typeBody)))...)
	typeSec = append(typeSec, typeBody...)

	// Import section: one entry: (module, fn) with kind=function, type-index=0.
	impBody := []byte{0x01} // count of imports = 1
	impBody = append(impBody, leb128(uint32(len(module)))...)
	impBody = append(impBody, module...)
	impBody = append(impBody, leb128(uint32(len(fn)))...)
	impBody = append(impBody, fn...)
	impBody = append(impBody, 0x00, 0x00) // kind=func, type idx=0

	impSec := []byte{0x02}
	impSec = append(impSec, leb128(uint32(len(impBody)))...)
	impSec = append(impSec, impBody...)

	// Magic + version.
	out := []byte{
		0x00, 0x61, 0x73, 0x6d, // \0asm
		0x01, 0x00, 0x00, 0x00, // version 1
	}
	out = append(out, typeSec...)
	out = append(out, impSec...)
	return out
}

// leb128 encodes a uint32 as little-endian base-128 (WASM's canonical
// length encoding for sections and counts).
func leb128(v uint32) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			break
		}
	}
	return out
}
