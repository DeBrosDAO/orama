package reporter

import (
	"errors"
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

// errUnusableVote marks a file that is not a vote, deterministically: reading
// it again gives the same answer. LoadVotes leaves such a file out; any other
// failure to read one is returned.
var errUnusableVote = errors.New("not a usable vote file")

// voteOpen is how a vote file is opened; a test fails it to exercise LoadVotes
// on an open that fails for a reason other than the file.
var voteOpen = func(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

// LoadVotes reads the regular files named *.vote in dir that hold a vote of
// authority inside the window. It reads a file's header first and the whole file
// only for a match. A directory entry that is not a regular file (a link, a
// FIFO) is not read, and a .vote file that is not a vote, deterministically (it
// does not parse, it is larger than a vote, or it turns out not to be a regular
// file once opened) is logged as an error naming it and left out: one damaged
// file in the archive must not stop the reporter from reporting the epochs the
// rest of it covers. Whoever recomputes the observations reads the same files
// and skips the same ones.
//
// A failure that says nothing about the file (it cannot be opened or its read
// fails: an I/O error, a descriptor limit, a file that vanished since the
// listing) is returned, so the run is retried. Leaving such a file out would
// build, sign and save a report from a partial vote set that no later attempt
// would recompute.
//
// The directory is written by the authority's account, which runs a Tor
// process facing the network, and read here by the account that holds the
// reporter's signing key. A file is therefore opened without following a link
// and without waiting on a FIFO, so a link could not point the reader at the
// hot key.
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
			if skipVote(path, err) {
				continue
			}
			return nil, err
		}
		if head.Authority != authority || head.ValidAfter.Before(w.From) || !head.ValidAfter.Before(w.To) {
			continue
		}
		full, err := readVote(path, false)
		if err != nil {
			if skipVote(path, err) {
				continue
			}
			return nil, err
		}
		votes = append(votes, full)
	}
	return Select(votes, authority, w)
}

// skipVote reports whether err is a fault of the file itself, and logs it if so.
func skipVote(path string, err error) bool {
	if !errors.Is(err, errUnusableVote) {
		return false
	}
	slog.Error("leaving out a vote file that is not a vote", "path", path, "err", err)
	return true
}

func readVote(path string, headerOnly bool) (Vote, error) {
	f, err := voteOpen(path)
	if errors.Is(err, syscall.ELOOP) {
		return Vote{}, fmt.Errorf("%w: vote %s is a link, not a regular file", errUnusableVote, path)
	} else if err != nil {
		return Vote{}, fmt.Errorf("open vote %s: %w", path, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Vote{}, fmt.Errorf("stat vote %s: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		return Vote{}, fmt.Errorf("%w: vote %s is not a regular file", errUnusableVote, path)
	}
	return parseVoteFile(path, f, headerOnly)
}

// failureTracker remembers the first error its reader returned that is not the
// end of the file, to tell a read that failed from a document that is wrong.
type failureTracker struct {
	r   io.Reader
	err error
}

func (t *failureTracker) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF && t.err == nil {
		t.err = err
	}
	return n, err
}

// parseVoteFile parses the vote read from r. A read that failed is returned as
// it is; a document that does not parse, or is larger than a vote, is
// errUnusableVote.
func parseVoteFile(path string, r io.Reader, headerOnly bool) (Vote, error) {
	parse := ParseVote
	if headerOnly {
		parse = parseHeader
	}
	tracked := &failureTracker{r: r}
	v, err := parse(tracked)
	if tracked.err != nil {
		return Vote{}, fmt.Errorf("read vote %s: %w", path, tracked.err)
	}
	if err != nil {
		return Vote{}, fmt.Errorf("%w: vote %s: %w", errUnusableVote, path, err)
	}
	return v, nil
}

// parseHeader is ParseVoteHeader on at most maxVoteBytes: a header ends at the
// first relay, and a file with none is not read to its end.
func parseHeader(r io.Reader) (Vote, error) {
	return ParseVoteHeader(io.LimitReader(r, maxVoteBytes))
}
