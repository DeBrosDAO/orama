package backup

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// written returns every SQL statement the rig's database ran, in order.
func written(r *rig) []string {
	var out []string
	for _, b := range r.db.batches {
		for _, op := range b {
			out = append(out, op.SQL)
		}
	}
	return out
}

func hasSQL(sqls []string, prefix string) bool {
	for _, s := range sqls {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func restoreInto(t *testing.T, dst *rig) int {
	t.Helper()
	src := newRig(t, sourceRoot, testNamespace, true)
	pub, priv := ownerKey(t)
	return do(dst.h.RestoreHandler, http.MethodPost, restoreBody(t, backupFrom(t, src, pub), priv, destRoot)).Code
}

func TestScrub_dropsEveryTriggerAndTheViewsOverPlatformTables(t *testing.T) {
	dst := newRig(t, destRoot, testNamespace, true)
	dst.db.rows["sqlite_master"] = []map[string]any{
		{"type": "trigger", "name": "grant_owner", "sql": "CREATE TRIGGER grant_owner AFTER INSERT ON notes BEGIN UPDATE grants SET role='owner'; END"},
		{"type": "view", "name": "everyones keys", "sql": `CREATE VIEW "everyones keys" AS SELECT * FROM api_keys`},
		{"type": "view", "name": "mine", "sql": "CREATE VIEW mine AS SELECT v FROM notes"},
		// Tenant SQL cannot create a trigger at all, so none is the tenant's own.
		{"type": "trigger", "name": "audit_notes", "sql": "CREATE TRIGGER audit_notes AFTER INSERT ON notes BEGIN INSERT INTO notes_log(v) VALUES (new.v); END"},
	}
	if code := restoreInto(t, dst); code != http.StatusOK {
		t.Fatalf("restore answered %d", code)
	}
	sqls := written(dst)
	for _, want := range []string{`DROP TRIGGER IF EXISTS "grant_owner"`, `DROP VIEW IF EXISTS "everyones keys"`, `DROP TRIGGER IF EXISTS "audit_notes"`} {
		if !hasSQL(sqls, want) {
			t.Errorf("%s was not run: %v", want, sqls)
		}
	}
	for _, kept := range []string{`"mine"`} {
		if hasSQL(sqls, "DROP TRIGGER IF EXISTS "+kept) || hasSQL(sqls, "DROP VIEW IF EXISTS "+kept) {
			t.Errorf("the view %s over the tenant's own table was dropped", kept)
		}
	}
}

func TestScrub_quotesTheNameOfWhatItDrops(t *testing.T) {
	if got := quoteIdent(`a"; DROP TABLE notes; --`); got != `"a""; DROP TABLE notes; --"` {
		t.Fatalf("%s", got)
	}
}

func TestScrub_removesPlaintextKeysAndForeignOwnership(t *testing.T) {
	dst := newRig(t, destRoot, testNamespace, true)
	dst.db.rows["ipfs_content_ownership"] = []map[string]any{{"cid": "QmMine"}, {"cid": "QmTheirs"}, {"cid": "QmNew"}}
	// QmMine is held by this namespace too; QmTheirs only by another; QmNew by nobody.
	dst.registry.rows["ipfs_cid_refs"] = []map[string]any{{"cid": "QmMine", "own": float64(1)}, {"cid": "QmTheirs", "own": float64(0)}}
	if code := restoreInto(t, dst); code != http.StatusOK {
		t.Fatalf("restore answered %d", code)
	}
	sqls := written(dst)
	if !hasSQL(sqls, "DELETE FROM api_keys WHERE key LIKE 'ak_%'") {
		t.Errorf("plaintext keys were not removed: %v", sqls)
	}
	if !hasSQL(sqls, "DELETE FROM ipfs_content_ownership WHERE namespace <> ?") {
		t.Errorf("other namespaces' ownership rows were kept: %v", sqls)
	}
	var dropped []string
	for _, b := range dst.db.batches {
		for _, op := range b {
			if strings.HasPrefix(op.SQL, "DELETE FROM ipfs_content_ownership WHERE cid = ?") {
				dropped = append(dropped, op.Args[0].(string))
			}
		}
	}
	if len(dropped) != 1 || dropped[0] != "QmTheirs" {
		t.Fatalf("dropped %v, want only the CID another namespace alone holds", dropped)
	}
}

func TestScrub_aFailedScrubIsReportedAndRerunnable(t *testing.T) {
	dst := newRig(t, destRoot, testNamespace, true)
	dst.registry.queryErr = errors.New("registry down")
	dst.db.rows["ipfs_content_ownership"] = []map[string]any{{"cid": "QmX"}}
	if code := restoreInto(t, dst); code != http.StatusBadGateway {
		t.Fatalf("a restore whose scrub failed answered %d, want 502", code)
	}
}

func TestScrub_aDatabaseWithoutAPIKeysIsClean(t *testing.T) {
	dst := newRig(t, destRoot, testNamespace, true)
	h := dst.h
	h.cfg.DB = noKeysTable{dst.db}
	if err := h.dropPlaintextKeys(t.Context()); err != nil {
		t.Fatalf("a namespace database without api_keys holds none: %v", err)
	}
}

// noKeysTable answers a batch as RQLite does for a table that does not exist.
type noKeysTable struct{ *fakeDB }

func (noKeysTable) Batch(_ context.Context, ops []rqlite.BatchOp) (*rqlite.BatchResult, error) {
	return &rqlite.BatchResult{Committed: false, FailedIndex: 0, Error: "no such table: api_keys"}, nil
}

func TestGuardLoad_putsTheQuotaBackAndScrubsAnImport(t *testing.T) {
	dst := newRig(t, destRoot, testNamespace, true)
	dst.db.rows["namespace_quotas"] = []map[string]any{{"max_storage_bytes": float64(1000)}}
	finish, err := dst.h.GuardLoad(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(t.Context()); err != nil {
		t.Fatal(err)
	}
	sqls := written(dst)
	if !hasSQL(sqls, "INSERT INTO namespace_quotas") || !hasSQL(sqls, "DELETE FROM api_keys") {
		t.Fatalf("an import's finish ran %v", sqls)
	}
}
