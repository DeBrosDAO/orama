//go:build !cgo || !orchardffi

package orchard

import "github.com/DeBrosOfficial/network/chain/x/shielded/verify"

// Append refuses: a build without the Rust library cannot hash the tree, so it cannot take a
// shielded bundle either.
func (Tree) Append([]byte, [][NodeLen]byte) ([]byte, [NodeLen]byte, error) {
	return nil, [NodeLen]byte{}, verify.ErrVerifierNotLinked
}

// EmptyRoot refuses, as Append does.
func (Tree) EmptyRoot() ([NodeLen]byte, error) {
	return [NodeLen]byte{}, verify.ErrVerifierNotLinked
}
