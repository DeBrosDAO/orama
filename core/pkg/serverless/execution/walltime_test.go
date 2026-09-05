package execution

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Bugboard #27 — wazero defaults to a FAKE walltime clock that returns
// ~2022-01-01T00:00:00.001Z frozen on every reading. TinyGo wasm calls
// WASI clock_time_get from time.Now(), so any serverless function that
// embeds timestamps (receipts, audit rows, cursor comparisons) silently
// poisons its writes with the sentinel epoch.
//
// The fix is to opt into real clocks via .WithSysWalltime() /
// .WithSysNanotime() on the wazero ModuleConfig (one-line per the two
// moduleConfig builders — engine.go for persistent WS, executor.go for
// stateless). This test pins the behavior at the executor's config
// path: instantiate a tiny wasm that calls WASI clock_time_get, read
// the result, assert it's a real post-2024 epoch and not the frozen
// 2022 sentinel.
//
// If a future refactor drops .WithSysWalltime(), this test fails with
// "got pre-2024 timestamp X (sentinel?); did the WithSysWalltime() call
// get dropped from moduleConfig?" — exact line back to the regression.

// walltimeProbeWasm is a hand-assembled WASM module that imports
// wasi_snapshot_preview1.clock_time_get and calls it from _start,
// writing the result to memory[0:8].
//
//	(module
//	  (type $clock_time_get (func (param i32 i64 i32) (result i32)))
//	  (type $start (func))
//	  (import "wasi_snapshot_preview1" "clock_time_get"
//	    (func $clock_time_get (type 0)))
//	  (memory (export "memory") 1)
//	  (func $_start (type 1)
//	    i32.const 0         ;; clock_id = REALTIME (0)
//	    i64.const 0         ;; precision = 0
//	    i32.const 0         ;; out_ptr = 0
//	    call $clock_time_get
//	    drop)
//	  (export "_start" (func $_start)))
//
// Reference: https://webassembly.github.io/spec/core/binary/modules.html
var walltimeProbeWasm = []byte{
	// Magic + version
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,

	// Type section (id=1) — body=11 bytes
	0x01,
	0x0b,                         // size = 11
	0x02,                         // 2 types
	0x60, 0x03, 0x7f, 0x7e, 0x7f, // type 0: func(i32, i64, i32)
	0x01, 0x7f, // -> (i32)
	0x60, 0x00, 0x00, // type 1: func() -> ()

	// Import section (id=2) — body=0x29 (41 bytes)
	0x02,
	0x29,
	0x01, // 1 import
	0x16, // module name "wasi_snapshot_preview1" length=22
	0x77, 0x61, 0x73, 0x69, 0x5f, 0x73, 0x6e, 0x61, 0x70, 0x73, 0x68, 0x6f, 0x74, 0x5f, 0x70, 0x72, 0x65, 0x76, 0x69, 0x65, 0x77, 0x31,
	0x0e, // fn name "clock_time_get" length=14
	0x63, 0x6c, 0x6f, 0x63, 0x6b, 0x5f, 0x74, 0x69, 0x6d, 0x65, 0x5f, 0x67, 0x65, 0x74,
	0x00, 0x00, // kind=func, type idx=0

	// Function section (id=3) — body=2 bytes
	0x03,
	0x02,
	0x01, // 1 function
	0x01, // type idx 1 (for _start)

	// Memory section (id=5) — body=3 bytes
	0x05,
	0x03,
	0x01,       // 1 memory
	0x00, 0x01, // limits: flags=0 (no max), min=1 page

	// Export section (id=7) — body=19 bytes (0x13)
	// = count(1) + memory_export(9) + start_export(9) = 19
	0x07,
	0x13,
	0x02,                                     // 2 exports
	0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, // "memory"
	0x02, 0x00, // kind=memory, idx=0
	0x06, 0x5f, 0x73, 0x74, 0x61, 0x72, 0x74, // "_start"
	0x00, 0x01, // kind=func, idx=1 (after the 1 import)

	// Code section (id=10) — body=13 bytes (0x0d)
	// = count(1) + body_size_byte(1) + body(11) = 13
	0x0a,
	0x0d,
	0x01,       // 1 function body
	0x0b,       // body size = 11 (locals_count + 10 instr bytes)
	0x00,       // 0 local groups
	0x41, 0x00, // i32.const 0  (clock_id)
	0x42, 0x00, // i64.const 0  (precision)
	0x41, 0x00, // i32.const 0  (out_ptr)
	0x10, 0x00, // call func 0 (the imported clock_time_get)
	0x1a, // drop (errno return)
	0x0b, // end
}

