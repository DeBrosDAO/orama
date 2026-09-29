// Package orchardffi is the Rust verifier crate. Its only Go file exports the committed test
// vectors, so a smoke binary can carry them and check the linked verifier on a machine that has
// no source tree.
package orchardffi

import "embed"

// Vectors holds chain-id and, per vector, <name>.bundle and <name>.sighash.
//
//go:embed testdata/chain-id testdata/ironwood-1-action.bundle testdata/ironwood-1-action.sighash testdata/ironwood-2-action.bundle testdata/ironwood-2-action.sighash
var Vectors embed.FS

// VectorNames lists the vectors in Vectors.
var VectorNames = []string{"ironwood-1-action", "ironwood-2-action"}
