package rqlite

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
)

// schemaEventRe finds every statement that creates, removes or renames a table,
// in the order they appear. One expression rather than three, because the order
// between them is the whole point: a rebuild drops `x`, then renames `x_new`
// over it, and `x` is very much still there afterwards.
var schemaEventRe = regexp.MustCompile(`(?is)` +
	`CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["'` + "`" + `]?([A-Za-z_][A-Za-z0-9_]*)` +
	`|DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?["'` + "`" + `]?([A-Za-z_][A-Za-z0-9_]*)` +
	`|ALTER\s+TABLE\s+["'` + "`" + `]?([A-Za-z_][A-Za-z0-9_]*)["'` + "`" + `]?\s+RENAME\s+TO\s+["'` + "`" + `]?([A-Za-z_][A-Za-z0-9_]*)`)

// liveTables is every table that exists once the embedded core migrations have
// all been applied.
//
// A table that is gone by the end needs no placement, and requiring one would
// be a decision about something that does not exist. A table a rebuild renames
// over does exist, which is why this replays the statements in order rather
// than collecting each kind separately.
func liveTables(t *testing.T) []string {
	t.Helper()

	live := map[string]struct{}{}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range schemaEventRe.FindAllStringSubmatch(string(body), -1) {
			switch {
			case m[1] != "":
				live[strings.ToLower(m[1])] = struct{}{}
			case m[2] != "":
				delete(live, strings.ToLower(m[2]))
			case m[3] != "" && m[4] != "":
				delete(live, strings.ToLower(m[3]))
				live[strings.ToLower(m[4])] = struct{}{}
			}
		}
	}

	out := make([]string, 0, len(live))
	for table := range live {
		out = append(out, table)
	}
	sort.Strings(out)
	return out
}

// A table nobody has placed ends up in both databases, which is how a tenant's
// application database came to hold `api_keys`. Deciding is one line; the
// decision is what this test is for.
func TestEveryCoreTableIsPlaced(t *testing.T) {
	for _, table := range liveTables(t) {
		if _, ok := PlacementOf(table); !ok {
			t.Errorf("migration creates %q and nothing says which database it belongs in. "+
				"Add it to tablePlacement: PlacementCluster if only the index reads it, "+
				"PlacementNamespace if a namespace gateway reads it on its own RQLite.", table)
		}
	}
}

// The reverse: a placement for a table nothing creates is a decision about
// something that no longer exists, and it reads as coverage.
func TestEveryPlacementNamesARealTable(t *testing.T) {
	created := map[string]struct{}{}
	for _, table := range liveTables(t) {
		created[table] = struct{}{}
	}
	for table := range tablePlacement {
		if _, ok := created[table]; !ok {
			t.Errorf("tablePlacement names %q, which no migration leaves behind — it is never created, "+
				"or a later migration drops or renames it away", table)
		}
	}
}

// Every placement carries a reason. "Because it is" is not one, and the reason
// is what the next person changing it reads.
func TestEveryPlacementSaysWhy(t *testing.T) {
	for table, note := range tablePlacement {
		if strings.TrimSpace(note.Why) == "" {
			t.Errorf("%q is placed with no reason", table)
		}
	}
}

// Everything that lives only in the cluster registry is there because the
// platform acts on it, so it is platform-trust. A telemetry or tenant-data
// class on a registry table would exempt it from the guard cross-check in
// pkg/sqlguard while the table is still one a tenant must not name.
func TestClusterPlacementIsPlatformTrust(t *testing.T) {
	for _, table := range ClusterOnlyTables() {
		note, _ := PlacementOf(table)
		if note.Trust != TrustPlatform {
			t.Errorf("%q lives only in the cluster registry and is classed %d, not TrustPlatform", table, note.Trust)
		}
	}
}

// The classes partition the placement: every table is in exactly one, and the
// two that a tenant may write are not empty, so the trust field is being used
// rather than left at its default.
func TestTablesOfTrust_partitionThePlacement(t *testing.T) {
	seen := map[string]Trust{}
	for _, trust := range []Trust{TrustPlatform, TrustTenantData, TrustTelemetry} {
		tables := TablesOfTrust(trust)
		if len(tables) == 0 {
			t.Errorf("no table is classed %d", trust)
		}
		for _, table := range tables {
			if prev, dup := seen[table]; dup {
				t.Errorf("%q is in trust class %d and %d", table, prev, trust)
			}
			seen[table] = trust
		}
	}
	if len(seen) != len(tablePlacement) {
		t.Errorf("the classes cover %d tables, the placement has %d", len(seen), len(tablePlacement))
	}
}

// What a tenant's own application data and the platform's request log are
// classed as is a decision the guard depends on: a platform decision must never
// rest on them. Naming the ones that must stay out of TrustPlatform keeps a
// mass reclassification from silently widening the guard.
func TestTrust_tenantDataAndTelemetryAreNotPlatform(t *testing.T) {
	for _, table := range []string{"apps", "push_devices", "request_logs", "function_logs", "function_invocations"} {
		note, ok := PlacementOf(table)
		if !ok || note.Trust == TrustPlatform {
			t.Errorf("%q must not be platform-trust (placed: %v)", table, ok)
		}
	}
	for _, table := range []string{"functions", "function_cron_triggers", "deployment_history", "ipfs_content_ownership", "namespace_quotas"} {
		note, ok := PlacementOf(table)
		if !ok || note.Trust != TrustPlatform {
			t.Errorf("%q must be platform-trust (placed: %v)", table, ok)
		}
	}
}

// The stripped list is derived rather than kept beside the placements, because
// two lists that must agree are two lists that will not.
func TestClusterOnlyTablesAreWhatIsStripped(t *testing.T) {
	stripped := map[string]struct{}{}
	for _, table := range namespaceStrippedTables {
		stripped[strings.ToLower(table)] = struct{}{}
	}

	for _, table := range ClusterOnlyTables() {
		if _, ok := stripped[table]; !ok {
			t.Errorf("%q lives only in the cluster registry and is still applied to a namespace RQLite", table)
		}
	}
}
