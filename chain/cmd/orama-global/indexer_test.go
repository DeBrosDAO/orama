package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequireLoopback_acceptsOnlyLoopbackIPs(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:31015", "[::1]:31015", "127.0.0.2:1"} {
		require.NoError(t, requireLoopback(ok), ok)
	}
	for _, bad := range []string{"0.0.0.0:31015", ":31015", "localhost:31015", "10.0.0.1:31015", "[::]:31015", "127.0.0.1"} {
		require.Error(t, requireLoopback(bad), bad)
	}
}

func TestIndexerCmd_defaultsAndRegistration(t *testing.T) {
	cmd := indexerCmd()
	require.Equal(t, defaultIndexerListen, cmd.Flag("listen").DefValue)
	require.Equal(t, "1", cmd.Flag("start-height").DefValue)
	require.Equal(t, defaultRPC, cmd.Flag("rpc").DefValue)
	found, _, err := rootCmd().Find([]string{"indexer"})
	require.NoError(t, err)
	require.Equal(t, "indexer", found.Name())
}

func TestRunIndexer_refusesBadFlagsBeforeTouchingAnything(t *testing.T) {
	home := t.TempDir()
	err := runIndexer(context.Background(), defaultRPC, home, "0.0.0.0:31015", 1, time.Second)
	require.ErrorContains(t, err, "must be a loopback IP")
	err = runIndexer(context.Background(), defaultRPC, home, defaultIndexerListen, 1, 0)
	require.ErrorContains(t, err, "--interval")
}
