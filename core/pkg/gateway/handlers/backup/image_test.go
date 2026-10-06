package backup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/nsbackup"
)

func checkBytes(t *testing.T, r *rig, image []byte) error {
	t.Helper()
	return r.h.checkImageBytes(t.Context(), image)
}

func TestCheckImage_acceptsACleanImage(t *testing.T) {
	r := newRig(t, destRoot, testNamespace, true)
	img := mustImage(
		"CREATE TABLE notes (v TEXT)", "CREATE VIEW mine AS SELECT v FROM notes",
		"CREATE TABLE ipfs_content_ownership (cid TEXT, namespace TEXT)",
		"INSERT INTO ipfs_content_ownership VALUES ('QmMine', '"+testNamespace+"')")
	r.registry.rows["ipfs_cid_refs"] = []map[string]any{{"cid": "QmMine", "own": float64(1)}}
	if err := checkBytes(t, r, img); err != nil {
		t.Fatal(err)
	}
}

// Real older backups carry these. Nothing reads them: a namespace gateway
// validates keys against the registry, and every read of the ownership table
// filters on its own namespace. The detached scrub removes them.
func TestCheckImage_acceptsThePassiveRowsTheScrubRemoves(t *testing.T) {
	r := newRig(t, destRoot, testNamespace, true)
	img := mustImage(
		"CREATE TABLE api_keys (key TEXT)", "INSERT INTO api_keys VALUES ('orama_legacyplaintext')",
		"CREATE TABLE ipfs_content_ownership (cid TEXT, namespace TEXT)",
		"INSERT INTO ipfs_content_ownership VALUES ('QmOther', 'other')")
	if err := checkBytes(t, r, img); err != nil {
		t.Fatalf("an older backup was refused: %v", err)
	}
	src := newRig(t, sourceRoot, testNamespace, true)
	pub, priv := ownerKey(t)
	src.snap.db = img
	dst := newRig(t, destRoot, testNamespace, true)
	rec := do(dst.h.RestoreHandler, http.MethodPost, restoreBody(t, backupFrom(t, src, pub), priv, destRoot))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	sqls := written(dst)
	if !hasSQL(sqls, "DELETE FROM api_keys") || !hasSQL(sqls, "DELETE FROM ipfs_content_ownership WHERE namespace <> ?") {
		t.Fatalf("the scrub did not remove them: %v", sqls)
	}
}

func TestCheckImage_refusesWhatIsActiveOrGrantsAccess(t *testing.T) {
	for name, tc := range map[string]struct {
		stmts []string
		held  []map[string]any
		want  string
	}{
		"a trigger": {stmts: []string{"CREATE TABLE notes (v TEXT)", "CREATE TABLE log (v TEXT)",
			"CREATE TRIGGER t AFTER INSERT ON notes BEGIN INSERT INTO log VALUES (new.v); END"}, want: "trigger (t)"},
		"a view over a platform table": {stmts: []string{"CREATE TABLE grants (role TEXT)", "CREATE VIEW v AS SELECT * FROM grants"}, want: "view (v)"},
		"a CID only another namespace holds (a row that grants access)": {stmts: []string{"CREATE TABLE ipfs_content_ownership (cid TEXT, namespace TEXT)",
			"INSERT INTO ipfs_content_ownership VALUES ('QmTheirs', '" + testNamespace + "')"},
			held: []map[string]any{{"cid": "QmTheirs", "own": float64(0)}}, want: "QmTheirs"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, destRoot, testNamespace, true)
			if tc.held != nil {
				r.registry.rows["ipfs_cid_refs"] = tc.held
			}
			err := checkBytes(t, r, mustImage(tc.stmts...))
			if !errors.Is(err, ErrImageRefused) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want a refusal naming %q", err, tc.want)
			}
		})
	}
}

