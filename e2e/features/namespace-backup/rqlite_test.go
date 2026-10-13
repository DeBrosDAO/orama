//go:build e2e_fleet

package namespacebackup

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	pathExport = "/v1/rqlite/export"
	pathImport = "/v1/rqlite/import"
	// sqliteMagic starts every SQLite database file.
	sqliteMagic = "SQLite format 3\x00"
)

func export(t testing.TB, n *ns.Namespace, who tenancy.Cred) *gw.Response {
	t.Helper()
	return tenancy.Get(t, n.Client, pathExport, who)
}

func importDB(t testing.TB, n *ns.Namespace, who tenancy.Cred, ctype string, db []byte) *gw.Response {
	t.Helper()
	return n.Client.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathImport, Bearer: who.Bearer, APIKey: who.APIKey,
		Header: http.Header{"Content-Type": {ctype}}, Body: db})
}

// TestRQLiteExport_importRoundTrip: the export is a consistent SQLite file of
// the namespace database; importing it replaces the database with it
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama namespace rqlite export|import").
func TestRQLiteExport_importRoundTrip(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	tenancy.NotesSeed(t, n, "one", "two")
	db := export(t, n, tenancy.Owner(n)).Expect(t, http.StatusOK).Body
	if !bytes.HasPrefix(db, []byte(sqliteMagic)) {
		t.Fatalf("the export is not a SQLite file (%d bytes)", len(db))
	}
	tenancy.NotesInsert(t, n, "after-export")
	importDB(t, n, tenancy.Owner(n), "application/octet-stream", db).Expect(t, http.StatusOK)
	if got := tenancy.NotesRows(t, n); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("after import the rows are %v", got)
	}
}

// TestRQLiteImport_refusalsChangeNothing: the wrong content type, a body that
// is not a database, and a caller without the owner's control plane are
// refused, and the database is untouched.
func TestRQLiteImport_refusalsChangeNothing(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	tenancy.NotesSeed(t, n, "keep")
	db := export(t, n, tenancy.Owner(n)).Expect(t, http.StatusOK).Body
	importDB(t, n, tenancy.Owner(n), "application/json", db).Expect(t, http.StatusBadRequest)
	if r := importDB(t, n, tenancy.Owner(n), "application/octet-stream", []byte("not a database")); r.Status < 400 {
		t.Errorf("importing garbage answered %d", r.Status)
	}
	runtime := tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleRuntime).Token()}
	for _, r := range []*gw.Response{export(t, n, runtime), importDB(t, n, runtime, "application/octet-stream", db), export(t, n, tenancy.Cred{})} {
		if r.Status != http.StatusUnauthorized && r.Status != http.StatusForbidden {
			t.Errorf("an export/import without the grant answered %d", r.Status)
		}
	}
	tenancy.Send(t, n.Client, http.MethodPost, pathExport, tenancy.Owner(n), nil).Expect(t, http.StatusMethodNotAllowed)
	if got := tenancy.NotesRows(t, n); !slices.Equal(got, []string{"keep"}) {
		t.Fatalf("refused imports changed the database to %v", got)
	}
}

// TestRQLiteCLI_exportWritesAFileImportNeedsConfirmation: `orama namespace
// rqlite export` writes the database; `import` refuses to replace it unless
// the operator types the namespace name.
func TestRQLiteCLI_exportWritesAFileImportNeedsConfirmation(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{Via: ns.ViaOperator})
	out := filepath.Join(t.TempDir(), "ns.db")
	n.CLI.MustOK(t, "namespace", "rqlite", "export", "-o", out)
	db, err := os.ReadFile(out)
	if err != nil || !bytes.HasPrefix(db, []byte(sqliteMagic)) {
		t.Fatalf("the exported file is not a SQLite database (%d bytes, %v)", len(db), err)
	}
	res, err := n.CLI.Run(t.Context(), "namespace", "rqlite", "import", "-i", out)
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit == 0 || !strings.Contains(res.Stderr+res.Stdout, "aborted") {
		t.Fatalf("an unconfirmed import did not abort: exit %d %s", res.Exit, res.Stdout)
	}
	for _, args := range [][]string{{"import", "-i", filepath.Join(t.TempDir(), "none")}, {"import", "-i", t.TempDir()}} {
		if r, err := n.CLI.Run(t.Context(), append([]string{"namespace", "rqlite"}, args...)...); err != nil || r.Exit == 0 {
			t.Errorf("rqlite %v succeeded (%v)", args, err)
		}
	}
	tenancy.Get(t, n.Client, "/health", tenancy.Cred{}).Expect(t, http.StatusOK)
}
