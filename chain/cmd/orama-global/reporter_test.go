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
	require.NoError(t, os.WriteFile(filepath.Join(home, "vote-interval"), []byte("30m\n"), 0o600))
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
	cfg, err := reporterConfig(reporterFlags{home: home, chunk: reporter.DefaultChunkEntries}, acct.Address)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "votes"), cfg.VotesDir, "the votes directory defaults to <home>/votes")
	require.Equal(t, byte(0xab), cfg.Authority[0])
	require.Equal(t, acct.Address, cfg.Operator)
	require.Equal(t, 30*time.Minute, cfg.VoteInterval, "the interval is the network's, from the home, not an hour")

	custom, err := reporterConfig(reporterFlags{home: home, votes: "/srv/votes", chunk: 10}, acct.Address)
	require.NoError(t, err)
	require.Equal(t, "/srv/votes", custom.VotesDir)
}

func TestReporterConfig_refusals(t *testing.T) {
	acct, err := tx.DeriveAccount(bytes.Repeat([]byte{7}, 32))
	require.NoError(t, err)
	good := strings.Repeat("ab", 20)
	fl := func(home string) reporterFlags {
		return reporterFlags{home: home, chunk: reporter.DefaultChunkEntries}
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
}

// The voting interval is the Tor network's, written by the install from the
// network file. A reporter without it, or with one that is not a duration,
// does not start: it would otherwise judge a 30-minute network by an hour.
func TestReporterConfig_voteInterval(t *testing.T) {
	acct, err := tx.DeriveAccount(bytes.Repeat([]byte{7}, 32))
	require.NoError(t, err)
	fl := func(home string) reporterFlags {
		return reporterFlags{home: home, chunk: reporter.DefaultChunkEntries}
	}
	for _, c := range []struct{ name, content, want string }{
		{"missing", "", "does not exist"},
		{"not a duration", "half-past", "not a positive duration"},
		{"zero", "0s", "not a positive duration"},
		{"negative", "-30m", "not a positive duration"},
		{"two values", "30m 1h", "does not hold one value"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := reporterHome(t, acct.Address, strings.Repeat("ab", 20))
			path := filepath.Join(home, "vote-interval")
			if c.content == "" {
				require.NoError(t, os.Remove(path))
			} else {
				require.NoError(t, os.WriteFile(path, []byte(c.content+"\n"), 0o600))
			}
			_, err := reporterConfig(fl(home), acct.Address)
			require.ErrorContains(t, err, c.want)
			require.ErrorContains(t, err, "vote-interval")
		})
	}
	home := reporterHome(t, acct.Address, strings.Repeat("ab", 20))
	require.NoError(t, os.WriteFile(filepath.Join(home, "vote-interval"), []byte("1h\n"), 0o600))
	cfg, err := reporterConfig(fl(home), acct.Address)
	require.NoError(t, err)
	require.Equal(t, time.Hour, cfg.VoteInterval)
}
