package orchardproc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// benchVectors are the committed real Ironwood bundles by action count, the same as the in-process
// verifier's benchmark so the two columns compare.
var benchVectors = map[int]string{1: "ironwood-1-action", 2: "ironwood-2-action"}

// BenchmarkVerify is the cost of the out-of-process verifier (the Go client and the Rust binary it
// keeps running) on one bundle, by action count. It skips with a message when the binary is not
// built: run `make orchard-verifier`.
func BenchmarkVerify(b *testing.B) {
	binary, err := filepath.Abs(filepath.Join("..", "..", "orchardverifier-bin", "target", "release", "orama-orchard-verifier"))
	if err != nil {
		b.Fatal(err)
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		b.Skipf("the out-of-process verifier is not built (%v): run `make orchard-verifier`", err)
	}
	sum := sha256.Sum256(raw)
	dir := filepath.Join("..", "..", "orchardffi", "testdata")
	chainID, err := os.ReadFile(filepath.Join(dir, "chain-id"))
	if err != nil {
		b.Fatal(err)
	}
	v, err := New(Config{Path: binary, ChainID: string(chainID), SHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = v.Close() })
	if err := v.Warm(); err != nil { // builds the verifying key once, outside the timing
		b.Fatal(err)
	}
	for _, actions := range []int{1, 2} {
		bundle, err := os.ReadFile(filepath.Join(dir, benchVectors[actions]+".bundle"))
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("actions=%d", actions), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if err := v.Verify(bundle, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
