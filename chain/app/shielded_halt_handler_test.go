package app

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/log/v2"

	shieldedkeeper "github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

// A verifier halt ends the process with the documented status and logs the height and the bundle,
// then panics with the halt if the exit returns; any other recovered value is not its business.
func TestVerifierHaltHandler_endsTheProcessAndLogsTheBundle(t *testing.T) {
	logger := log.NewTestLoggerInfo(t)
	exited := -1
	handler := verifierHaltHandler(logger, func(code int) { exited = code })
	halt := shieldedkeeper.VerifierHalt{Height: 77, BundleHash: "abc123", Err: verify.ErrVerifierFault}

	var panicked any
	func() {
		defer func() { panicked = recover() }()
		_ = handler(halt)
	}()
	require.Equal(t, ExitVerifierHalt, exited, "the process must exit with the verifier halt status")
	require.Equal(t, 87, ExitVerifierHalt)
	require.Equal(t, halt, panicked, "if exit returns, the halt itself escapes")

	exited = -1
	require.NoError(t, handler("some other panic"))
	require.NoError(t, handler(errors.New("an error")))
	require.Equal(t, -1, exited, "only a verifier halt ends the process")
}
