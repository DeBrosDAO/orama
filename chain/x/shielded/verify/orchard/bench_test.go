package orchard

import (
	"fmt"
	"testing"
)

// benchVectors are the committed real Ironwood bundles, by action count. Larger bundles need the
// wallet crate's prover (chain/x/shielded/wallet) and are not committed.
var benchVectors = map[int]string{1: "ironwood-1-action", 2: "ironwood-2-action"}

// BenchmarkVerify is the cost of the in-process Rust verifier (cgo) on one bundle, by action count.
// It skips with a message when this build does not link the library: build it with
// `make orchard-lib` and run with -tags orchardffi.
func BenchmarkVerify(b *testing.B) {
	if !Linked {
		b.Skip("the Rust verifier library is not linked: run `make orchard-lib` and build with -tags orchardffi")
	}
	for _, actions := range []int{1, 2} {
		b.Run(fmt.Sprintf("actions=%d", actions), func(b *testing.B) {
			bundle, _, chainID := loadVector(b, benchVectors[actions])
			v := mustNew(b, chainID)
			if err := v.Verify(bundle, nil); err != nil { // builds the verifying key once, outside the timing
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := v.Verify(bundle, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