func TestCheckImage_refusesADamagedOrForeignFile(t *testing.T) {
	good := mustImage("CREATE TABLE notes (v TEXT)", "INSERT INTO notes VALUES (hex(zeroblob(5000)))", "CREATE INDEX i ON notes(v)")
	damaged := append([]byte(nil), good...)
	for i := len(damaged) / 2; i < len(damaged); i++ {
		damaged[i] ^= 0xff
	}
	for name, img := range map[string][]byte{
		"garbage after the magic": append([]byte(nsbackup.SQLiteMagic), []byte("not pages")...),
		"damaged pages":           damaged,
		"a SQL dump":              []byte("BEGIN; UPDATE grants SET role='owner'; COMMIT;"),
		"empty":                   nil,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, destRoot, testNamespace, true)
			if err := checkBytes(t, r, img); !errors.Is(err, ErrImageRefused) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestCheckImage_aRegistryThatCannotAnswerIsNotARefusal(t *testing.T) {
	r := newRig(t, destRoot, testNamespace, true)
	r.registry.queryErr = errors.New("down")
	img := mustImage("CREATE TABLE ipfs_content_ownership (cid TEXT, namespace TEXT)",
		"INSERT INTO ipfs_content_ownership VALUES ('QmX', '"+testNamespace+"')")
	err := checkBytes(t, r, img)
	if err == nil || errors.Is(err, ErrImageRefused) {
		t.Fatalf("got %v: an outage is not the caller's fault", err)
	}
}

func TestSpool_leavesNoFileBehindAnError(t *testing.T) {
	before, _ := os.ReadDir(os.TempDir())
	_, _, _, err := Spool(errReader{})
	if err == nil {
		t.Fatal("a failing reader was spooled")
	}
	after, _ := os.ReadDir(os.TempDir())
	if len(after) > len(before) {
		t.Fatalf("a spool file was left behind: %d entries before, %d after", len(before), len(after))
	}
	path, size, cleanup, err := Spool(strings.NewReader("abc"))
	if err != nil || size != 3 {
		t.Fatal(size, err)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cleanup left %s", path)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("client went away") }

func TestRestoreHandler_refusesACraftedImageBeforeLoadingIt(t *testing.T) {
	src := newRig(t, sourceRoot, testNamespace, true)
	pub, priv := ownerKey(t)
	crafted := mustImage("CREATE TABLE notes (v TEXT)", "CREATE TABLE log (v TEXT)",
		"CREATE TRIGGER t AFTER INSERT ON notes BEGIN INSERT INTO log VALUES (new.v); END")
	src.snap.db = crafted
	body := restoreBody(t, backupFrom(t, src, pub), priv, destRoot)
	dst := newRig(t, destRoot, testNamespace, true)
	rec := do(dst.h.RestoreHandler, http.MethodPost, body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "trigger") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertNothingWritten(t, dst)
}

// A client that goes away after the load began must not stop the scrub, and a
// load that failed may still have been applied, so the scrub runs after it too.
func TestRestoreHandler_scrubsEvenWhenTheClientWentAwayOrTheLoadFailed(t *testing.T) {
	for name, tc := range map[string]struct {
		cancelled bool
		loadErr   error
	}{"client went away": {cancelled: true}, "load failed": {loadErr: errBoom}} {
		t.Run(name, func(t *testing.T) {
			src := newRig(t, sourceRoot, testNamespace, true)
			pub, priv := ownerKey(t)
			body := restoreBody(t, backupFrom(t, src, pub), priv, destRoot)
			dst := newRig(t, destRoot, testNamespace, true)
			dst.snap.loadErr = tc.loadErr
			ctx, cancel := context.WithCancel(context.Background())
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))).WithContext(ctx)
			if tc.cancelled {
				dst.snap.onLoad = cancel // the client goes away once the load began
			}
			defer cancel()
			rec := httptest.NewRecorder()
			dst.h.RestoreHandler(rec, r)
			if tc.cancelled && (rec.Code != http.StatusOK || dst.snap.loadCtxErr != nil) {
				t.Fatalf("%d %s, load context error %v", rec.Code, rec.Body, dst.snap.loadCtxErr)
			}
			if tc.loadErr != nil && rec.Code != http.StatusBadGateway {
				t.Fatalf("%d", rec.Code)
			}
			if !hasSQL(written(dst), "DELETE FROM api_keys") {
				t.Fatalf("no scrub ran: %v", written(dst))
			}
		})
	}
}

// SQLite resolves table names case-insensitively, so an image that stores the
// table as IPFS_Content_Ownership is read by the platform as its own.
func TestCheckImage_aMixedCaseTableIsCheckedLikeAnyOther(t *testing.T) {
	r := newRig(t, destRoot, testNamespace, true)
	r.registry.rows["ipfs_cid_refs"] = []map[string]any{{"cid": "QmTheirs", "own": float64(0)}}
	img := mustImage(`CREATE TABLE "IPFS_Content_Ownership" (cid TEXT, namespace TEXT)`,
		"INSERT INTO ipfs_content_ownership VALUES ('QmTheirs', '"+testNamespace+"')")
	if err := checkBytes(t, r, img); !errors.Is(err, ErrImageRefused) || !strings.Contains(err.Error(), "QmTheirs") {
		t.Fatalf("got %v, want a refusal naming the foreign CID", err)
	}
}

func TestCheckImage_aViewOverAMixedCasePlatformTableIsRefused(t *testing.T) {
	r := newRig(t, destRoot, testNamespace, true)
	img := mustImage("CREATE TABLE Grants (role TEXT)", "CREATE VIEW v AS SELECT * FROM GRANTS")
	if err := checkBytes(t, r, img); !errors.Is(err, ErrImageRefused) {
		t.Fatalf("got %v", err)
	}
}

// An ownership table without the columns the gateway reads it by is not one it
// could serve: storage would fail on "no such column" after the load.
func TestCheckImage_anOwnershipTableWithoutItsColumnsIsRefusedClearly(t *testing.T) {
	for name, ddl := range map[string]string{
		"no namespace": "CREATE TABLE ipfs_content_ownership (cid TEXT)",
		"no cid":       "CREATE TABLE ipfs_content_ownership (namespace TEXT)",
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, destRoot, testNamespace, true)
			err := checkBytes(t, r, mustImage(ddl))
			if !errors.Is(err, ErrImageRefused) || !strings.Contains(err.Error(), "no \"") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestCheckImage_aMixedCaseColumnNameStillCounts(t *testing.T) {
	r := newRig(t, destRoot, testNamespace, true)
	if err := checkBytes(t, r, mustImage("CREATE TABLE ipfs_content_ownership (CID TEXT, Namespace TEXT)")); err != nil {
		t.Fatal(err)
	}
}

// The budget a restore started with may be spent by the time it has an answer.
func TestRestoreHandler_theResponseGetsItsOwnDeadline(t *testing.T) {
	old := transferBudget
	transferBudget = 200 * time.Millisecond
	t.Cleanup(func() { transferBudget = old })

	src := newRig(t, sourceRoot, testNamespace, true)
	pub, priv := ownerKey(t)
	body := restoreBody(t, backupFrom(t, src, pub), priv, destRoot)
	dst := newRig(t, destRoot, testNamespace, true)
	dst.snap.onLoad = func() { time.Sleep(500 * time.Millisecond) } // longer than the budget
	srv := httptest.NewUnstartedServer(http.HandlerFunc(dst.h.RestoreHandler))
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()
	resp, err := http.Post(srv.URL, "application/octet-stream", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("the client never saw the answer of a restore that succeeded: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d", resp.StatusCode)
	}
}
