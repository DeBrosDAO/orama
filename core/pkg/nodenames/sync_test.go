package nodenames

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const testZone = "nodes.stagenet.orama.network"

// newRegistry is an in-memory dns_records with the registry's uniqueness rule.
func newRegistry(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := sql.Open("sqlite3", dsn)
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
	return db
}

// fakeChain serves names in pages of pageSize, in the order given.
type fakeChain struct {
	names    []Named
	pageSize int
	err      error
	// failAt makes the call for this page number (1-based) fail.
	failAt int
	// loop answers the same page key for ever.
	loop  bool
	calls int
	// catchingUp and statusErr are the chain node's status.
	catchingUp bool
	statusErr  error
}

func (f *fakeChain) CatchingUp(context.Context) (bool, error) { return f.catchingUp, f.statusErr }

func (f *fakeChain) NodeNames(_ context.Context, key string) (Page, error) {
	f.calls++
	if f.err != nil {
		return Page{}, f.err
	}
	if f.failAt != 0 && f.calls == f.failAt {
		return Page{}, errors.New("the chain went away")
	}
	start := 0
	if key != "" {
		fmt.Sscanf(key, "k%d", &start)
	}
	end := start + f.pageSize
	if f.pageSize == 0 || end >= len(f.names) {
		end = len(f.names)
	}
	page := Page{Nodes: f.names[start:end]}
	if end < len(f.names) {
		page.NextKey = fmt.Sprintf("k%d", end)
	}
	if f.loop {
		page.NextKey = "k0"
	}
	return page, nil
}

func registryOf(db *sql.DB) func() (*sql.DB, error) {
	return func() (*sql.DB, error) { return db, nil }
}

func syncer(db *sql.DB, chain Chain) *Syncer {
	return &Syncer{Registry: registryOf(db), Chain: chain, Zone: testZone}
}

type row struct {
	fqdn, typ, value, namespace string
	active                      bool
}

func rows(t *testing.T, db *sql.DB) []row {
	t.Helper()
	rs, err := db.Query(`SELECT fqdn, record_type, value, namespace, is_active FROM dns_records ORDER BY fqdn, record_type, value`)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.fqdn, &r.typ, &r.value, &r.namespace, &r.active); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func named(name string, ips ...string) Named {
	return Named{Name: name, NodeID: "node-" + name, IPs: ips}
}

