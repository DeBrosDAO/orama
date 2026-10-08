package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/client/tx"
	"github.com/DeBrosOfficial/network/chain/reporter"
)

func reporterHome(t *testing.T, operator, authority string) string {
	t.Helper()
	home := t.TempDir()
	if operator != "" {
		require.NoError(t, os.WriteFile(filepath.Join(home, "operator"), []byte(operator+"\n"), 0o600))
	}
	if authority != "" {
		require.NoError(t, os.WriteFile(filepath.Join(home, "authority-id"), []byte(authority+"\n"), 0o600))
	}
	return home
}

func TestReporterConfig(t *testing.T) {
	acct, err := tx.DeriveAccount(bytes.Repeat([]byte{7}, 32))
	require.NoError(t, err)
	authority := strings.Repeat("ab", 20)

	home := reporterHome(t, acct.Address, authority)
	cfg, err := reporterConfig(reporterFlags{home: home, voteInterval: reporter.DefaultVoteInterval, chunk: reporter.DefaultChunkEntries}, acct.Address)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "votes"), cfg.VotesDir, "the votes directory defaults to <home>/votes")
	require.Equal(t, byte(0xab), cfg.Authority[0])
	require.Equal(t, acct.Address, cfg.Operator)

	custom, err := reporterConfig(reporterFlags{home: home, votes: "/srv/votes", voteInterval: time.Hour, chunk: 10}, acct.Address)
	require.NoError(t, err)
	require.Equal(t, "/srv/votes", custom.VotesDir)
}

func TestReporterConfig_refusals(t *testing.T) {
	acct, err := tx.DeriveAccount(bytes.Repeat([]byte{7}, 32))
	require.NoError(t, err)
	good := strings.Repeat("ab", 20)
	fl := func(home string) reporterFlags {
		return reporterFlags{home: home, voteInterval: time.Hour, chunk: reporter.DefaultChunkEntries}
	}

	_, err = reporterConfig(fl(reporterHome(t, "", good)), acct.Address)
	require.ErrorContains(t, err, "operator", "no operator file")
	_, err = reporterConfig(fl(reporterHome(t, acct.Address, "")), acct.Address)
	require.ErrorContains(t, err, "authority-id", "no authority identity file")
	_, err = reporterConfig(fl(reporterHome(t, acct.Address, "abcd")), acct.Address)
	require.ErrorContains(t, err, "40-hex")
	_, err = reporterConfig(fl(reporterHome(t, acct.Address, strings.Repeat("zz", 20))), acct.Address)
	require.ErrorContains(t, err, "40-hex")

	bad := fl(reporterHome(t, acct.Address, good))
	bad.chunk = -1
	_, err = reporterConfig(bad, acct.Address)
	require.Error(t, err, "a negative chunk size is refused at startup, not after an epoch has passed")
	bad.chunk = 1 << 20
	_, err = reporterConfig(bad, acct.Address)
	require.Error(t, err)
	bad.chunk, bad.voteInterval = 10, 0
	_, err = reporterConfig(bad, acct.Address)
	require.Error(t, err)
}
