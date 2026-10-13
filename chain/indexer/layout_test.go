package indexer

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cockroachdb/pebble"
)

var movedAt = time.Date(2026, 10, 11, 1, 30, 5, 0, time.UTC)

// indexOfLayout is an index in dir that indexed three blocks, then recorded layout version
// `version` (0 leaves none, as an index written before versions).
func indexOfLayout(t *testing.T, dir string, version uint64) {
	t.Helper()
	chain, _ := econChain(t)
	addEconBlocks(t, chain, 1, 3)
	store, _ := follow(t, chain, dir, 1)
	if version == 0 {
		require.NoError(t, store.db.Delete([]byte(keyVersion), pebble.Sync))
	} else {
		require.NoError(t, store.db.Set([]byte(keyVersion), be64(version), pebble.Sync))
	}
	require.NoError(t, store.Close())
}

func TestMoveAsideStale_anOldIndexIsMovedAsideAndAFreshOneStarts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	indexOfLayout(t, dir, schemaVersion-1)
	_, err := Open(dir)
	require.Error(t, err, "the indexer refuses it")

	res, err := MoveAsideStale(dir, movedAt)
	require.NoError(t, err)
	want := fmt.Sprintf("%s.v%d-20261011T013005Z", dir, schemaVersion-1)
	require.Equal(t, MoveResult{Present: true, Version: schemaVersion - 1, Current: schemaVersion, MovedTo: want}, res)
	_, err = os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist, "nothing is left where the indexer looks")

	old, err := pebble.Open(want, &pebble.Options{ReadOnly: true})
	require.NoError(t, err, "the old index is kept whole")
	start, ok, err := getUint(old, []byte(keyStart))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(1), start)
	cursor, _, err := getUint(old, []byte(keyCursor))
	require.NoError(t, err)
	require.Equal(t, uint64(3), cursor, "its three blocks are still in it")
	require.NoError(t, old.Close())

	fresh, err := Open(dir)
	require.NoError(t, err, "a fresh index starts")
	defer fresh.Close()
	st, err := fresh.Status()
	require.NoError(t, err)
	require.Equal(t, Status{}, st)
	_, err = NewFollower(newFakeChain(), fresh, 7)
	require.NoError(t, err, "and takes any start height")
}

func TestMoveAsideStale_anIndexWrittenBeforeVersionsIsVersionZero(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	indexOfLayout(t, dir, 0)

	res, err := MoveAsideStale(dir, movedAt)
	require.NoError(t, err)
	require.Equal(t, uint64(0), res.Version)
	require.Equal(t, dir+".v0-20261011T013005Z", res.MovedTo)
}

func TestMoveAsideStale_aCurrentIndexIsKept(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	indexOfLayout(t, dir, schemaVersion)

	res, err := MoveAsideStale(dir, movedAt)
	require.NoError(t, err)
	require.Equal(t, MoveResult{Present: true, Version: schemaVersion, Current: schemaVersion}, res)

	store, err := Open(dir)
	require.NoError(t, err, "the index opens as it did")
	defer store.Close()
	st, err := store.Status()
	require.NoError(t, err)
	require.Equal(t, int64(3), st.Cursor, "with its blocks")
	entries, err := os.ReadDir(filepath.Dir(dir))
	require.NoError(t, err)
	require.Len(t, entries, 1, "and nothing beside it")
}

func TestMoveAsideStale_noIndexIsNothingToDo(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "index")
	res, err := MoveAsideStale(missing, movedAt)
	require.NoError(t, err)
	require.Equal(t, MoveResult{Current: schemaVersion}, res)
	_, err = os.Stat(missing)
	require.ErrorIs(t, err, os.ErrNotExist, "looking creates nothing")

	empty := filepath.Join(root, "empty")
	require.NoError(t, os.Mkdir(empty, 0o755))
	res, err = MoveAsideStale(empty, movedAt)
	require.NoError(t, err)
	require.False(t, res.Present, "a directory with no index in it is not one")
}

func TestMoveAsideStale_aNewerIndexIsAnErrorAndStaysWhereItIs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	indexOfLayout(t, dir, schemaVersion+1)

	_, err := MoveAsideStale(dir, movedAt)
	require.ErrorContains(t, err, fmt.Sprintf("newer than the %d this indexer writes", schemaVersion))
	_, err = os.Stat(dir)
	require.NoError(t, err)
}

