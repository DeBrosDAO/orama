package deployments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
