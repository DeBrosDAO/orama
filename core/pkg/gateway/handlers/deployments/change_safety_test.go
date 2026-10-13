package deployments

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// afterQueryDB runs hook once, right after the first read of the deployments
// table, as another gateway writing between two reads would.
type afterQueryDB struct {
	rqlite.Client
	once sync.Once
	hook func()
}

func (d *afterQueryDB) Query(ctx context.Context, dest any, query string, args ...any) error {
	err := d.Client.Query(ctx, dest, query, args...)
	if strings.Contains(query, "FROM deployments WHERE namespace") {
		d.once.Do(d.hook)
	}
	return err
}

// The bug: the deployment was read, then the stored environment, as the
// compare-and-swap token. A change landing between the two reads was in the
// token but not in the environment the new one was built from, so the swap
// passed and the earlier change was overwritten without a word.
func TestApplyChange_aChangeLandingAfterTheReadIsRefusedNotOverwritten(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	ctx := context.Background()
	if _, err := svc.db.Exec(ctx, `UPDATE deployments SET type = 'static' WHERE id = ?`, envTestDeploy); err != nil {
		t.Fatal(err)
	}
	inner := svc.db
	svc.db = &afterQueryDB{Client: inner, hook: func() {
		if _, err := inner.Exec(ctx, `UPDATE deployments SET environment = 'written-by-another-gateway' WHERE id = ?`, envTestDeploy); err != nil {
			t.Error(err)
		}
	}}
	h := &EnvHandler{service: svc, logger: zap.NewNop()}

	_, status, err := h.applyChange(ctx, "acme", envTestAppName, map[string]string{"A": "1"}, nil)

	if status != http.StatusConflict || err == nil {
		t.Fatalf("status %d, err %v; the change made against a stale read must be refused with 409", status, err)
	}
	var stored string
	var rows []struct {
		Environment string `db:"environment"`
	}
	if err := inner.Query(ctx, &rows, `SELECT environment FROM deployments WHERE id = ?`, envTestDeploy); err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	if stored = rows[0].Environment; stored != "written-by-another-gateway" {
		t.Errorf("the other writer's environment was overwritten: %q", stored)
	}
}

func TestLockDeployment_dropsItsEntryWhenNobodyHoldsOrWaits(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	for i := 0; i < 50; i++ {
		unlock, err := svc.lockDeployment(context.Background(), "acme", "app"+string(rune('a'+i%26)))
		if err != nil {
			t.Fatal(err)
		}
		unlock()
	}
	svc.deploymentLockMu.Lock()
	left := len(svc.deploymentLocks)
	svc.deploymentLockMu.Unlock()
	if left != 0 {
		t.Fatalf("%d lock entries left after every lock was released", left)
	}
}

func TestLockDeployment_aWaiterThatGivesUpLeavesNoEntryAndDoesNotBreakTheHolder(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	unlock, err := svc.lockDeployment(context.Background(), "acme", "api")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := svc.lockDeployment(ctx, "acme", "api"); err == nil {
		t.Fatal("a second holder got the lock")
	}
	// A third caller must still wait for the holder, not get a fresh entry.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if _, err := svc.lockDeployment(ctx2, "acme", "api"); err == nil {
		t.Fatal("the entry was dropped while the first holder still held it")
	}
	unlock()
	svc.deploymentLockMu.Lock()
	left := len(svc.deploymentLocks)
	svc.deploymentLockMu.Unlock()
	if left != 0 {
		t.Fatalf("%d entries left", left)
	}
}

// A setup or teardown restarting or deleting a unit under an update that is
// restarting the same one leaves the deployment in a state neither meant.
func TestReplicaSetupAndTeardown_waitForTheDeploymentsLock(t *testing.T) {
	type route struct {
		path string
		call func(*ReplicaHandler, http.ResponseWriter, *http.Request)
	}
	for name, rt := range map[string]route{
		"setup":    {replicaSetupPath, (*ReplicaHandler).HandleSetup},
		"teardown": {replicaTeardownPath, (*ReplicaHandler).HandleTeardown},
	} {
		call := rt.call
		t.Run(name, func(t *testing.T) {
			svc, _ := replicaEnvService(t, nil)
			h := NewReplicaHandler(svc, nil, nil, zap.NewNop(), envReplicaBase(t))
			unlock, err := svc.lockDeployment(context.Background(), "acme", envTestAppName)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()

			raw := `{"deployment_id":"` + envTestDeploy + `","namespace":"acme","name":"` + envTestAppName + `","type":"go-backend"}`
			req := httptest.NewRequest(http.MethodPost, rt.path, strings.NewReader(raw))
			req.RemoteAddr = "10.0.0.2:4000"
			if err := svc.signReplicaRequest(req, envTestHome); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(req.Context(), 50*time.Millisecond)
			defer cancel()
			w := httptest.NewRecorder()
			call(h, w, req.WithContext(ctx))

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status %d, want 503 while a change holds the lock: %s", w.Code, w.Body)
			}
		})
	}
}

