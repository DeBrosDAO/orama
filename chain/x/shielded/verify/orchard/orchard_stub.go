//go:build !cgo || !orchardffi

package orchard

import "github.com/DeBrosOfficial/network/chain/x/shielded/verify"

// Linked reports whether this build carries the Rust verifier. This one does not: it was built
// without cgo or without the orchardffi tag.
const Linked = false

type unlinked struct{}

// New returns a verifier that refuses every bundle with verify.ErrVerifierNotLinked. A build
// without the Rust library never accepts a shielded bundle. An empty chain id is refused, as in
// the linked build, so the two builds have one contract.
func New(chainID string) (verify.Verifier, error) {
	if chainID == "" {
		return nil, verify.ErrEmptyChainID
	}
	return unlinked{}, nil
}

// Warm has nothing to build without the Rust library.
func Warm() error { return verify.ErrVerifierNotLinked }

func (unlinked) ID() string { return VerifierID }

func (unlinked) Verify([]byte) error { return verify.ErrVerifierNotLinked }
