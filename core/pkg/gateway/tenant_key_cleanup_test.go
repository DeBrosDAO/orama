package gateway

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// recordingKeyDB answers the count query and records what it was asked to run.
type recordingKeyDB struct {
	count      int
	countErr   error
	deleteErr  error
	statements []string
}

func (r *recordingKeyDB) Query(_ context.Context, sql string, _ ...interface{}) (*client.QueryResult, error) {
	r.statements = append(r.statements, strings.Join(strings.Fields(sql), " "))
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(sql)), "SELECT") {
		if r.countErr != nil {
			return nil, r.countErr
		}
		return &client.QueryResult{Columns: []string{"COUNT(*)"}, Rows: [][]interface{}{{int64(r.count)}}, Count: 1}, nil
	}
	if r.deleteErr != nil {
		return nil, r.deleteErr
	}
	return &client.QueryResult{}, nil
}

// The rows are raw `ak_…` credentials in a database the tenant can read.
func TestPurgeTenantPlaintextAPIKeys_removesThem(t *testing.T) {
	db := &recordingKeyDB{count: 3}

	n, err := purgeTenantPlaintextAPIKeys(context.Background(), db)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 3 {
		t.Errorf("removed %d rows, want 3", n)
	}

	var deleted string
	for _, stmt := range db.statements {
		if strings.HasPrefix(stmt, "DELETE") {
			deleted = stmt
		}
	}
	if deleted == "" {
		t.Fatalf("nothing was deleted: %v", db.statements)
	}
	if !strings.Contains(deleted, "LIKE 'ak_%'") {
		t.Errorf("the delete is not confined to plaintext rows: %s", deleted)
	}
}

// A hashed row is inert too, but it is not a credential, and deleting more than
// the thing that is wrong is how a cleanup becomes data loss.
func TestPurgeTenantPlaintextAPIKeys_leavesHashedRowsAlone(t *testing.T) {
	db := &recordingKeyDB{count: 2}
	if _, err := purgeTenantPlaintextAPIKeys(context.Background(), db); err != nil {
		t.Fatalf("purge: %v", err)
	}
	for _, stmt := range db.statements {
		if strings.HasPrefix(stmt, "DELETE") && !strings.Contains(stmt, "LIKE 'ak_%'") {
			t.Errorf("an unqualified delete would take hashed rows with it: %s", stmt)
		}
	}
}

func TestPurgeTenantPlaintextAPIKeys_doesNothingWhenThereIsNothingToDo(t *testing.T) {
	db := &recordingKeyDB{count: 0}
	n, err := purgeTenantPlaintextAPIKeys(context.Background(), db)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 0 {
		t.Errorf("removed %d rows from an empty table", n)
	}
	for _, stmt := range db.statements {
		if strings.HasPrefix(stmt, "DELETE") {
			t.Errorf("a delete ran with nothing to delete: %s", stmt)
		}
	}
}

// A database with no api_keys table has not run the core migrations, which is
// the state this wants anyway — not a failure to report.
func TestPurgeTenantPlaintextAPIKeys_toleratesAMissingTable(t *testing.T) {
	db := &recordingKeyDB{countErr: errNoSuchTable{}}
	n, err := purgeTenantPlaintextAPIKeys(context.Background(), db)
	if err != nil {
		t.Fatalf("a missing table was reported as an error: %v", err)
	}
	if n != 0 {
		t.Errorf("removed %d rows", n)
	}
}

// A delete that fails is reported. The rows are still on disk, and saying
// otherwise would be worse than the leftover.
func TestPurgeTenantPlaintextAPIKeys_reportsAFailedDelete(t *testing.T) {
	db := &recordingKeyDB{count: 1, deleteErr: errNoSuchTable{}}
	if _, err := purgeTenantPlaintextAPIKeys(context.Background(), db); err == nil {
		t.Fatal("a failed delete was reported as a successful cleanup")
	}
}

func TestPurgeTenantPlaintextAPIKeys_nilDatabase(t *testing.T) {
	if _, err := purgeTenantPlaintextAPIKeys(context.Background(), nil); err != nil {
		t.Errorf("purge with no database: %v", err)
	}
}

type errNoSuchTable struct{}

func (errNoSuchTable) Error() string { return "no such table: api_keys" }

// Only a missing table means there is nothing to remove. A count that failed
// for any other reason says nothing about whether the credentials are still
// there, and reporting "nothing to remove" would let the gateway go ready with
// them on disk.
func TestPurgeTenantPlaintextAPIKeys_aFailedCountIsAnError(t *testing.T) {
	db := &recordingKeyDB{countErr: errors.New("leader not found")}
	_, err := purgeTenantPlaintextAPIKeys(context.Background(), db)
	if err == nil {
		t.Fatal("a count that failed was reported as a clean database")
	}
	if !strings.Contains(err.Error(), "leader not found") {
		t.Errorf("the error does not carry the cause: %v", err)
	}
	for _, stmt := range db.statements {
		if strings.HasPrefix(stmt, "DELETE") {
			t.Errorf("a delete ran after the count failed: %s", stmt)
		}
	}
}

// flakyKeyDB is a namespace database whose count fails `failures` times, then
// answers with one plaintext row.
type flakyKeyDB struct {
	client.DatabaseClient
	mu       sync.Mutex
	failures int
	counts   int
	deleted  bool
}

func (f *flakyKeyDB) Query(_ context.Context, sql string, _ ...interface{}) (*client.QueryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasPrefix(strings.TrimSpace(sql), "DELETE") {
		f.deleted = true
		return &client.QueryResult{}, nil
	}
	f.counts++
	if f.counts <= f.failures {
		return nil, errors.New("leader not found")
	}
	return &client.QueryResult{Rows: [][]interface{}{{int64(1)}}}, nil
}

// The removal gates readiness: while it fails the gateway stays not ready,
// says why with the post-schema code, and the readiness loop retries it.
func TestPurgeStep_failureKeepsTheGatewayNotReadyAndIsRetried(t *testing.T) {
	fastRetries(t)
	db := openSQLite(t)
	migrate(t, db)
	g, deps := postSchemaGateway(t, db)
	g.ready = newReadiness()
	keys := &flakyKeyDB{failures: 3}
	g.client = &fakeNetworkClient{db: keys}
	steps := g.postSchemaSteps(&Config{RQLiteDSN: "http://10.0.0.2:10100", GlobalRQLiteDSN: "http://10.0.0.1:5001"}, deps)

	var states []ReadinessReason
	prepare := func(ctx context.Context) error {
		_, code, _, _ := g.ready.snapshot()
		states = append(states, code)
		return runGatingSteps(ctx, steps)
	}
	done := make(chan struct{})
	go func() {
		g.convergeSchema(context.Background(), prepare, func() string { return "Leader" })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the readiness loop never converged")
	}

	if !g.IsReady() {
		t.Fatal("the gateway did not become ready once the removal succeeded")
	}
	if keys.counts != 4 || !keys.deleted {
		t.Fatalf("count ran %d times (want 4: three failures, one success), deleted=%v", keys.counts, keys.deleted)
	}
	// Every retry after the first saw the gateway not ready for this reason.
	for i, code := range states[1:] {
		if code != ReasonPostSchema {
			t.Errorf("attempt %d ran while the gateway reported %q, want %q", i+2, code, ReasonPostSchema)
		}
	}
}