func TestMoveAsideStale_aRunningIndexerIsNotMovedUnderItself(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	indexOfLayout(t, dir, schemaVersion-1)
	held, err := pebble.Open(dir, &pebble.Options{})
	require.NoError(t, err)
	defer held.Close()

	_, err = MoveAsideStale(dir, movedAt)
	require.ErrorContains(t, err, "is the indexer still running?")
	_, err = os.Stat(dir)
	require.NoError(t, err, "the index stays where the running indexer has it")
}

func TestMoveAsideStale_aTakenNameIsNotOverwritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	indexOfLayout(t, dir, schemaVersion-1)
	taken := fmt.Sprintf("%s.v%d-20261011T013005Z", dir, schemaVersion-1)
	require.NoError(t, os.Mkdir(taken, 0o755))

	_, err := MoveAsideStale(dir, movedAt)
	require.ErrorContains(t, err, "exists")
	_, err = os.Stat(dir)
	require.NoError(t, err, "the index is where it was")
}

func TestMoveAsideStale_theSecondRunFindsTheFreshIndexCurrent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	indexOfLayout(t, dir, schemaVersion-1)
	_, err := MoveAsideStale(dir, movedAt)
	require.NoError(t, err)
	fresh, err := Open(dir)
	require.NoError(t, err)
	require.NoError(t, fresh.Close())

	res, err := MoveAsideStale(dir, movedAt.Add(time.Hour))
	require.NoError(t, err)
	require.Empty(t, res.MovedTo, "the upgrade is safe to run again")
	require.True(t, res.Present)
}

// Layout 3 is the one before the per-epoch rewards: an index of it has epochs that never recorded
// who was paid, and cannot be completed, so the upgrade moves it aside and a new one starts.
func TestMoveAsideStale_theLayoutBeforeRewardsIsMovedAside(t *testing.T) {
	const beforeRewards = 3
	require.Greater(t, schemaVersion, uint64(beforeRewards))
	dir := filepath.Join(t.TempDir(), "index")
	indexOfLayout(t, dir, beforeRewards)

	res, err := MoveAsideStale(dir, movedAt)
	require.NoError(t, err)
	require.True(t, res.Present)
	require.Equal(t, uint64(beforeRewards), res.Version)
	require.Equal(t, dir+".v3-20261011T013005Z", res.MovedTo)

	fresh, err := Open(dir)
	require.NoError(t, err)
	defer fresh.Close()
	_, err = os.Stat(res.MovedTo)
	require.NoError(t, err, "the old index is kept")
}

func TestMoveAsideStale_aSymlinkedIndexDirectoryIsRefusedAndWhatItPointsAtIsLeftAlone(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "elsewhere")
	indexOfLayout(t, target, schemaVersion-1)
	dir := filepath.Join(root, "index")
	require.NoError(t, os.Symlink(target, dir))

	res, err := MoveAsideStale(dir, movedAt)

	require.ErrorContains(t, err, "symbolic link")
	require.Equal(t, MoveResult{Current: schemaVersion}, res, "nothing was found, so nothing was read")
	_, err = os.Lstat(dir)
	require.NoError(t, err, "the link is still where it was")
	_, err = os.Stat(filepath.Join(target, pebbleCurrent))
	require.NoError(t, err, "and what it points at was not opened, renamed or removed")
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Len(t, entries, 2, "no moved-aside copy appeared beside it")
}

func TestMoveAsideStale_anIndexThatIsNotADirectoryIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	require.NoError(t, os.WriteFile(dir, []byte("not an index"), 0o600))

	_, err := MoveAsideStale(dir, movedAt)

	require.ErrorContains(t, err, "not a directory")
	_, err = os.Stat(dir)
	require.NoError(t, err, "the file is left where it is")
}

func TestMoveAsideStale_aCurrentFileThatIsALinkIsRefused(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "index")
	require.NoError(t, os.Mkdir(dir, 0o700))
	secret := filepath.Join(root, "secret")
	require.NoError(t, os.WriteFile(secret, []byte("MANIFEST-000001\n"), 0o600))
	require.NoError(t, os.Symlink(secret, filepath.Join(dir, pebbleCurrent)))

	_, err := MoveAsideStale(dir, movedAt)

	require.ErrorContains(t, err, "not a regular file")
	_, err = os.Stat(dir)
	require.NoError(t, err, "the directory is not moved")
}

func TestMoveAsideStale_aDirectoryWithoutACurrentFileHoldsNoIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	require.NoError(t, os.Mkdir(dir, 0o700))

	res, err := MoveAsideStale(dir, movedAt)

	require.NoError(t, err)
	require.False(t, res.Present)
}
