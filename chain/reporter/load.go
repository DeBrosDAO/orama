package reporter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VoteSuffix names the archived votes in the votes directory. The archive
// (E2) also holds consensus documents and bandwidth files; only files with
// this suffix are read.
const VoteSuffix = ".vote"

// LoadVotes reads the regular files named *.vote in dir that hold a vote of
// authority inside the window. It reads a file's header first and the whole file
// only for a match. A .vote file that does not parse is an error naming it: an archive
// with a damaged vote must be repaired, not reported around.
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
			return nil, err
		}
		if head.Authority != authority || head.ValidAfter.Before(w.From) || !head.ValidAfter.Before(w.To) {
			continue
		}
		full, err := readVote(path, false)
		if err != nil {
			return nil, err
		}
		votes = append(votes, full)
	}
	return Select(votes, authority, w)
}

func readVote(path string, headerOnly bool) (Vote, error) {
	f, err := os.Open(path)
	if err != nil {
		return Vote{}, fmt.Errorf("open vote %s: %w", path, err)
	}
	defer f.Close()
	parse := ParseVote
	if headerOnly {
		parse = ParseVoteHeader
	}
	v, err := parse(f)
	if err != nil {
		return Vote{}, fmt.Errorf("vote %s: %w", path, err)
	}
	return v, nil
}
