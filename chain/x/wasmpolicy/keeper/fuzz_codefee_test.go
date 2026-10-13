package keeper

import (
	"bytes"
	"testing"
)

// FuzzWasmPolicyCodeFeeSize: the size the code fee counts is the uncompressed length, the same
// whether the upload is raw or gzip, and an upload that expands past the cap has no size (it is
// refused, never fully read).
func FuzzWasmPolicyCodeFeeSize(f *testing.F) {
	f.Add([]byte("\x00asm\x01\x00\x00\x00"), false, uint8(0))
	f.Add([]byte("\x00asm\x01\x00\x00\x00"), true, uint8(0))
	f.Add([]byte{}, true, uint8(0))
	f.Add([]byte("x"), true, uint8(5))
	f.Fuzz(func(t *testing.T, raw []byte, gz bool, bomb uint8) {
		if bomb%6 != 0 {
			raw = make([]byte, int(bomb%6)*(1<<20)+1)
		}
		payload := raw
		if gz {
			payload = gzipped(raw)
		}
		if !gz && bytes.HasPrefix(raw, gzipMagic) {
			return // read as gzip; only "no panic" holds.
		}
		got, ok := uncompressedWasm(payload)
		if want := len(raw) <= maxHashedWasmBytes; ok != want {
			t.Fatalf("sized=%v for %d bytes of code (gzip=%v), want %v", ok, len(raw), gz, want)
		}
		if ok && len(got) != len(raw) {
			t.Fatalf("size %d, want %d", len(got), len(raw))
		}
	})
}
