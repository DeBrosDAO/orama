package reporter

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeVote(t *testing.T, dir, name string, validAfter time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(testVote(testAuthorityHex, validAfter, relaySpec{n: 1, flags: "Running"})), 0o640))
	return path
}

func TestLoadVotes_readsTheRegularVoteFilesOfTheWindow(t *testing.T) {
	dir := t.TempDir()
	writeVote(t, dir, "a"+VoteSuffix, epochStart.Add(time.Hour))
	writeVote(t, dir, "outside"+VoteSuffix, epochStart.Add(48*time.Hour))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "consensus"), []byte("not a vote"), 0o640))
	votes, err := LoadVotes(dir, testAuthority(), dayWindow())
	require.NoError(t, err)
	require.Len(t, votes, 1)
}

func TestLoadVotes_missingDirectoryIsAnError(t *testing.T) {
	_, err := LoadVotes(filepath.Join(t.TempDir(), "absent"), testAuthority(), dayWindow())
	require.ErrorContains(t, err, "read vote archive")
}

func TestLoadVotes_emptyDirectoryHoldsNoVotes(t *testing.T) {
	votes, err := LoadVotes(t.TempDir(), testAuthority(), dayWindow())
	require.NoError(t, err)
	require.Empty(t, votes)
}

// The votes directory is written by the authority's account; a link in it must
// not make the reporter, which holds a signing key, read another file.
func TestLoadVotes_neverFollowsALink(t *testing.T) {
	dir := t.TempDir()
	target := writeVote(t, t.TempDir(), "target.txt", epochStart.Add(time.Hour))
	require.NoError(t, os.Symlink(target, filepath.Join(dir, "link"+VoteSuffix)))
	votes, err := LoadVotes(dir, testAuthority(), dayWindow())
	require.NoError(t, err, "a link is not a regular file: the directory listing skips it")
	require.Empty(t, votes)

	// The listing says regular, then the file is swapped for a link before the open.
	require.NoError(t, os.Symlink(target, filepath.Join(dir, "swapped")))
	_, err = readVote(filepath.Join(dir, "swapped"), false)
	require.Error(t, err)
	_, err = readVote(filepath.Join(dir, "swapped"), true)
	require.Error(t, err)
}

func TestLoadVotes_aFIFODoesNotHangTheReader(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe"+VoteSuffix)
	if err := exec.Command("mkfifo", fifo).Run(); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := readVote(fifo, false); done <- err }()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "not a regular file")
	case <-time.After(10 * time.Second):
		t.Fatal("the reader waited on a FIFO")
	}
}
