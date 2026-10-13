package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/indexer"
)

// indexWithLayout makes the index of a home and gives it the layout version (the indexer keeps it
// under "m/version", 8 bytes big-endian).
func indexWithLayout(t *testing.T, home string, version uint64) string {
	t.Helper()
	dir := filepath.Join(home, indexerDBDir)
	store, err := indexer.Open(dir)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	db, err := pebble.Open(dir, &pebble.Options{})
	require.NoError(t, err)
	require.NoError(t, db.Set([]byte("m/version"), binary.BigEndian.AppendUint64(nil, version), pebble.Sync))
	require.NoError(t, db.Close())
	return dir
}

// currentLayout is the layout version this indexer writes, as MoveAsideStale reports it.
func currentLayout(t *testing.T) uint64 {
	t.Helper()
	res, err := indexer.MoveAsideStale(t.TempDir(), time.Now())
	require.NoError(t, err)
	return res.Current
}

func TestRunIndexerMigrate_movesAnOldIndexAsideAndSaysSo(t *testing.T) {
	home := t.TempDir()
	dir := indexWithLayout(t, home, 2)
	var out bytes.Buffer

	require.NoError(t, runIndexerMigrate(&out, home, time.Date(2026, 10, 11, 1, 30, 5, 0, time.UTC)))
	moved := dir + ".v2-20261011T013005Z"
	require.Contains(t, out.String(), "layout version 2")
	require.Contains(t, out.String(), "moved it aside to "+moved)
	require.Contains(t, out.String(), "nothing deleted")
	_, err := os.Stat(moved)
	require.NoError(t, err)
	_, err = os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist)

	store, err := indexer.Open(dir)
	require.NoError(t, err, "the indexer's own open now makes a fresh index")
	require.NoError(t, store.Close())
}

func TestRunIndexerMigrate_keepsACurrentIndexAndToleratesNone(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	require.NoError(t, runIndexerMigrate(&out, home, time.Now()))
	require.Contains(t, out.String(), "no index in")

	dir := filepath.Join(home, indexerDBDir)
	store, err := indexer.Open(dir)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	out.Reset()
	require.NoError(t, runIndexerMigrate(&out, home, time.Now()))
	require.Contains(t, out.String(), fmt.Sprintf("current layout version %d; kept", currentLayout(t)))
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestRunIndexerMigrate_aNewerIndexIsAnError(t *testing.T) {
	home := t.TempDir()
	current := currentLayout(t)
	indexWithLayout(t, home, current+1)
	require.ErrorContains(t, runIndexerMigrate(&bytes.Buffer{}, home, time.Now()), fmt.Sprintf("newer than the %d", current))
}

func TestIndexerCmd_migrateIsASubcommand(t *testing.T) {
	found, _, err := rootCmd().Find([]string{"indexer", "migrate"})
	require.NoError(t, err)
	require.Equal(t, "migrate", found.Name())
	require.NotNil(t, found.Flag("home"))
}
