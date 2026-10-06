// Package testutil is TEST ONLY. It holds fakes for x/shielded's keeper tests: a verifier that
// accepts, a tree that is not the Orchard tree, in-memory bank and fee keepers, and a builder for
// bundles that have the canonical framing but no valid proof.
//
// Nothing outside a _test.go file may import it. TestNoProductionImport walks the module and fails
// when a production file does, so a fake verifier can never be linked into oramad.
package testutil