func TestSync_writesTheChainsNames(t *testing.T) {
	db := newRegistry(t)
	chain := &fakeChain{names: []Named{named("alice", "93.184.216.34", "2606:4700:4700::1111"), named("bob", "1.1.1.1")}}
	stats, err := syncer(db, chain).Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Names != 2 || stats.Added != 3 || stats.Removed != 0 {
		t.Errorf("stats = %+v", stats)
	}
	want := []row{
		{"alice.nodes.stagenet.orama.network.", "A", "93.184.216.34", RecordNamespace, true},
		{"alice.nodes.stagenet.orama.network.", "AAAA", "2606:4700:4700::1111", RecordNamespace, true},
		{"bob.nodes.stagenet.orama.network.", "A", "1.1.1.1", RecordNamespace, true},
	}
	got := rows(t, db)
	if len(got) != len(want) {
		t.Fatalf("rows = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestSync_isIdempotent(t *testing.T) {
	db := newRegistry(t)
	s := syncer(db, &fakeChain{names: []Named{named("alice", "93.184.216.34")}})
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Changed() || len(rows(t, db)) != 1 {
		t.Fatalf("a second pass wrote: %+v, rows %v", stats, rows(t, db))
	}
}

func TestSync_removesAReleasedNameAndAMovedAddress(t *testing.T) {
	db := newRegistry(t)
	chain := &fakeChain{names: []Named{named("alice", "93.184.216.34"), named("bob", "1.1.1.1")}}
	s := syncer(db, chain)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.names = []Named{named("alice", "8.8.8.8")} // alice's node moved; bob released the name
	stats, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 1 || stats.Removed != 2 {
		t.Errorf("stats = %+v, want 1 added and 2 removed", stats)
	}
	got := rows(t, db)
	if len(got) != 1 || got[0].value != "8.8.8.8" {
		t.Fatalf("rows = %v", got)
	}
}

func TestSync_emptyChainRemovesEveryOwnedRow(t *testing.T) {
	db := newRegistry(t)
	chain := &fakeChain{names: []Named{named("alice", "93.184.216.34")}}
	s := syncer(db, chain)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.names = nil
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rows(t, db); len(got) != 0 {
		t.Fatalf("rows = %v", got)
	}
}

func TestSync_neverTouchesRowsItDoesNotOwn(t *testing.T) {
	db := newRegistry(t)
	for _, r := range []row{
		{"nodes.stagenet.orama.network.", "A", "93.184.216.1", "system", true},
		{"ns1.nodes.stagenet.orama.network.", "A", "93.184.216.2", "system", true},
		{"alice.nodes.stagenet.orama.network.", "A", "93.184.216.34", "namespace:alice", true}, // another owner holds this exact row
		{"x.other.example.", "A", "1.1.1.1", "deployment", true},
	} {
		if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value, namespace, is_active) VALUES (?,?,?,?,?)`,
			r.fqdn, r.typ, r.value, r.namespace, r.active); err != nil {
			t.Fatal(err)
		}
	}
	chain := &fakeChain{names: []Named{named("alice", "93.184.216.34")}}
	s := syncer(db, chain)
	for i := 0; i < 2; i++ {
		if _, err := s.Sync(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	chain.names = nil
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := rows(t, db)
	if len(got) != 4 {
		t.Fatalf("a row that is not the sync's changed: %v", got)
	}
	for _, r := range got {
		if r.namespace == RecordNamespace {
			t.Errorf("the sync claimed a row another owner holds: %v", r)
		}
	}
}

func TestSync_reactivatesARowSomethingDeactivated(t *testing.T) {
	db := newRegistry(t)
	s := syncer(db, &fakeChain{names: []Named{named("alice", "93.184.216.34")}})
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE dns_records SET is_active = FALSE`); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reactivated != 1 || !rows(t, db)[0].active {
		t.Fatalf("stats %+v rows %v", stats, rows(t, db))
	}
}

func TestSync_aFailedReadChangesNothing(t *testing.T) {
	db := newRegistry(t)
	names := []Named{named("alice", "93.184.216.34"), named("bob", "1.1.1.1"), named("carol", "8.8.8.8")}
	if _, err := syncer(db, &fakeChain{names: names, pageSize: 1}).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The chain dies on the second page: the names on the pages not read must not be deleted.
	_, err := syncer(db, &fakeChain{names: names[:1], pageSize: 1, err: errors.New("down")}).Sync(context.Background())
	if err == nil {
		t.Fatal("a failed read succeeded")
	}
	if got := rows(t, db); len(got) != 3 {
		t.Fatalf("a failed read changed the zone: %v", got)
	}
	_, err = syncer(db, &fakeChain{names: names, pageSize: 1, failAt: 2}).Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "page 2") {
		t.Fatalf("err = %v, want it to name page 2", err)
	}
	if got := rows(t, db); len(got) != 3 {
		t.Fatalf("a half-read list changed the zone: %v", got)
	}
}

func TestSync_pagesThroughEveryName(t *testing.T) {
	db := newRegistry(t)
	var names []Named
	for i := 0; i < 25; i++ {
		names = append(names, named(fmt.Sprintf("host-%02d", i), fmt.Sprintf("93.184.%d.%d", i+1, i+1)))
	}
	chain := &fakeChain{names: names, pageSize: 10}
	stats, err := syncer(db, chain).Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if chain.calls != 3 || stats.Names != 25 || stats.Added != 25 {
		t.Errorf("calls %d stats %+v", chain.calls, stats)
	}
}

