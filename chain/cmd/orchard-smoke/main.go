// Command orchard-smoke checks that the Orchard verifier linked into this binary accepts the two
// committed Ironwood vectors and rejects a tampered copy of each. It exists to prove a static
// build, the musl/zig one that cannot run on the machine that built it, on the machine that will
// run the node. See docs/whitepaper/technical-reference/vol2/39-chain-architecture.md, "Orchard smoke test".
//
// Exit status: 0 all checks passed, 1 a check failed, 2 the binary was built without the Rust
// verifier.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/DeBrosOfficial/network/chain/x/shielded/orchardffi"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
	orchardverify "github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchard"
)

const (
	exitOK       = 0
	exitFailed   = 1
	exitNotBuilt = 2
)

func main() {
	os.Exit(run())
}

func run() int {
	if !orchardverify.Linked {
		fmt.Fprintln(os.Stderr, "orchard-smoke: built without the Rust verifier (needs cgo and -tags orchardffi)")
		return exitNotBuilt
	}
	fmt.Printf("orchard-smoke %s/%s\n", runtime.GOOS, runtime.GOARCH)
	if err := orchardverify.Warm(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL warm: %v\n", err)
		return exitFailed
	}
	chainID, err := orchardffi.Vectors.ReadFile("testdata/chain-id")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL read chain id: %v\n", err)
		return exitFailed
	}
	v, err := orchardverify.New(string(chainID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL build verifier: %v\n", err)
		return exitFailed
	}
	failed := false
	for _, name := range orchardffi.VectorNames {
		bundle, err := orchardffi.Vectors.ReadFile("testdata/" + name + ".bundle")
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %s: read: %v\n", name, err)
			failed = true
			continue
		}
		failed = !check(name+" accepts", v.Verify(bundle, nil), nil) || failed
		tampered := bytes.Clone(bundle)
		tampered[len(tampered)-10] ^= 1 // inside the binding signature.
		failed = !check(name+" rejects a tampered binding signature", v.Verify(tampered, nil), verify.ErrSignatureRejected) || failed
	}
	if failed {
		return exitFailed
	}
	fmt.Println("orchard-smoke: all checks passed")
	return exitOK
}

// check prints one result line. want nil means the call must succeed.
func check(what string, got, want error) bool {
	ok := (want == nil && got == nil) || (want != nil && errors.Is(got, want))
	if ok {
		fmt.Printf("ok   %s\n", what)
		return true
	}
	fmt.Fprintf(os.Stderr, "FAIL %s: got %v, want %v\n", what, got, want)
	return false
}