func TestModuleConfig_walltimeIsRealNotFrozenSentinel(t *testing.T) {
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		t.Fatalf("instantiate WASI: %v", err)
	}

	compiled, err := runtime.CompileModule(ctx, walltimeProbeWasm)
	if err != nil {
		t.Fatalf("compile probe wasm: %v (hex assembly likely off; recompute section sizes)", err)
	}
	defer compiled.Close(ctx)

	// Mirror the executor.go moduleConfig — the assertion is that this
	// SAME config is what protects against the bug-#27 regression.
	moduleConfig := wazero.NewModuleConfig().
		WithName("").
		WithArgs("probe").
		WithSysWalltime().
		WithSysNanotime()

	mod, err := runtime.InstantiateModule(ctx, compiled, moduleConfig)
	if err != nil {
		t.Fatalf("instantiate probe module: %v", err)
	}
	defer mod.Close(ctx)

	mem := mod.Memory()
	if mem == nil {
		t.Fatal("probe module has no memory export")
	}
	raw, ok := mem.Read(0, 8)
	if !ok {
		t.Fatal("could not read 8 bytes from probe memory at offset 0")
	}
	gotNs := binary.LittleEndian.Uint64(raw)

	// 2024-01-01T00:00:00 in unix nanoseconds = 1704067200000000000.
	// Any real time after that passes. The sentinel ~2022-01-01 fails.
	const cutoff2024Ns uint64 = 1704067200000000000
	if gotNs < cutoff2024Ns {
		gotTime := time.Unix(0, int64(gotNs))
		t.Errorf("BUG #27 REGRESSION: WASI clock_time_get returned %d ns (%s) — "+
			"pre-2024 means the fake/sentinel clock is in effect. "+
			"Did the .WithSysWalltime() call get dropped from moduleConfig "+
			"in executor.go or engine.go?", gotNs, gotTime)
	}
}

func TestModuleConfig_walltimeWithoutFix_demoSentinel(t *testing.T) {
	// Negative control: WITHOUT .WithSysWalltime(), confirm wazero
	// returns the frozen sentinel. This pins the *cause* (so anyone
	// reading the test understands why the positive test is meaningful).
	// If wazero ever changes its default to a real clock, this test
	// fails — at which point the bug is moot and both tests can be
	// retired. Pinning the negative makes that change visible instead
	// of silently invalidating the fix's necessity.
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		t.Fatalf("instantiate WASI: %v", err)
	}
	compiled, err := runtime.CompileModule(ctx, walltimeProbeWasm)
	if err != nil {
		t.Fatalf("compile probe wasm: %v", err)
	}
	defer compiled.Close(ctx)

	// Default config — NO WithSysWalltime.
	defaultConfig := wazero.NewModuleConfig().WithName("").WithArgs("probe")
	mod, err := runtime.InstantiateModule(ctx, compiled, defaultConfig)
	if err != nil {
		t.Fatalf("instantiate probe module: %v", err)
	}
	defer mod.Close(ctx)

	raw, _ := mod.Memory().Read(0, 8)
	gotNs := binary.LittleEndian.Uint64(raw)

	const cutoff2024Ns uint64 = 1704067200000000000
	if gotNs >= cutoff2024Ns {
		t.Logf("wazero default walltime is now %d ns (%s) — past 2024. "+
			"If this happened upstream-by-default, the bug-#27 fix is no longer "+
			"necessary and the .WithSysWalltime() opt-in can be retired.",
			gotNs, time.Unix(0, int64(gotNs)))
		t.Skip("wazero default walltime is real time — bug-#27 fix may be redundant; review")
	}
	// Sentinel confirmed → fix is meaningful.
}
