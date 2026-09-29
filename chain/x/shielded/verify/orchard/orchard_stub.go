//go:build !cgo || !orchardffi

package orchard

import "github.com/DeBrosOfficial/network/chain/x/shielded/verify"

// Linked reports whether this build carries the Rust verifier. This one does not: it was built
// without cgo or without the orchardffi tag.
const Linked = false

type unlinked struct{}

// New returns a verifier that refuses every bundle with verify.ErrVerifierNotLinked. A build
// without the Rust library never accepts a shielded bundle.
func New(string) verify.Verifier { return unlinked{} }

func (unlinked) Verify([]byte) error { return verify.ErrVerifierNotLinked }
