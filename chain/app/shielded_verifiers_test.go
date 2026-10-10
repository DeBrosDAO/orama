package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/client/flags"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"

	"github.com/DeBrosOfficial/network/chain/app"
	orchardverify "github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchard"
)

// A stagenet, testnet or mainnet node without the verifiers voted on proposals it never verified
// and forked itself off at the first shielded transaction it executed. `oramad start` now refuses it
// and says what to install.
func TestCheckNodeCanVerify_publicNetworkWithoutVerifierIsRefused(t *testing.T) {
	opts := simtestutil.AppOptionsMap{flags.FlagHome: t.TempDir()}
	for _, chainID := range []string{"orama-stagenet-6", "orama-testnet-1", "orama-1"} {
		err := app.CheckNodeCanVerify(chainID, opts)
		require.Error(t, err, "%s may start without the shielded verifiers", chainID)
		require.True(t, strings.Contains(err.Error(), app.ShieldedVerifierBinary) && strings.Contains(err.Error(), chainID),
			"%s: the refusal does not say what to install: %v", chainID, err)
	}
}

// Localnets and the devnet a script builds for one e2e run are built without the orchard library and
// keep starting.
func TestCheckNodeCanVerify_localnetAndDevnetAreExempt(t *testing.T) {
	for _, chainID := range []string{"orama-localnet-1", "orama-devnet-e2e-run7"} {
		require.NoError(t, app.CheckNodeCanVerify(chainID, simtestutil.EmptyAppOptions{}), chainID)
	}
}

// The binary at the default path counts only together with the linked library: a node with the file
// but without the library still cannot verify.
func TestCheckNodeCanVerify_binaryAloneIsNotEnough(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin", app.ShieldedVerifierBinary)
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))
	err := app.CheckNodeCanVerify("orama-stagenet-6", simtestutil.AppOptionsMap{flags.FlagHome: home})
	if orchardverify.Linked {
		require.NoError(t, err)
		return
	}
	require.ErrorContains(t, err, "orchard library linked: false")
}
