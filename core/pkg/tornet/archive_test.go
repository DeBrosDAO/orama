package tornet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dataDir is an authority's DataDirectory holding the fixture consensus and votes.
func dataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, DataDirConsensus), readFixture(t, "consensus-microdesc.txt"))
	write(t, filepath.Join(dir, DataDirVotes), readFixture(t, "votes.txt"))
	return dir
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveVotingPeriod_archivesConsensusVotesAndBandwidth(t *testing.T) {
	dir, archive := dataDir(t), t.TempDir()
	bw := filepath.Join(t.TempDir(), "latest.v1")
	write(t, bw, readFixture(t, "bandwidth-file.txt"))
	m, wrote, err := ArchiveVotingPeriod(dir, archive, bw)
	if err != nil || !wrote {
		t.Fatalf("archive: wrote %v err %v", wrote, err)
	}
	period := filepath.Join(archive, "20261008T120000Z")
	for _, f := range []string{ArchiveConsensusFile, ArchiveVotesFile, ArchiveBandwidthFile, ArchiveManifestFile} {
		if _, err := os.Stat(filepath.Join(period, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if m.VotesMissing || m.ValidAfter != "2026-10-08 12:00:00" || len(m.Files) != 3 {
		t.Fatalf("manifest = %+v", m)
	}
	if m.Root != ManifestRoot(m.Files) || len(m.Root) != 64 {
		t.Errorf("root %q is not the digest of the file digests", m.Root)
	}
	got, err := os.ReadFile(filepath.Join(period, ArchiveVotesFile))
	if err != nil || strings.Count(string(got), "vote-status vote") != 2 {
		t.Errorf("both votes are archived: %v\n%s", err, got)
	}
}

func TestArchiveVotingPeriod_isIdempotent(t *testing.T) {
	dir, archive := dataDir(t), t.TempDir()
	first, _, err := ArchiveVotingPeriod(dir, archive, "")
	if err != nil {
		t.Fatal(err)
	}
	second, wrote, err := ArchiveVotingPeriod(dir, archive, "")
	if err != nil {
		t.Fatal(err)
	}
	if wrote || second.Root != first.Root {
		t.Fatalf("a second run wrote %v and changed the root %s -> %s", wrote, first.Root, second.Root)
	}
}

func TestArchiveVotingPeriod_votesOfAnotherPeriodAreNotArchived(t *testing.T) {
	dir, archive := dataDir(t), t.TempDir()
	stale := strings.ReplaceAll(string(readFixture(t, "votes.txt")), "valid-after 2026-10-08 12:00:00", "valid-after 2026-10-08 12:30:00")
	write(t, filepath.Join(dir, DataDirVotes), []byte(stale))
	m, _, err := ArchiveVotingPeriod(dir, archive, "")
	if err != nil {
		t.Fatal(err)
	}
	if !m.VotesMissing || len(m.Files) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	if _, err := os.Stat(filepath.Join(archive, "20261008T120000Z", ArchiveVotesFile)); err == nil {
		t.Fatal("votes of the next round were archived as this period's")
	}
	// The right votes arrive later in the period: they are added.
	write(t, filepath.Join(dir, DataDirVotes), readFixture(t, "votes.txt"))
	m2, wrote, err := ArchiveVotingPeriod(dir, archive, "")
	if err != nil || !wrote || m2.VotesMissing || len(m2.Files) != 2 {
		t.Fatalf("late votes: %+v wrote %v err %v", m2, wrote, err)
	}
}

func TestArchiveVotingPeriod_moreSignaturesReplaceTheConsensusOnly(t *testing.T) {
	dir, archive := dataDir(t), t.TempDir()
	full := string(readFixture(t, "consensus-microdesc.txt"))
	// The authority first caches the consensus with one signature.
	first := full[:strings.LastIndex(full, "directory-signature")]
	write(t, filepath.Join(dir, DataDirConsensus), []byte(first))
	if _, _, err := ArchiveVotingPeriod(dir, archive, ""); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, DataDirConsensus), []byte(full))
	m, wrote, err := ArchiveVotingPeriod(dir, archive, "")
	if err != nil || !wrote {
		t.Fatalf("a better-signed consensus was not archived: %v wrote %v", err, wrote)
	}
	got, _ := os.ReadFile(filepath.Join(archive, "20261008T120000Z", ArchiveConsensusFile))
	if string(got) != full {
		t.Fatal("the archived consensus is not the fully signed one")
	}
	// Fewer signatures later do not take signatures away.
	write(t, filepath.Join(dir, DataDirConsensus), []byte(first))
	m2, _, err := ArchiveVotingPeriod(dir, archive, "")
	if err != nil || m2.Root != m.Root {
		t.Fatalf("a less-signed copy changed the archive: %v %s vs %s", err, m2.Root, m.Root)
	}
}

func TestArchiveVotingPeriod_aDifferentConsensusForTheSamePeriodIsAnError(t *testing.T) {
	dir, archive := dataDir(t), t.TempDir()
	if _, _, err := ArchiveVotingPeriod(dir, archive, ""); err != nil {
		t.Fatal(err)
	}
	forged := strings.Replace(string(readFixture(t, "consensus-microdesc.txt")), "OramaRelayA", "OramaRelayZ", 1)
	write(t, filepath.Join(dir, DataDirConsensus), []byte(forged))
	if _, _, err := ArchiveVotingPeriod(dir, archive, ""); err == nil || !strings.Contains(err.Error(), "never rewritten") {
		t.Fatalf("err = %v", err)
	}
}

func TestArchiveVotingPeriod_failures(t *testing.T) {
	archive := t.TempDir()
	if _, wrote, err := ArchiveVotingPeriod(t.TempDir(), archive, ""); !errors.Is(err, ErrNoConsensusYet) || wrote {
		t.Fatalf("an authority with no consensus yet: wrote %v, err %v; want ErrNoConsensusYet and nothing written", wrote, err)
	}
	if entries, _ := os.ReadDir(archive); len(entries) != 0 {
		t.Fatalf("an authority with no consensus yet left %d entries in the archive", len(entries))
	}
	unreadable := t.TempDir()
	if err := os.Mkdir(filepath.Join(unreadable, DataDirConsensus), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ArchiveVotingPeriod(unreadable, t.TempDir(), ""); err == nil || errors.Is(err, ErrNoConsensusYet) {
		t.Fatalf("a consensus that cannot be read must fail, not read as none yet: %v", err)
	}
	dir := dataDir(t)
	write(t, filepath.Join(dir, DataDirConsensus), []byte("garbage\n"))
	if _, _, err := ArchiveVotingPeriod(dir, t.TempDir(), ""); err == nil {
		t.Fatal("garbage was archived")
	}
	dir = dataDir(t)
	if _, _, err := ArchiveVotingPeriod(dir, t.TempDir(), filepath.Join(dir, "no-such-bandwidth-file")); err == nil {
		t.Fatal("a missing bandwidth file was ignored")
	}
}

func TestManifestRoot_dependsOnNamesAndDigests(t *testing.T) {
	a := ManifestRoot(map[string]string{"consensus": "aa", "votes": "bb"})
	if a != ManifestRoot(map[string]string{"votes": "bb", "consensus": "aa"}) {
		t.Fatal("root depends on map order")
	}
	if a == ManifestRoot(map[string]string{"consensus": "aa", "votes": "bc"}) || a == ManifestRoot(map[string]string{"consensus": "aa"}) {
		t.Fatal("root ignores a digest or a file")
	}
}

func TestSplitDocuments(t *testing.T) {
	docs := splitDocuments(readFixture(t, "votes.txt"))
	if len(docs) != 2 || !strings.HasPrefix(string(docs[1]), "network-status-version 3\n") {
		t.Fatalf("%d documents", len(docs))
	}
	if got := splitDocuments(nil); len(got) != 0 {
		t.Fatal("documents in nothing")
	}
}
