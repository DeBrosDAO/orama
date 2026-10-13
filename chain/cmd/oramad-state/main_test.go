package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCount_set(t *testing.T) {
	tests := map[string]uint64{
		"0": 0, "100000": 100_000, "100_000": 100_000, "1,000": 1_000,
		"100k": 100_000, "10M": 10_000_000, "10m": 10_000_000, "1G": 1_000_000_000, " 5k ": 5_000,
	}
	for in, want := range tests {
		var c count
		require.NoError(t, c.Set(in), in)
		require.Equal(t, want, uint64(c), in)
	}
}

func TestCount_setRefuses(t *testing.T) {
	for _, in := range []string{"", "k", "-5", "1.5", "ten", "99999999999999999999", "18446744073709551615g"} {
		var c count
		require.Error(t, c.Set(in), in)
	}
}

func TestRun_usageAndUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run(context.Background(), nil, &out, &errOut)
	require.ErrorContains(t, err, "usage: oramad-state")
	err = run(context.Background(), []string{"nope"}, &out, &errOut)
	require.ErrorContains(t, err, `unknown command "nope"`)
	require.Empty(t, out.String(), "nothing is printed for a command that did not run")
}

func TestRun_printsTheResultAsJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run(context.Background(), []string{"prune", "--work", t.TempDir(), "--blocks", "60", "--window", "20", "--txs", "1", "--tx-bytes", "40"}, &out, &errOut)
	require.NoError(t, err)
	var res map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &res))
	require.EqualValues(t, 39, res["blocks_pruned"])
	require.Contains(t, errOut.String(), "building 60 blocks", "progress goes to standard error")
}

func TestRun_commandsNeedAHome(t *testing.T) {
	for _, name := range []string{"prepare", "stategen", "boot", "snapshot", "export", "indexer"} {
		var out, errOut bytes.Buffer
		err := run(context.Background(), []string{name}, &out, &errOut)
		require.ErrorContains(t, err, "--home is required", name)
	}
}

func TestRun_flagErrorsAreReturned(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run(context.Background(), []string{"stategen", "--home", t.TempDir(), "--nullifiers", "lots"}, &out, &errOut)
	require.Error(t, err)
	err = run(context.Background(), []string{"indexer", "--home", t.TempDir(), "--blocks", "0"}, &out, &errOut)
	require.ErrorContains(t, err, "at least 1")
}
