package keeper

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// codeMsg carries code bytes the way wasmd's store-code messages do.
type codeMsg struct{ code []byte }

func (*codeMsg) Reset()                    {}
func (*codeMsg) String() string            { return "codeMsg" }
func (*codeMsg) ProtoMessage()             {}
func (m *codeMsg) GetWASMByteCode() []byte { return m.code }

func gzipped(raw []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	return buf.Bytes()
}

// FuzzWasmPolicyCodeHash: the hash that x/houses' allow-list is matched against is the SHA-256 of
// the uncompressed wasm, the same whether the upload is raw or gzip, and an upload that expands
// past the cap is never hashed (a compression bomb cannot make a node read gigabytes).
func FuzzWasmPolicyCodeHash(f *testing.F) {
	f.Add([]byte("\x00asm\x01\x00\x00\x00"), false, uint8(0))
	f.Add([]byte("\x00asm\x01\x00\x00\x00"), true, uint8(0))
	f.Add([]byte{}, true, uint8(0))
	f.Add([]byte{0x1f, 0x8b, 0x08, 0, 0}, false, uint8(0))
	f.Add([]byte("x"), true, uint8(5))
	f.Fuzz(func(t *testing.T, raw []byte, gz bool, bomb uint8) {
		if bomb%6 != 0 {
			raw = make([]byte, int(bomb%6)*(1<<20)+1)
		}
		payload := raw
		if gz {
			payload = gzipped(raw)
		}
		got, ok := codeHash(&codeMsg{code: payload})
		if !gz && bytes.HasPrefix(raw, gzipMagic) {
			return // a raw upload that looks like gzip is read as gzip; only "no panic" holds.
		}
		want := len(raw) > 0 && len(raw) <= maxHashedWasmBytes
		if ok != want {
			t.Fatalf("hashed=%v for %d bytes of code (gzip=%v), want %v", ok, len(raw), gz, want)
		}
		if ok {
			sum := sha256.Sum256(raw)
			if got != hex.EncodeToString(sum[:]) {
				t.Fatalf("hash %s is not the SHA-256 of the uncompressed code", got)
			}
		}
	})
}

// FuzzWasmPolicyAllowStore: before the sunset height only genesis code ids may be stored, from the
// sunset on anything may, and the decision is a pure function of its inputs.
func FuzzWasmPolicyAllowStore(f *testing.F) {
	f.Add(int64(5), uint64(10), uint64(3), []byte{3, 4})
	f.Add(int64(10), uint64(10), uint64(0), []byte{})
	f.Add(int64(-1), uint64(0), uint64(1), []byte{1})
	f.Fuzz(func(t *testing.T, height int64, sunset, codeID uint64, genesisIDs []byte) {
		genesis := map[uint64]struct{}{}
		for _, id := range genesisIDs {
			genesis[uint64(id)] = struct{}{}
		}
		err := AllowStore(height, sunset, codeID, genesis)
		_, inGenesis := genesis[codeID]
		open := height >= 0 && uint64(height) >= sunset
		switch {
		case open && err != nil:
			t.Fatalf("store refused at or after the sunset: %v", err)
		case !open && inGenesis && err != nil:
			t.Fatalf("a genesis code id was refused before the sunset: %v", err)
		case !open && !inGenesis && !errors.Is(err, types.ErrUploadClosed):
			t.Fatalf("a non-genesis upload was allowed before the sunset (%v)", err)
		}
		if again := AllowStore(height, sunset, codeID, genesis); (again == nil) != (err == nil) {
			t.Fatal("AllowStore is not deterministic")
		}
	})
}

// FuzzWasmPolicyClassify: classifyMsg never panics on a message it knows nothing about and reports
// no upload for it.
func FuzzWasmPolicyClassify(f *testing.F) {
	f.Add([]byte("abc"))
	f.Fuzz(func(t *testing.T, code []byte) {
		if store, _, changes := classifyMsg(&codeMsg{code: code}); store || changes {
			t.Fatal("an unregistered message was classified as a code upload or a sunset change")
		}
		if store, id, changes := classifyMsg(nil); store || id != 0 || changes {
			t.Fatal("a nil message was classified")
		}
	})
}
