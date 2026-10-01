package migrations

import (
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// selfRecord is a migration file recording its own version.
var selfRecord = regexp.MustCompile(`INSERT OR IGNORE INTO schema_migrations\s*\(version\)\s*VALUES\s*\((\d+)\)`)

// TestMigrations_selfRecordNamesTheirOwnVersion: a file renumbered without
// its own record (068 said 67) would mark another migration applied on a
// database that has not run it.
func TestMigrations_selfRecordNamesTheirOwnVersion(t *testing.T) {
	names, err := fs.Glob(FS, "*.sql")
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no embedded migrations")
	}
	for _, name := range names {
		num, _, ok := strings.Cut(name, "_")
		if !ok {
			t.Errorf("%s has no NNN_ prefix", name)
			continue
		}
		want, err := strconv.Atoi(num)
		if err != nil {
			t.Errorf("%s: prefix %q is not a number", name, num)
			continue
		}
		body, err := fs.ReadFile(FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range selfRecord.FindAllSubmatch(body, -1) {
			if got, _ := strconv.Atoi(string(m[1])); got != want {
				t.Errorf("%s records schema_migrations version %d, want %d", name, got, want)
			}
		}
	}
}
