package app

import (
	"fmt"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
	orchardverify "github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchard"
)

// newShieldedVerifiers builds the verifiers a shielded bundle must pass.
//
// An app with no chain id is not a node: it is the throwaway instance the CLI builds to read
// module metadata. It gets no verifiers, so verify.Check refuses every bundle, and it pays for no
// key generation. A verifier bound to the empty chain id is never built.
//
// When the Rust verifier is linked, its verifying key is built here, at construction, so the
// seconds it takes never land inside ProcessProposal or FinalizeBlock. A failure to build the key
// or to construct the verifier stops the app: a node that cannot verify must not start serving.
func newShieldedVerifiers(chainID string) []verify.Verifier {
	if chainID == "" {
		return nil
	}
	orchard, err := orchardverify.New(chainID)
	if err != nil {
		panic(fmt.Errorf("failed to build the Orchard verifier for chain %q: %w", chainID, err))
	}
	if orchardverify.Linked {
		if err := orchardverify.Warm(); err != nil {
			panic(fmt.Errorf("failed to build the Orchard verifying key: %w", err))
		}
	}
	return []verify.Verifier{orchard}
}
