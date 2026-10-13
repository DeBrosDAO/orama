//go:build cgo && orchardffi

package app_test

import (
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// The helpers the real-verifier tests (shielded_real_test.go, shielded_wallet_test.go) share. They
// live in a file with their tests' build tag: without the tag nothing uses them, and staticcheck
// reports dead code in shielded_helpers_test.go.

// Short names the real-verifier tests share.
type (
	abciResult         = abci.ExecTxResult
	cryptotypesPrivKey = cryptotypes.PrivKey
)

func cmtprotoHeader(height int64, at time.Time) cmtproto.Header {
	return cmtproto.Header{Height: height, Time: at}
}

// validatorAddr is committee member 0's operator address, the validator the bond vector names.
func (c *shieldedChain) validatorAddr() sdk.ValAddress {
	return sdk.ValAddress(c.committee[0].account.PubKey().Address())
}

// signerless builds a tx with no signature, no fee and exactly the given gas.
func (c *shieldedChain) signerless(t *testing.T, gas uint64, msg sdk.Msg) []byte {
	t.Helper()
	b := c.app.TxConfig().NewTxBuilder()
	require.NoError(t, b.SetMsgs(msg))
	b.SetGasLimit(gas)
	out, err := c.app.TxConfig().TxEncoder()(b.GetTx())
	require.NoError(t, err)
	return out
}

func withMinDelegation(n int64) chainOption { return func(c *chainConfig) { c.minDelegation = n } }

func dbmMem() dbm.DB { return dbm.NewMemDB() }
