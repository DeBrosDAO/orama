package nodenames

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// countDriver is SQLite that counts the transactions opened on it: an ExecBatch is one.
type countDriver struct{ begins *atomic.Int64 }

func (d countDriver) Open(dsn string) (driver.Conn, error) {
	inner, err := (&sqlite3.SQLiteDriver{}).Open(dsn)
	if err != nil {
		return nil, err
	}
	return &countConn{Conn: inner, begins: d.begins}, nil
}

type countConn struct {
	driver.Conn
	begins *atomic.Int64
}

func (c *countConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.begins.Add(1)
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

var driverSeq atomic.Int64

// countingRegistry is a registry on a counting driver, and the counter.
func countingRegistry(t *testing.T) (*sql.DB, *atomic.Int64) {
	t.Helper()
	begins := &atomic.Int64{}
	name := fmt.Sprintf("nodenames-count-%d", driverSeq.Add(1))
	sql.Register(name, countDriver{begins: begins})
	db, err := sql.Open(name, fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE dns_records (
		id INTEGER PRIMARY KEY AUTOINCREMENT, fqdn TEXT NOT NULL,
		record_type TEXT NOT NULL DEFAULT 'A', value TEXT NOT NULL,
		ttl INTEGER NOT NULL DEFAULT 300, priority INTEGER DEFAULT 0,
		namespace TEXT NOT NULL DEFAULT 'system', deployment_id TEXT, node_id TEXT,
		is_active BOOLEAN NOT NULL DEFAULT TRUE,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		created_by TEXT NOT NULL DEFAULT 'system',
		UNIQUE(fqdn, record_type, value))`); err != nil {
		t.Fatal(err)
	}
	return db, begins
}

func manyNames(n int) []Named {
	var names []Named
	for i := 0; i < n; i++ {
		names = append(names, named(fmt.Sprintf("host-%04d", i), fmt.Sprintf("93.184.%d.%d", 1+i/250, 1+i%250)))
	}
	return names
}

// A pass writes in transactions of BatchSize rows: one request each, not one per row.
func TestSync_writesInBatchesNotRowByRow(t *testing.T) {
	db, begins := countingRegistry(t)
	const total = 250
	stats, err := syncer(db, &fakeChain{names: manyNames(total)}).Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != total {
		t.Fatalf("stats = %+v", stats)
	}
	if want := int64((total + BatchSize - 1) / BatchSize); begins.Load() != want {
		t.Fatalf("%d requests for %d rows, want %d", begins.Load(), total, want)
	}
	// A pass with nothing to write sends nothing.
	begins.Store(0)
	if _, err := syncer(db, &fakeChain{names: manyNames(total)}).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if begins.Load() != 0 {
		t.Fatalf("an up-to-date zone sent %d requests", begins.Load())
	}
}

// The database blames a row: that row is reported, the others of its batch still land.
func TestSync_aRowTheDatabaseRejectsDoesNotStarveItsBatch(t *testing.T) {
	db, _ := countingRegistry(t)
	if _, err := db.Exec(`CREATE TRIGGER poison BEFORE INSERT ON dns_records WHEN NEW.value = '93.184.250.1'
		BEGIN SELECT RAISE(ABORT, 'poisoned row'); END`); err != nil {
		t.Fatal(err)
	}
	names := append(manyNames(5), named("poisoned", "93.184.250.1"))
	stats, err := syncer(db, &fakeChain{names: names}).Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "poisoned") {
		t.Fatalf("err = %v, want the rejected row named", err)
	}
	if stats.Added != 5 {
		t.Fatalf("stats = %+v, want the five good rows written", stats)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE namespace = ?`, RecordNamespace).Scan(&n); err != nil || n != 5 {
		t.Fatalf("%d rows, %v", n, err)
	}
}

// A transport fault blames no row: the pass ends with it, once.
func TestSync_aFailedRequestEndsThePassWithOneError(t *testing.T) {
	db, _ := countingRegistry(t)
	names := manyNames(30)
	if _, err := syncer(db, &fakeChain{names: names}).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE dns_records`); err != nil {
		t.Fatal(err)
	}
	_, err := syncer(db, &fakeChain{names: names}).Sync(context.Background())
	if err == nil {
		t.Fatal("a missing table was not an error")
	}
}

func TestNextWait(t *testing.T) {
	live := context.Background()
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	timeout := fmt.Errorf("the pass ended before every row was written: %w", context.DeadlineExceeded)
	for name, tc := range map[string]struct {
		run   context.Context
		stats Stats
		err   error
		want  time.Duration
	}{
		"up to date":                    {live, Stats{}, nil, SyncInterval},
		"budget left over":              {live, Stats{Remaining: 3}, nil, CatchUpDelay},
		"the pass hit its own timeout":  {live, Stats{}, timeout, CatchUpDelay},
		"the timeout joined with a row": {live, Stats{}, errors.Join(errors.New("add x"), timeout), CatchUpDelay},
		"another failure":               {live, Stats{Remaining: 3}, errors.New("the chain is down"), SyncInterval},
		"the run is ending":             {ended, Stats{}, timeout, SyncInterval},
	} {
		if got := nextWait(tc.run, tc.stats, tc.err); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

// The pass's own deadline ends it part way with an error that says so, and the rows already
// written stay written.
func TestSync_aDeadlineMidPassIsADeadlineError(t *testing.T) {
	db := newRegistry(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := syncer(db, &fakeChain{names: manyNames(10)}).Sync(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
}
