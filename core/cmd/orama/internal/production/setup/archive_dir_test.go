package setup

import (
	"strings"
	"testing"
)

func TestStageArchiveCommand_aDirectoryUnderVarTmpStagesFromThereAndNamesItsCLIDirectoryByItsSuffix(t *testing.T) {
	const dir = "/var/tmp/orama-archive.AbC12345"
	got, err := StageArchiveCommand(dir, testCLISum, []string{operatorWallet})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{dir + "/archive.tar.gz", "/opt/orama/.archive-cli-AbC12345/bin/orama node stage-archive", "rm -rf " + dir + " /opt/orama/.archive-cli-AbC12345"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"/var/tmp/other.AbC12345", "/var/tmp/orama-archive.a'b12345", "/var/tmp/x/orama-archive.AbC12345", "/var/tmp/orama-archive.AbC1234"} {
		if ValidArchiveDir(bad) {
			t.Errorf("%q passed as an archive directory", bad)
		}
	}
}

func TestNewArchiveDir_isRandomAndValid(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		dir, err := NewArchiveDir()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidArchiveDir(dir) || !strings.HasPrefix(dir, ArchiveDirPrefix) {
			t.Fatalf("%q is not an archive directory", dir)
		}
		seen[dir] = true
	}
	if len(seen) < 19 {
		t.Errorf("20 names gave %d different ones: the name is not random", len(seen))
	}
}