const (
	replicaSetupPath    = "/v1/internal/deployments/replica/setup"
	replicaTeardownPath = "/v1/internal/deployments/replica/teardown"
)

func TestRecordAppliedVersion_usesAUniqueTempFileAndLeavesNoneBehind(t *testing.T) {
	dir := t.TempDir()
	deployPath := filepath.Join(dir, "app")
	// A stale file under the old fixed temp name must not be the one written.
	stale := appliedVersionPath(deployPath, envVersion) + ".tmp"
	if err := os.WriteFile(stale, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := int64(1); i <= 20; i++ {
		wg.Add(1)
		go func(v int64) {
			defer wg.Done()
			if err := recordAppliedVersion(deployPath, envVersion, v); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	if data, _ := os.ReadFile(stale); string(data) != "junk" {
		t.Errorf("the fixed temp name was written: %q", data)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 { // the record and the untouched stale file
		t.Errorf("entries %v, want the record and the stale file only", entries)
	}
	if v, err := readAppliedVersion(deployPath, envVersion); err != nil || v < 1 || v > 20 {
		t.Errorf("version %d, %v", v, err)
	}
}

// ---- delete, update ordering, env segments, versions ----------------------

func namespaced(r *http.Request, ns string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxkeys.NamespaceOverride, ns))
}

// Delete removes the unit an update is restarting, so it waits its turn.
func TestHandleDelete_waitsForTheDeploymentsLock(t *testing.T) {
	svc := registryWith(t, [2]string{"acme", "api"})
	h := NewListHandler(svc, nil, nil, zap.NewNop(), "")
	unlock, err := svc.lockDeployment(context.Background(), "acme", "api")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	w := httptest.NewRecorder()
	h.HandleDelete(w, namespaced(httptest.NewRequest(http.MethodDelete, "/v1/deployments/delete?name=api", nil).WithContext(ctx), "acme"))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 while a change holds the lock: %s", w.Code, w.Body)
	}
	if got := deploymentRows(t, svc, "api"); got != 1 {
		t.Errorf("the deployment was deleted under the lock")
	}
}

func TestHandleDelete_forgetsTheEnvVersionAndDeletesByID(t *testing.T) {
	svc := registryWith(t, [2]string{"acme", "api"})
	svc.nextEnvVersion("acme", "api", time.Time{})
	pm := process.NewManager(zap.NewNop(), process.Config{Systemctl: func(...string) error { return nil }})
	h := NewListHandler(svc, pm, nil, zap.NewNop(), "")

	w := httptest.NewRecorder()
	h.HandleDelete(w, namespaced(httptest.NewRequest(http.MethodDelete, "/v1/deployments/delete?id=a", nil), "acme"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	svc.envVersionMu.Lock()
	left := len(svc.envVersions)
	svc.envVersionMu.Unlock()
	if left != 0 || deploymentRows(t, svc, "api") != 0 {
		t.Errorf("%d env versions remembered, %d rows left", left, deploymentRows(t, svc, "api"))
	}
}

func deploymentRows(t *testing.T, svc *DeploymentService, name string) int {
	t.Helper()
	var rows []struct {
		ID string `db:"id"`
	}
	if err := svc.db.Query(context.Background(), &rows, `SELECT id FROM deployments WHERE name = ?`, name); err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

// deadlineRecorder is a ResponseWriter whose deadlines can be moved, recording
// that they were.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	writeDeadlines []time.Time
}

func (d *deadlineRecorder) SetReadDeadline(time.Time) error { return nil }
func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.writeDeadlines = append(d.writeDeadlines, t)
	return nil
}

// The deadlines are moved before the lock is waited for: a request refused at
// the lock must already have had them moved, or the upload and the wait ran
// under the server's own.
func TestUpdateAndRollback_moveTheirDeadlinesBeforeTheLock(t *testing.T) {
	for name, call := range map[string]func(*deadlineRecorder, *DeploymentService, context.Context){
		"update": func(w *deadlineRecorder, svc *DeploymentService, ctx context.Context) {
			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			_ = mw.WriteField("name", "api")
			_ = mw.Close()
			r := httptest.NewRequest(http.MethodPost, "/v1/deployments/go/update?name=api", &body).WithContext(ctx)
			r.Header.Set("Content-Type", mw.FormDataContentType())
			(&UpdateHandler{service: svc, logger: zap.NewNop()}).HandleUpdate(w, namespaced(r, "acme"))
		},
		"rollback": func(w *deadlineRecorder, svc *DeploymentService, ctx context.Context) {
			r := httptest.NewRequest(http.MethodPost, "/v1/deployments/rollback",
				strings.NewReader(`{"name":"api","version":1}`)).WithContext(ctx)
			(&RollbackHandler{service: svc, logger: zap.NewNop()}).HandleRollback(w, namespaced(r, "acme"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc := registryWith(t, [2]string{"acme", "api"})
			unlock, err := svc.lockDeployment(context.Background(), "acme", "api")
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}

			call(w, svc, ctx)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status %d, want 503 at the lock: %s", w.Code, w.Body)
			}
			if len(w.writeDeadlines) == 0 || time.Until(w.writeDeadlines[0]) < constants.DeploymentUpdateBudget {
				t.Errorf("write deadlines %v: not moved out to cover the whole change before the lock", w.writeDeadlines)
			}
		})
	}
}

type slowReconfigurer struct{ d time.Duration }

func (s slowReconfigurer) Reconfigure(context.Context, *deployments.Deployment, string) error {
	time.Sleep(s.d)
	return nil
}

// The bug: the replicas ran under what the local restart left of one budget, so
// a slow restart was reported as the replicas timing out.
func TestRestartEverywhere_aSlowLocalRestartDoesNotStarveTheReplicas(t *testing.T) {
	svc, calls := replicaEnvService(t, nil, envTestNodeB)
	var sawDeadline bool
	inner := svc.callReplica
	svc.callReplica = func(ctx context.Context, nodeID, ip, path string, p map[string]interface{}) (map[string]interface{}, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sawDeadline = true
		return inner(ctx, nodeID, ip, path, p)
	}
	h := &EnvHandler{service: svc, processManager: slowReconfigurer{d: 80 * time.Millisecond}, logger: zap.NewNop(), baseDeployPath: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond) // the local budget, already spent
	defer cancel()

	replicas, restarted, status, err := h.restartEverywhere(ctx, envTestDeployment(), 5)

	if err != nil || status != http.StatusOK || !restarted || replicas != 1 || !sawDeadline || len(calls.nodes) != 1 {
		t.Fatalf("replicas %d, restarted %v, status %d, err %v: the replicas were starved by the local step", replicas, restarted, status, err)
	}
}

// The bug: after a restart with the clock behind, the version fell below the one
// the replicas had applied and every change was refused (409) for good.
func TestNextEnvVersion_startsAboveTheRecordedOneAfterARestartWithTheClockBehind(t *testing.T) {
	svc := &DeploymentService{}
	recorded := time.Now().Add(time.Hour) // the previous process's clock ran ahead
	got := svc.nextEnvVersion("acme", "api", recorded)
	if got <= recorded.UnixNano() {
		t.Fatalf("version %d does not exceed the recorded %d", got, recorded.UnixNano())
	}
	if next := svc.nextEnvVersion("acme", "api", recorded); next <= got {
		t.Errorf("%d not above %d", next, got)
	}
}

func TestApplyChange_recordsItsVersionAsTheRowsUpdatedAt(t *testing.T) {
	svc, _ := replicaEnvService(t, nil)
	ctx := context.Background()
	if _, err := svc.db.Exec(ctx, `UPDATE deployments SET type = 'static' WHERE id = ?`, envTestDeploy); err != nil {
		t.Fatal(err)
	}
	h := &EnvHandler{service: svc, logger: zap.NewNop()}
	if _, status, err := h.applyChange(ctx, "acme", envTestAppName, map[string]string{"A": "1"}, nil); err != nil {
		t.Fatalf("status %d: %v", status, err)
	}
	d, err := svc.GetDeployment(ctx, "acme", envTestAppName)
	if err != nil {
		t.Fatal(err)
	}
	svc.envVersionMu.Lock()
	issued := svc.envVersions["acme/"+envTestAppName]
	svc.envVersionMu.Unlock()
	if d.UpdatedAt.UnixNano() != issued {
		t.Errorf("updated_at %d, issued version %d", d.UpdatedAt.UnixNano(), issued)
	}
}

func TestSweepStaleVersionTemps_removesOnlyTheTempFiles(t *testing.T) {
	base := t.TempDir()
	keep := filepath.Join(base, "acme-api.env-version")
	stale := filepath.Join(base, "acme-api.env-version.12345.tmp")
	for _, f := range []string{keep, stale, filepath.Join(base, "unrelated.tmp")} {
		if err := os.WriteFile(f, []byte("1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n, err := sweepStaleVersionTemps(base)
	if err != nil || n != 1 {
		t.Fatalf("removed %d, %v; want 1", n, err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("the record was removed")
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("the stale temp file is still there")
	}
	if n, err := sweepStaleVersionTemps(filepath.Join(base, "missing")); err != nil || n != 0 {
		t.Errorf("a missing directory: %d, %v", n, err)
	}
}
