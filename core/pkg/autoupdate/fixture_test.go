package autoupdate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/updatepolicy"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// A three-node cluster: n1 is the leader, so the plan is n2, n3, n1.
var testNodes = []struct{ id, host, role string }{
	{"n1", "10.0.0.1", "nameserver-ns1"},
	{"n2", "10.0.0.2", "node"},
	{"n3", "10.0.0.3", "node"},
}

// newClusterDB is a registry with the real schema and the three nodes live.
func newClusterDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	for _, n := range testNodes {
		_, err := db.Exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status, role, last_seen)
		                   VALUES (?, ?, ?, 'active', ?, datetime('now'))`, n.id, "203.0.113."+n.id[1:], n.host, n.role)
		if err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func setSetting(t *testing.T, db *sql.DB, key, value string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO cluster_settings (key, value, updated_by) VALUES (?, ?, 'test')
	                   ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		t.Fatal(err)
	}
}

// release is a published channel: one archive on stable, under a root this
// test controls.
type release struct {
	keys    releaserepo.Keys
	dir     string
	url     string
	rootOut string
	archive []byte
}

const (
	testVersion = "0.3.1"
	testArch    = "amd64"
)

func newRelease(t *testing.T) *release {
	t.Helper()
	keys, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	root, err := releaserepo.NewRoot(keys, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	r := &release{keys: keys, dir: t.TempDir(), archive: []byte("release archive bytes"), rootOut: filepath.Join(t.TempDir(), "release-root.json")}
	if err := os.WriteFile(r.rootOut, root, 0o644); err != nil {
		t.Fatal(err)
	}
	releaseverify.AllowLocalRepositories(t)
	srv := httptest.NewServer(http.FileServer(http.Dir(r.dir)))
	t.Cleanup(srv.Close)
	r.url = srv.URL
	r.publish(t, 5, time.Time{}, testVersion, r.archive)
	return r
}

// publish writes the metadata at snapshot version, naming archive as
// version's, and serves served as its bytes.
func (r *release) publish(t *testing.T, snapshot int64, timestampExpires time.Time, version string, served []byte) {
	t.Helper()
	files, err := releaserepo.Build(r.keys, releaserepo.Spec{
		Version: snapshot, RootValidUntil: time.Now().Add(24 * time.Hour), TimestampExpires: timestampExpires,
		Targets: map[string][]byte{releaseverify.ArchiveTarget("stable", version, testArch): r.archive},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(r.dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(r.dir, "targets", "stable", fmt.Sprintf("orama-%s-linux-%s.tar.gz", version, testArch))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, served, 0o644); err != nil {
		t.Fatal(err)
	}
}

// testStore is the store over db. Every call opens a handle, as production
// does; opens counts them.
func testStore(db *sql.DB) SQLStore {
	return SQLStore{Open: func(context.Context) (*sql.DB, func(), error) { return db, func() {}, nil }}
}

// memJournal is the journal in memory.
type memJournal struct {
	intent    *Intent
	beginErr  error
	clearErrs int
}

func (j *memJournal) Begin(i Intent) error {
	if j.beginErr != nil {
		return j.beginErr
	}
	if j.intent != nil {
		return fmt.Errorf("an install is already under way")
	}
	j.intent = &i
	return nil
}
func (j *memJournal) Replace(i Intent) error {
	j.intent = &i
	return nil
}
func (j *memJournal) Pending() (*Intent, error) { return j.intent, nil }
func (j *memJournal) Clear() error {
	j.intent = nil
	j.clearErrs++
	return nil
}

// memRetries is the retry record in memory.
type memRetries struct{ retry *Retry }

func (r *memRetries) Current() (*Retry, error) { return r.retry, nil }
func (r *memRetries) Set(v Retry) error        { r.retry = &v; return nil }
func (r *memRetries) Clear() error             { r.retry = nil; return nil }

// fakeNode records what the agent asks of the machine.
type fakeNode struct {
	current  string
	calls    []string
	upgrades int
	// healthyAfter makes Healthy fail until Restore has been called, as a
	// release that does not come up does.
	badRelease bool
	restored   bool
	stageErr   error
	upgradeErr error
	restoreErr error
	previous   string
	// recoverTo is what Recover makes of a half-swapped tree: while unreadable
	// is set, Current is "" until Recover has run.
	unreadable bool
	recoverErr error
	// stagedThenFailed makes Stage swap the release in and then fail, as a
	// step after the swap does.
	stagedThenFailed bool
	// stageBreaksTree makes a failed Stage leave the tree unreadable, as a swap
	// killed half-way, whose own undo also failed, does.
	stageBreaksTree bool
	// restoreKilled makes Restore put the release back and then report a kill,
	// as a run killed between the two steps of a rollback.
	restoreKilled bool
	// restoreLeaves, when set, is what the tree holds after Restore instead of
	// the kept release: a restore that put back another release, or that was
	// killed half-way and left a tree whose manifest cannot be read ("").
	restoreLeaves *string
	// restoredFor is the version Restore was asked to put back.
	restoredFor string
}

func (n *fakeNode) Current() string {
	if n.unreadable {
		return ""
	}
	return n.current
}

func (n *fakeNode) Recover(context.Context) error {
	n.calls = append(n.calls, "recover")
	if n.recoverErr == nil {
		n.unreadable = false
	}
	return n.recoverErr
}

func (n *fakeNode) Stage(_ context.Context, rel Release) error {
	n.calls = append(n.calls, "stage")
	if n.stageErr == nil || n.stagedThenFailed {
		n.previous, n.current = n.current, rel.Version
	}
	if n.stageErr != nil && n.stageBreaksTree {
		n.unreadable = true
	}
	return n.stageErr
}

func (n *fakeNode) Upgrade(context.Context) error {
	n.calls = append(n.calls, "upgrade")
	n.upgrades++
	if n.upgradeErr != nil && !n.restored {
		return n.upgradeErr
	}
	return nil
}

func (n *fakeNode) Restore(_ context.Context, version string) error {
	n.calls = append(n.calls, "restore")
	n.restoredFor = version
	if n.previous != version && n.restoreLeaves == nil {
		return fmt.Errorf("the kept release is %s, not %s", n.previous, version)
	}
	n.restored = true
	n.current = n.previous
	if n.restoreLeaves != nil {
		n.current = *n.restoreLeaves
	}
	if n.restoreKilled {
		return errors.New("killed")
	}
	return n.restoreErr
}

func (n *fakeNode) Healthy(context.Context) error {
	n.calls = append(n.calls, "healthy")
	if n.badRelease && !n.restored {
		return fmt.Errorf("the node did not rejoin the cluster")
	}
	return nil
}

type fakeRaft struct{ view RaftView }

func (f fakeRaft) View(context.Context) (RaftView, error) { return f.view, nil }

// harness is one node's agent over a shared registry.
type harness struct {
	db     *sql.DB
	rel    *release
	node   *fakeNode
	jrnl   *memJournal
	retry  *memRetries
	agent  *Agent
	notice string
	logs   []string
}

// newHarness is the agent on host (one of testNodes), running 0.3.0, over db.
func newHarness(t *testing.T, db *sql.DB, rel *release, host string) *harness {
	t.Helper()
	h := &harness{db: db, rel: rel, node: &fakeNode{current: "0.3.0"}, jrnl: &memJournal{}, retry: &memRetries{}, notice: filepath.Join(t.TempDir(), "notice.json")}
	h.agent = &Agent{
		Store: testStore(db), Journal: h.jrnl, Retries: h.retry,
		Raft: fakeRaft{view: RaftView{LeaderHost: "10.0.0.1", Voters: 3, HealthyVoters: 3}},
		Source: Source{
			RootPath: rel.rootOut, SeenPath: filepath.Join(t.TempDir(), "release-seen.json"),
			WorkDir: t.TempDir(), Arch: testArch, Now: time.Now,
		},
		Node: h.node, Role: RoleCluster, NodeHost: host, NoticePath: h.notice, Now: time.Now,
		Logf: func(format string, args ...any) { h.logs = append(h.logs, fmt.Sprintf(format, args...)) },
	}
	setSetting(t, db, updatepolicy.KeyRepo, rel.url)
	return h
}

func (h *harness) run(t *testing.T) (Outcome, error) {
	t.Helper()
	return h.agent.Run(t.Context())
}

func installState(t *testing.T, db *sql.DB, version, node string) string {
	t.Helper()
	var state string
	err := db.QueryRow(`SELECT state FROM release_installs WHERE version = ? AND node_id = ?`, version, node).Scan(&state)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return state
}
