package reporter

import (
	"bytes"
	"log/slog"
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

// One damaged file in the archive does not stop the reporter from reading the
// rest: it is logged as an error naming it and left out.
func TestLoadVotes_aDamagedFileIsLoggedAndSkipped(t *testing.T) {
	dir := t.TempDir()
	writeVote(t, dir, "good"+VoteSuffix, epochStart.Add(time.Hour))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "garbage"+VoteSuffix), []byte("this is not a vote\n"), 0o640))
	// A vote cut off before its footer, as one still being written is.
	whole := testVote(testAuthorityHex, epochStart.Add(2*time.Hour), relaySpec{n: 2, flags: "Running"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cut"+VoteSuffix), []byte(whole[:len(whole)-len("directory-footer\n")-60]), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "empty"+VoteSuffix), nil, 0o640))

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	votes, err := LoadVotes(dir, testAuthority(), dayWindow())
	require.NoError(t, err)
	require.Len(t, votes, 1, "the good vote is still read")
	out := logs.String()
	require.Contains(t, out, "level=ERROR")
	require.Contains(t, out, "garbage"+VoteSuffix)
	require.Contains(t, out, "empty"+VoteSuffix)
}

// A swapped-in link is skipped like any other file that cannot be read.
func TestLoadVotes_aFileSwappedForALinkIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	writeVote(t, dir, "good"+VoteSuffix, epochStart.Add(time.Hour))
	target := writeVote(t, t.TempDir(), "target.txt", epochStart.Add(time.Hour))
	// The directory listing reports a regular file; the open finds a link.
	_, err := readVote(target, true)
	require.NoError(t, err)
	require.NoError(t, os.Symlink(target, filepath.Join(dir, "link"+VoteSuffix)))
	votes, err := LoadVotes(dir, testAuthority(), dayWindow())
	require.NoError(t, err)
	require.Len(t, votes, 1)
}

// endlessHeader is a document that never reaches its first relay.
type endlessHeader struct{ read int }

func (e *endlessHeader) Read(p []byte) (int, error) {
	const line = "# nothing a header reads\n"
	n := 0
	for n+len(line) <= len(p) {
		n += copy(p[n:], line)
	}
	e.read += n
	return n, nil
}

// The header of a file with no relay in it is read up to the size of a vote and
// no further.
func TestParseHeader_isBoundedByTheSizeOfAVote(t *testing.T) {
	src := &endlessHeader{}
	_, err := parseHeader(src)
	require.Error(t, err, "a header with no authority is not a vote")
	require.LessOrEqual(t, src.read, maxVoteBytes+(64<<10), "read well past the largest vote")
}
