package tornet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// Archive file names, and the files of a directory authority's DataDirectory
// they are copied from.
const (
	ArchiveConsensusFile = "consensus"
	ArchiveVotesFile     = "votes"
	ArchiveBandwidthFile = "bandwidth"
	ArchiveManifestFile  = "MANIFEST.json"

	// DataDirConsensus and DataDirVotes are the files an authority keeps: the
	// consensus it holds and the votes of the round that made it.
	DataDirConsensus = "cached-consensus"
	DataDirVotes     = "v3-status-votes"

	archiveStampLayout = "20060102T150405Z"
	archiveFileLimit   = 64 << 20
	archiveDirMode     = 0o700
	archiveFileMode    = 0o600
	voteDocHeader      = "network-status-version "
)

// Manifest lists the files of one archived voting period and their SHA-256.
// Root is the digest a reporter commits to (the inputs_root of a relay
// report): SHA-256 over the lines "<name> <sha256>\n" in name order.
type Manifest struct {
	ValidAfter string            `json:"valid_after"`
	Files      map[string]string `json:"files"`
	// VotesMissing is true when the authority held no votes of the consensus's
	// voting period (they were already replaced by the next round's).
	VotesMissing bool   `json:"votes_missing,omitempty"`
	Root         string `json:"root"`
}

// ManifestRoot is the Root of files (name to hex SHA-256).
func ManifestRoot(files map[string]string) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%s %s\n", n, files[n])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ArchiveVotingPeriod copies the consensus a directory authority holds, the
// votes that made it and, when bandwidthFile is not empty, the bandwidth file
// it voted with, into <archiveDir>/<valid-after>/ with a manifest. It is safe
// to run as often as you like: a period already archived is left as it is, a
// file that is missing is added, and a file whose bytes differ from the
// archived ones is kept (see keepArchived).
//
// It returns the manifest, and whether anything was written.
func ArchiveVotingPeriod(dataDir, archiveDir, bandwidthFile string) (Manifest, bool, error) {
	consensusRaw, err := readLimited(filepath.Join(dataDir, DataDirConsensus))
	if err != nil {
		return Manifest{}, false, fmt.Errorf("the authority has no consensus to archive: %w", err)
	}
	c, err := ParseConsensus(bytes.NewReader(consensusRaw))
	if err != nil {
		return Manifest{}, false, fmt.Errorf("parse %s: %w", DataDirConsensus, err)
	}
	validAfter := c.ValidAfter.Format(consensusTimeLayout)
	files := map[string][]byte{ArchiveConsensusFile: consensusRaw}
	votes, err := votesOfPeriod(dataDir, validAfter)
	if err != nil {
		return Manifest{}, false, err
	}
	if votes != nil {
		files[ArchiveVotesFile] = votes
	}
	if bandwidthFile != "" {
		bw, err := readLimited(bandwidthFile)
		if err != nil {
			return Manifest{}, false, fmt.Errorf("read the bandwidth file: %w", err)
		}
		files[ArchiveBandwidthFile] = bw
	}
	return writeArchive(filepath.Join(archiveDir, c.ValidAfter.Format(archiveStampLayout)), validAfter, files, votes == nil)
}

func writeArchive(dir, validAfter string, files map[string][]byte, votesMissing bool) (Manifest, bool, error) {
	if err := os.MkdirAll(dir, archiveDirMode); err != nil {
		return Manifest{}, false, fmt.Errorf("create %s: %w", dir, err)
	}
	sums := map[string]string{}
	wrote := false
	for name, data := range files {
		path := filepath.Join(dir, name)
		existing, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Manifest{}, false, fmt.Errorf("read %s: %w", path, err)
		}
		keep, err := keepArchived(name, existing, data)
		if err != nil {
			return Manifest{}, false, fmt.Errorf("%s: %w", path, err)
		}
		if keep {
			data = existing
		} else {
			if err := writeAtomic(path, data, archiveFileMode); err != nil {
				return Manifest{}, false, err
			}
			wrote = true
		}
		sum := sha256.Sum256(data)
		sums[name] = hex.EncodeToString(sum[:])
	}
	m := Manifest{ValidAfter: validAfter, Files: sums, VotesMissing: votesMissing, Root: ManifestRoot(sums)}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Manifest{}, false, fmt.Errorf("encode the manifest: %w", err)
	}
	body = append(body, '\n')
	manifestPath := filepath.Join(dir, ArchiveManifestFile)
	if old, err := os.ReadFile(manifestPath); err != nil || !bytes.Equal(old, body) {
		if err := writeAtomic(manifestPath, body, archiveFileMode); err != nil {
			return Manifest{}, false, err
		}
		wrote = true
	}
	return m, wrote, nil
}

// keepArchived decides between the archived bytes and a new copy of a file.
// The first copy of the votes and of the bandwidth file wins. A consensus is
// replaced only by the same consensus with more signatures on it: signatures
// keep arriving after it is first cached, and two consensuses with different
// bodies for one period are an anomaly, not an update.
func keepArchived(name string, existing, data []byte) (bool, error) {
	if existing == nil {
		return false, nil
	}
	if name != ArchiveConsensusFile {
		return true, nil
	}
	if bytes.Equal(existing, data) {
		return true, nil
	}
	if !bytes.Equal(consensusBody(existing), consensusBody(data)) {
		return false, errors.New("this voting period is already archived with a different consensus; an archived period is never rewritten")
	}
	return bytes.Count(existing, []byte("\ndirectory-signature ")) >= bytes.Count(data, []byte("\ndirectory-signature ")), nil
}

// consensusBody is a consensus without its signatures.
func consensusBody(raw []byte) []byte {
	if i := bytes.Index(raw, []byte("\ndirectory-signature ")); i >= 0 {
		return raw[:i]
	}
	return raw
}

// votesOfPeriod is the part of the authority's vote file whose documents are
// for the voting period starting at validAfter, or nil when there is none.
func votesOfPeriod(dataDir, validAfter string) ([]byte, error) {
	raw, err := readLimited(filepath.Join(dataDir, DataDirVotes))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", DataDirVotes, err)
	}
	var out bytes.Buffer
	for _, doc := range splitDocuments(raw) {
		if bytes.Contains(doc, []byte("\nvalid-after "+validAfter+"\n")) {
			out.Write(doc)
		}
	}
	if out.Len() == 0 {
		return nil, nil
	}
	return out.Bytes(), nil
}

// splitDocuments cuts concatenated network-status documents at their version lines.
func splitDocuments(raw []byte) [][]byte {
	var docs [][]byte
	start := -1
	for i := 0; i < len(raw); {
		end := len(raw)
		if j := bytes.IndexByte(raw[i:], '\n'); j >= 0 {
			end = i + j + 1
		}
		if strings.HasPrefix(string(raw[i:end]), voteDocHeader) {
			if start >= 0 {
				docs = append(docs, raw[start:i])
			}
			start = i
		}
		i = end
	}
	if start >= 0 {
		docs = append(docs, raw[start:])
	}
	return docs
}

// readLimited reads a whole file, refusing one larger than archiveFileLimit.
func readLimited(path string) ([]byte, error) {
	// O_NONBLOCK: opening a FIFO a hostile directory holds must not hang the reader.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, archiveFileLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > archiveFileLimit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, archiveFileLimit)
	}
	return data, nil
}

// writeAtomic writes data to a new file beside path and renames it over path,
// so a reader sees the old file or the new one and never half of either.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create a temporary file beside %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("move %s into place: %w", path, err)
	}
	return nil
}
