// Package inclusion is the C13 decision rule for vote-extension inclusion lists.
// It is pure: it reads no state of its own and applies none. chain/app
// connects it to CometBFT: ExtendVote, VerifyVoteExtension, PrepareProposal
// and ProcessProposal call it (docs/CHAIN.md, "Inclusion lists (C13)").
//
// A list is raw transaction bytes, capped by ListMaxBytes. SelectList picks
// what a validator lists, EncodeList and DecodeList carry it in a vote
// extension, and ValidateList is the stateless check VerifyVoteExtension runs.
// PrepareCommit, Required, Assemble and Process take the extensions of a
// commit: they drop invalid ones, require the rest to hold at least 2/3 of
// View.TotalPower, and deduplicate transaction bytes, which must fit in
// MaxEmbeddedListBytes.
//
// Process requires every remaining listed transaction that is valid and that
// fits to occupy the front of the block, in lexicographic order of the raw
// bytes. A listed transaction may be absent only when it is invalid in that
// sequential walk or when it does not fit after the earlier listed
// transactions that were actually placed. Power on an Extension is supplied
// by the caller from the validator set and is not part of the signature.
//
// Two modes exist. The library alone signs each extension with ed25519
// (BuildExtension, MarshalBinary) and PrepareCommit checks that signature. On
// a chain, CometBFT already signs and verifies the extension, so the caller
// sets View.Authenticated and the Extension signature is not checked. View
// also carries the hooks a chain needs: Decode reads its own transaction
// format, and Admit runs its full transaction validity check in walk order.
package inclusion
