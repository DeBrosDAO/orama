//go:build cgo && orchardffi

package orchardproc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchard"
)

// verdict names the class of a verifier's answer: the reason a bundle is refused, or accepted.
func verdict(err error) string {
	switch {
	case err == nil:
		return "accept"
	case errors.Is(err, verify.ErrMalformed):
		return "malformed"
	case errors.Is(err, verify.ErrProofLength):
		return "proof length"
	case errors.Is(err, verify.ErrSignatureRejected):
		return "signature"
	case errors.Is(err, verify.ErrProofRejected):
		return "proof"
	case errors.Is(err, verify.ErrVerifierFault):
		return "fault"
	}
	return "other: " + err.Error()
}

// FuzzOrchardVerifyDifferential gives the same bytes to the in-process library (cgo) and to the
// out-of-process binary and requires the same verdict class, and, since bundle.Parse documents
// that it accepts only the framing the Rust verifiers accept, requires that a bundle Parse
// refuses is refused as malformed or as a wrong proof length by both. A fault (a caught panic,
// a dead process) is never an acceptable answer to bytes.
func FuzzOrchardVerifyDifferential(f *testing.F) {
	dir := filepath.Join("..", "..", "orchardffi", "testdata")
	chain, err := os.ReadFile(filepath.Join(dir, "chain-id"))
	if err != nil {
		f.Fatal(err)
	}
	path, err := filepath.Abs(filepath.Join("..", "..", "orchardverifier-bin", "target", "release", "orama-orchard-verifier"))
	if err != nil {
		f.Fatal(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		f.Fatalf("build the out-of-process verifier first (make orchard-verifier): %v", err)
	}
	pin := sha256.Sum256(binary)
	proc, err := New(Config{Path: path, ChainID: string(chain), SHA256: hex.EncodeToString(pin[:]), RequestTimeout: 2 * time.Minute})
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = proc.Close() })
	lib, err := orchard.New(string(chain))
	if err != nil {
		f.Fatal(err)
	}

	for _, name := range vectors {
		raw, err := os.ReadFile(filepath.Join(dir, name+".bundle"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw, []byte(nil))
		f.Add(raw, []byte("unshield-binding"))
	}
	f.Add([]byte{}, []byte(nil))
	f.Add([]byte{1}, []byte(nil))
	f.Add([]byte{0}, []byte(nil))

	f.Fuzz(func(t *testing.T, raw, binding []byte) {
		a, b := lib.Verify(raw, binding), proc.Verify(raw, binding)
		if verdict(a) != verdict(b) {
			t.Fatalf("in process: %s (%v); out of process: %s (%v)", verdict(a), a, verdict(b), b)
		}
		if class := verdict(a); class == "fault" || strings.HasPrefix(class, "other") {
			t.Fatalf("bytes were answered with %s", class)
		}
		if _, perr := bundle.Parse(raw); perr != nil {
			if class := verdict(a); class != "malformed" && class != "proof length" {
				t.Fatalf("bundle.Parse refuses (%v) but the verifiers answer %s", perr, class)
			}
			if verdict(perr) != verdict(a) {
				t.Fatalf("bundle.Parse says %s, the verifiers say %s", verdict(perr), verdict(a))
			}
		}
	})
}
