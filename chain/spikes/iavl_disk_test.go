package spikes_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/iavl"
	iavldb "github.com/cosmos/iavl/db"
	"github.com/stretchr/testify/require"
)

func dirBytes(t *testing.T, root string) int64 {
	t.Helper()
	var n int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	require.NoError(t, err)
	return n
}

func nullifierKey(i int) []byte {
	var key [32]byte
	binary.BigEndian.PutUint64(key[24:], uint64(i))
	return key[:]
}

func measureNullifiers(t *testing.T, n int) (iavlBytes, rawBytes int64) {
	t.Helper()
	value := []byte{0, 0, 0, 0, 0, 0, 0, 1}

	dir := t.TempDir()
	db, err := iavldb.NewDB("nullifiers", string(dbm.PebbleDBBackend), dir)
	require.NoError(t, err)
	tree := iavl.NewMutableTree(db, 100, false, iavl.NewNopLogger())
	for i := 0; i < n; i++ {
		_, err := tree.Set(nullifierKey(i), value)
		require.NoError(t, err)
	}
	_, version, err := tree.SaveVersion()
	require.NoError(t, err)
	require.Equal(t, int64(1), version)
	require.NoError(t, db.Close())
	iavlBytes = dirBytes(t, dir)

	rawDir := t.TempDir()
	raw, err := dbm.NewDB("raw", dbm.PebbleDBBackend, rawDir)
	require.NoError(t, err)
	for i := 0; i < n; i++ {
		require.NoError(t, raw.Set(nullifierKey(i), value))
	}
	require.NoError(t, raw.Close())
	rawBytes = dirBytes(t, rawDir)
	return iavlBytes, rawBytes
}

func TestIAVLv1NullifierDisk(t *testing.T) {
	// Two sizes so a fixed Pebble file overhead is not mistaken for a per-key cost.
	for _, n := range []int{1_000, 10_000} {
		iavlBytes, rawBytes := measureNullifiers(t, n)
		t.Logf("keys=%d iavl_disk_bytes=%d iavl_bytes_per_key=%.2f raw_pebble_disk_bytes=%d raw_bytes_per_key=%.2f iavl_over_raw=%.2f",
			n, iavlBytes, float64(iavlBytes)/float64(n), rawBytes, float64(rawBytes)/float64(n), float64(iavlBytes)/float64(rawBytes))
	}
}
