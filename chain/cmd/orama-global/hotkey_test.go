package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadOrCreateHotKey_createsOnceAndReloadsTheSameAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hot-key")
	first, created, err := loadOrCreateHotKey(path)
	require.NoError(t, err)
	require.True(t, created)
	require.Contains(t, first.Address, "orama1")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	again, created, err := loadOrCreateHotKey(path)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.Address, again.Address)
}

func TestLoadOrCreateHotKey_refusesAReadableOrMalformedKey(t *testing.T) {
	dir := t.TempDir()
	open := filepath.Join(dir, "open")
	_, _, err := loadOrCreateHotKey(open)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(open, 0o640))
	_, _, err = loadOrCreateHotKey(open)
	require.ErrorContains(t, err, "must be 0600")

	for name, body := range map[string]string{
		"short": "abcd",
		"hex":   "zz",
		"zero":  "0000000000000000000000000000000000000000000000000000000000000000",
	} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		_, _, err := loadOrCreateHotKey(path)
		require.Errorf(t, err, name)
	}
}

func TestReadNodeID_needsOneID(t *testing.T) {
	dir := t.TempDir()
	_, err := readNodeID(filepath.Join(dir, "absent"))
	require.ErrorContains(t, err, "register the node")
	for body, ok := range map[string]bool{"node-7\n": true, "  ": false, "a b": false} {
		path := filepath.Join(dir, "id")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		id, err := readNodeID(path)
		if ok {
			require.NoError(t, err)
			require.Equal(t, "node-7", id)
		} else {
			require.Error(t, err)
		}
	}
}

func TestReadDenylist_absentIsEmpty(t *testing.T) {
	got, err := readDenylist(filepath.Join(t.TempDir(), "denylist"))
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestStartHeight_readsRegistrationOnlyWithoutState(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	calls := 0
	registered := func(context.Context) (int64, error) { calls++; return 77, nil }
	h, err := startHeight(context.Background(), state, 0, registered)
	require.NoError(t, err)
	require.Equal(t, int64(77), h)
	require.NoError(t, os.WriteFile(state, []byte(`{"height":90}`), 0o600))
	h, err = startHeight(context.Background(), state, 0, func(context.Context) (int64, error) {
		return 0, errors.New("x/nodes is down")
	})
	require.NoError(t, err, "a restart does not ask x/nodes")
	require.Zero(t, h)
	h, err = startHeight(context.Background(), filepath.Join(dir, "absent"), 5, registered)
	require.NoError(t, err)
	require.Equal(t, int64(5), h)
	require.Equal(t, 1, calls)
}
