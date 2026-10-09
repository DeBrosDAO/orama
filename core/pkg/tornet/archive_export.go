package tornet

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// VoteExportSuffix names an exported vote. The bandwidth reporter
	// (chain/reporter, VoteSuffix) reads the files of its votes directory with
	// this suffix and no others.
	VoteExportSuffix = ".vote"
	// exportFileMode is read by the group of the votes directory, the
	// reporter's: the directory is setgid, so a file made in it has that group.
	exportFileMode = 0o640

	voteStatusLine = "\nvote-status vote\n"
	voteFooterLine = "\ndirectory-footer\n"
	dirSourceKey   = "dir-source"
	// dirSourceFields: dir-source <nickname> <v3 identity> <hostname> <address> <dirport> <orport>.
	dirSourceFields = 7
)

// ExportOwnVote copies this authority's own vote of the consensus's voting
// period out of its DataDirectory to <exportDir>/<valid-after>.vote, for the
// bandwidth reporter, which runs as an account of its own and cannot read the
// DataDirectory (it holds the authority's keys). Only the authority's own vote
// is exported, not the others' it also holds, and nothing else of the
// DataDirectory.
//
// The authority is the one whose certificate is in the DataDirectory. A vote
// that ends before its directory-footer is refused, and a period already
// exported is left as it is, like the archive. The directory must exist: the
// install makes it, owned by the authority's account and shared read-only with
// the reporter's group. It reports whether a file was written; a period whose
// votes the authority does not hold writes nothing and is not an error.
func ExportOwnVote(dataDir, exportDir string) (bool, error) {
	consensusRaw, err := readLimited(filepath.Join(dataDir, DataDirConsensus))
	if err != nil {
		return false, fmt.Errorf("the authority has no consensus to export a vote for: %w", err)
	}
	c, err := ParseConsensus(bytes.NewReader(consensusRaw))
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", DataDirConsensus, err)
	}
	votes, err := votesOfPeriod(dataDir, c.ValidAfter.Format(consensusTimeLayout))
	if err != nil || votes == nil {
		return false, err
	}
	cert, err := readLimited(filepath.Join(dataDir, ceremonyKeysDir, KeyAuthorityCert))
	if err != nil {
		return false, fmt.Errorf("read the authority's certificate: %w", err)
	}
	own, _, err := ParseAuthorityCertificate(string(cert))
	if err != nil {
		return false, fmt.Errorf("the authority's certificate: %w", err)
	}
	vote, err := ownVote(votes, own)
	if err != nil {
		return false, err
	}
	path := filepath.Join(exportDir, c.ValidAfter.Format(archiveStampLayout)+VoteExportSuffix)
	if _, err := os.Lstat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("check %s: %w", path, err)
	}
	if err := writeAtomic(path, vote, exportFileMode); err != nil {
		return false, err
	}
	return true, nil
}

// ownVote is the document of the votes made by the authority with v3 identity
// own. A vote is "vote-status vote", never the consensus.
func ownVote(votes []byte, own string) ([]byte, error) {
	for _, doc := range splitDocuments(votes) {
		if !bytes.Contains(doc, []byte(voteStatusLine)) || voteSource(doc) != own {
			continue
		}
		if !bytes.Contains(doc, []byte(voteFooterLine)) {
			return nil, fmt.Errorf("the authority's own vote %s has no directory-footer: it is not complete", own)
		}
		return doc, nil
	}
	return nil, fmt.Errorf("the authority's own vote (identity %s) is not among the votes it holds for this period", own)
}

// voteSource is the v3 identity on a vote's dir-source line, upper case, or "".
func voteSource(doc []byte) string {
	for _, line := range strings.Split(string(doc), "\n") {
		if !strings.HasPrefix(line, dirSourceKey+" ") {
			continue
		}
		if f := strings.Fields(line); len(f) == dirSourceFields {
			return strings.ToUpper(f[2])
		}
		return ""
	}
	return ""
}
