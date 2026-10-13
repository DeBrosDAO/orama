package inclusion

import "bytes"

// InjectedCommitMagic starts the transaction a proposer puts first in a block
// to carry the previous height's ExtendedCommitInfo. Its first byte, 'O', is
// protobuf field 9 with wire type 7, which is not a legal wire type, so these
// bytes can never decode as a chain transaction. Anything that reads a block's
// transactions (the app, the indexer) tells this one apart by its prefix.
const InjectedCommitMagic = "ORAMA-INCLUSION-EXTENDED-COMMIT-V1:"

// IsInjectedCommit reports whether tx is the injected extended commit.
func IsInjectedCommit(tx []byte) bool {
	return bytes.HasPrefix(tx, []byte(InjectedCommitMagic))
}
