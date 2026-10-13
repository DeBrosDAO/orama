package app

import (
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
)

// SetInclusionClock replaces the clock the inclusion seen-set reads, so a test
// can age a transaction past inclusionIncludeAfter without waiting.
func (app *OramaApp) SetInclusionClock(now func() time.Time) {
	app.inclusion.now = now
}

// InclusionIncludeAfter exposes the include-after delay to tests.
const InclusionIncludeAfter = inclusionIncludeAfter

// InjectedCommitMagic exposes the injected transaction prefix to tests.
const InjectedCommitMagic = injectedCommitMagic

// EncodeInjectedCommitForTest builds the injected transaction for a commit.
func EncodeInjectedCommitForTest(ec abci.ExtendedCommitInfo) ([]byte, error) {
	return encodeInjectedCommit(ec)
}

// DecodeInjectedCommitForTest parses the injected transaction the way ProcessProposal does.
func DecodeInjectedCommitForTest(tx []byte) (abci.ExtendedCommitInfo, error) {
	return decodeInjectedCommit(tx)
}

// InclusionPoolLen is the number of transactions the seen-set tracks.
func (app *OramaApp) InclusionPoolLen() int { return app.inclusion.pool.Len() }

// SeedInclusionPoolForTest puts a transaction in the seen-set as if CheckTx had accepted it: the
// shielded tests list transactions whose bundles CheckTx would refuse.
func (app *OramaApp) SeedInclusionPoolForTest(tx []byte) {
	app.inclusion.pool.Add(tx, app.inclusion.now())
}
