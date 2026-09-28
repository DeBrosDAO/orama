// Package inclusion is the C13 decision rule for vote-extension inclusion lists.
// PrepareProposal and ProcessProposal call it; this package does not connect
// to CometBFT or apply state.
//
// A vote extension carries raw transaction bytes, capped by ListMaxBytes.
// PrepareCommit verifies extension signatures, drops invalid ones, and
// requires the rest to hold at least 2/3 of View.TotalPower. Transaction
// bytes are deduplicated across those extensions and must fit in
// MaxEmbeddedListBytes.
//
// Process requires every remaining listed transaction that is valid and that
// fits to occupy the front of the block, in lexicographic order of the raw
// bytes. A listed transaction may be absent only when it is invalid in that
// sequential walk or when it does not fit after the earlier listed
// transactions that were actually placed. Power on an Extension is supplied
// by the caller from the validator set and is not part of the signature.
package inclusion
