//go:build !cgo || !orchardffi

package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	shieldedtypes "github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// A build without the Rust verifier linked accepts no bundle, whatever the other verifier says.
func TestShielded_buildWithoutTheVerifierAcceptsNoBundle(t *testing.T) {
	vectorChainIDFromFile(t)
	c := newShieldedChain(t, shieldedApp(t, "", ""), nil)
	c.fund(t, c.alice, 1_000_000)

	shield := &shieldedtypes.MsgShield{Signer: c.aliceAddr().String(), Bundle: loadVector(t, "ironwood-1-action")}
	results := c.block(t, c.signed(t, c.alice, 200_000, shield))
	require.NotZero(t, results[0].Code)
	require.Contains(t, results[0].Log, "not linked")

	pool, err := c.app.ShieldedKeeper.Pools.Get(c.app.NewContext(true), poolKey())
	require.Error(t, err, "no pool was created")
	_ = pool
}
