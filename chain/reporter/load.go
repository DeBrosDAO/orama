package reporter

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// VoteSuffix names the archived votes in the votes directory. The archive
// (E2) also holds consensus documents and bandwidth files; only files with
// this suffix are read.
const VoteSuffix = ".vote"

// LoadVotes reads the regular files named *.vote in dir that hold a vote of
// authority inside the window. It reads a file's header first and the whole file
// only for a match. A .vote file that cannot be read or does not parse is logged
// as an error naming it and left out: one damaged file in the archive must not
// stop the reporter from reporting the epochs the rest of it covers. Whoever
// recomputes the observations reads the same files and skips the same ones.
//
// The directory is written by the authority's account, which runs a Tor
// process facing the network, and read here by the account that holds the
// reporter's signing key. A file is therefore opened without following a link
// and without waiting on a FIFO, and one that is not a regular file is an
// error: a link could otherwise point the reader at the hot key.
func LoadVotes(dir string, authority [fingerprintLen]byte, w Window) ([]Vote, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read vote archive %s: %w", dir, err)
	}
	var votes []Vote
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), VoteSuffix) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		head, err := readVote(path, true)
		if err != nil {
			slog.Error("leaving out a vote file that cannot be read", "path", path, "err", err)
			continue
		}
		if head.Authority != authority || head.ValidAfter.Before(w.From) || !head.ValidAfter.Before(w.To) {
			continue
		}
		full, err := readVote(path, false)
		if err != nil {
			slog.Error("leaving out a vote file that cannot be read", "path", path, "err", err)
			continue
		}
		votes = append(votes, full)
	}
	return Select(votes, authority, w)
}

func readVote(path string, headerOnly bool) (Vote, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Vote{}, fmt.Errorf("open vote %s: %w", path, err)
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return Vote{}, fmt.Errorf("vote %s is not a regular file", path)
	}
	parse := ParseVote
	if headerOnly {
		parse = parseHeader
	}
	v, err := parse(f)
	if err != nil {
		return Vote{}, fmt.Errorf("vote %s: %w", path, err)
	}
	return v, nil
}

// parseHeader is ParseVoteHeader on at most maxVoteBytes: a header ends at the
// first relay, and a file with none is not read to its end.
func parseHeader(r io.Reader) (Vote, error) {
	return ParseVoteHeader(io.LimitReader(r, maxVoteBytes))
}