func TestSync_aListThatDoesNotAdvanceIsAFault(t *testing.T) {
	db := newRegistry(t)
	_, err := syncer(db, &fakeChain{names: []Named{named("alice", "93.184.216.34"), named("bob", "1.1.1.1")}, pageSize: 1, loop: true}).Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not advance") {
		t.Fatalf("err = %v", err)
	}
	if len(rows(t, db)) != 0 {
		t.Fatal("a looping list wrote rows")
	}
}

func TestSync_refusedAddressesAreReportedNotPublished(t *testing.T) {
	db := newRegistry(t)
	stats, err := syncer(db, &fakeChain{names: []Named{named("alice", "10.0.0.5", "93.184.216.34")}}).Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Refused) != 1 || stats.Refused[0].IP != "10.0.0.5" {
		t.Errorf("refused = %v", stats.Refused)
	}
	if got := rows(t, db); len(got) != 1 || got[0].value != "93.184.216.34" {
		t.Fatalf("rows = %v", got)
	}
}

func TestSync_aChangedZoneRemovesTheOldZonesRows(t *testing.T) {
	db := newRegistry(t)
	chain := &fakeChain{names: []Named{named("alice", "93.184.216.34")}}
	if _, err := syncer(db, chain).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	moved := &Syncer{Registry: registryOf(db), Chain: chain, Zone: "names.stagenet.orama.network"}
	if _, err := moved.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := rows(t, db)
	if len(got) != 1 || got[0].fqdn != "alice.names.stagenet.orama.network." {
		t.Fatalf("rows = %v", got)
	}
}

func TestSync_aBadZoneWritesNothing(t *testing.T) {
	db := newRegistry(t)
	chain := &fakeChain{names: []Named{named("alice", "93.184.216.34")}}
	for _, zone := range []string{"", "Stagenet.Orama.Network", "localhost"} {
		if _, err := (&Syncer{Registry: registryOf(db), Chain: chain, Zone: zone}).Sync(context.Background()); err == nil {
			t.Errorf("zone %q accepted", zone)
		}
	}
	if chain.calls != 0 || len(rows(t, db)) != 0 {
		t.Fatalf("a bad zone read the chain (%d calls) or wrote %v", chain.calls, rows(t, db))
	}
}

func TestSync_capsTheWritesOfOnePass(t *testing.T) {
	db := newRegistry(t)
	var names []Named
	total := MaxWritesPerPass + 3
	for i := 0; i < total; i++ {
		names = append(names, named(fmt.Sprintf("host-%05d", i), fmt.Sprintf("93.%d.%d.%d", 1+i/65536, (i/256)%256, 1+i%255)))
	}
	chain := &fakeChain{names: names}
	s := syncer(db, chain)
	first, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Added != MaxWritesPerPass || first.Remaining != 3 {
		t.Fatalf("first pass %+v, want %d added and 3 remaining", first, MaxWritesPerPass)
	}
	second, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Added != 3 || second.Remaining != 0 {
		t.Fatalf("second pass %+v", second)
	}
}

func TestRun_syncsRepeatedlyUntilCancelled(t *testing.T) {
	db := newRegistry(t)
	ctx, cancel := context.WithCancel(context.Background())
	passes := make(chan Stats, 4)
	done := make(chan struct{})
	go func() {
		syncer(db, &fakeChain{names: []Named{named("alice", "93.184.216.34")}}).Run(ctx, func(s Stats, err error) {
			if err != nil {
				t.Errorf("pass failed: %v", err)
			}
			passes <- s
		})
		close(done)
	}()
	select {
	case first := <-passes:
		if first.Added != 1 {
			t.Errorf("first pass %+v", first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no first pass")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop when its context ended")
	}
}

func TestSync_aRegistryThatIsNotThereIsReportedAfterTheChainWasRead(t *testing.T) {
	s := &Syncer{Registry: func() (*sql.DB, error) { return nil, errors.New("no rqlite adapter yet") }, Chain: &fakeChain{names: []Named{named("alice", "93.184.216.34")}}, Zone: testZone}
	_, err := s.Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "open the cluster registry") {
		t.Fatalf("err = %v", err)
	}
}
