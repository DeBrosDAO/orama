package overlay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fakeDB is a wireguard_peers table with a UNIQUE index on wg_ip, which is the
// only behaviour of the real table Register depends on.
type fakeDB struct {
	allocated []string
	execErr   error
	queryErr  error

	// beforeInsert runs before each insert, so a test can simulate another node
	// taking the address between the read and the write.
	beforeInsert func()
	inserts      int
}

func (f *fakeDB) Query(_ context.Context, dest any, _ string, _ ...any) error {
	if f.queryErr != nil {
		return f.queryErr
	}
	rows := reflect.ValueOf(dest).Elem()
	slice := reflect.MakeSlice(rows.Type(), 0, len(f.allocated))
	for _, ip := range f.allocated {
		row := reflect.New(rows.Type().Elem()).Elem()
		row.Field(0).SetString(ip)
		slice = reflect.Append(slice, row)
	}
	rows.Set(slice)
	return nil
}

func (f *fakeDB) Exec(_ context.Context, query string, args ...any) (sql.Result, error) {
	if f.execErr != nil {
		return nil, f.execErr
	}
	if f.beforeInsert != nil {
		f.beforeInsert()
	}
	f.inserts++

	if len(args) > 1 {
		ip, _ := args[1].(string)
		for _, existing := range f.allocated {
			if existing == ip {
				return nil, errors.New("UNIQUE constraint failed: wireguard_peers.wg_ip")
			}
		}
		f.allocated = append(f.allocated, ip)
	}
	_ = query
	return nil, nil
}

func newPeer() Peer {
	return Peer{PublicKey: "key", PublicIP: "1.2.3.4"}
}

func TestRegister_empty_table_starts_at_first_host(t *testing.T) {
	db := &fakeDB{}
	got, err := Register(context.Background(), db, newPeer())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if want := "10.0.0.2"; got != want {
		t.Fatalf("got %q, want %q (.1 is the genesis node)", got, want)
	}
}

func TestRegister_reuses_the_lowest_freed_address(t *testing.T) {
	// A cluster that has churned: .2 was freed, .3 and .4 are live. max+1 would
	// hand out .5 and eventually roll past .254 out of the /24.
	db := &fakeDB{allocated: []string{"10.0.0.1", "10.0.0.3", "10.0.0.4"}}

	got, err := Register(context.Background(), db, newPeer())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if want := "10.0.0.2"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRegister_retries_when_a_concurrent_join_takes_the_address(t *testing.T) {
	db := &fakeDB{allocated: []string{"10.0.0.1"}}
	once := false
	db.beforeInsert = func() {
		if !once {
			once = true
			// Another join wins the race for .2 between our read and write.
			db.allocated = append(db.allocated, "10.0.0.2")
		}
	}

	got, err := Register(context.Background(), db, newPeer())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if want := "10.0.0.3"; got != want {
		t.Fatalf("got %q, want %q — the loser must take the next address, not overwrite the winner", got, want)
	}
	if db.inserts != 2 {
		t.Fatalf("expected 2 insert attempts, got %d", db.inserts)
	}
	// The winner's row must survive: OR REPLACE used to delete it.
	for _, ip := range []string{"10.0.0.2", "10.0.0.3"} {
		found := false
		for _, a := range db.allocated {
			if a == ip {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s missing from the table after the race", ip)
		}
	}
}

func TestRegister_does_not_retry_a_real_failure(t *testing.T) {
	db := &fakeDB{execErr: errors.New("database is locked")}
	if _, err := Register(context.Background(), db, newPeer()); err == nil {
		t.Fatal("expected an error")
	}
	if db.inserts != 0 {
		t.Fatalf("a non-race failure must not be retried, got %d inserts", db.inserts)
	}
}

func TestRegister_exhausted_address_space(t *testing.T) {
	db := &fakeDB{}
	for host := FirstHost; host <= LastHost; host++ {
		db.allocated = append(db.allocated, Address(host))
	}
	_, err := Register(context.Background(), db, newPeer())
	if err == nil {
		t.Fatal("expected an error when every address is allocated")
	}
	if !strings.Contains(err.Error(), "10.0.0.2-254") {
		t.Fatalf("error %q should name the exhausted range", err)
	}
}

func TestRegister_requires_key_and_ip(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Peer
	}{
		{"no key", Peer{PublicIP: "1.2.3.4"}},
		{"no ip", Peer{PublicKey: "key"}},
		{"neither", Peer{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Register(context.Background(), &fakeDB{}, tc.p); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestRegister_synthesises_an_id_when_the_node_sends_none(t *testing.T) {
	var gotID string
	// Capture the id by wrapping Exec through a tiny shim.
	shim := &idCapturingDB{fakeDB: &fakeDB{}, id: &gotID}

	if _, err := Register(context.Background(), shim, newPeer()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if want := "node-10.0.0.2"; gotID != want {
		t.Fatalf("got id %q, want %q", gotID, want)
	}
}

// realPeerID is a well-formed libp2p peer id, so the tests exercise the same
// validation a joining node's real identity goes through.
const realPeerID = "12D3KooWMtjibhBBfgbhN9Vio4jTFDeD1NLZCSxG98B6nTsoscRy"

func TestRegister_uses_the_peer_id_when_given(t *testing.T) {
	var gotID string
	shim := &idCapturingDB{fakeDB: &fakeDB{}, id: &gotID}

	p := newPeer()
	p.NodeID = realPeerID
	if _, err := Register(context.Background(), shim, p); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if gotID != realPeerID {
		t.Fatalf("got id %q, want the peer id the node sent", gotID)
	}
}

type idCapturingDB struct {
	*fakeDB
	id *string
}

func (d *idCapturingDB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if len(args) > 0 {
		*d.id, _ = args[0].(string)
	}
	return d.fakeDB.Exec(ctx, query, args...)
}

func TestNextFree_surfaces_a_read_failure(t *testing.T) {
	db := &fakeDB{queryErr: errors.New("no leader")}
	if _, err := NextFree(context.Background(), db); err == nil {
		t.Fatal("a failed read must not be reported as a free address")
	}
}

func TestNextFree_ignores_addresses_outside_the_overlay(t *testing.T) {
	// A malformed or out-of-range row must not be counted as allocated, but
	// must also not make the whole allocation fail.
	db := &fakeDB{allocated: []string{"", "not-an-ip", "10.0.1.2", "192.168.0.2", "10.0.0.2"}}
	got, err := NextFree(context.Background(), db)
	if err != nil {
		t.Fatalf("NextFree: %v", err)
	}
	if want := "10.0.0.3"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHost(t *testing.T) {
	for _, tc := range []struct {
		in    string
		want  int
		valid bool
	}{
		{"10.0.0.1", 1, true},
		{"10.0.0.254", 254, true},
		{"10.0.1.5", 0, false},
		{"10.1.0.5", 0, false},
		{"192.168.0.1", 0, false},
		{"", 0, false},
		{"garbage", 0, false},
		{"10.0.0", 0, false},
	} {
		t.Run(fmt.Sprintf("%q", tc.in), func(t *testing.T) {
			got, ok := Host(tc.in)
			if ok != tc.valid || got != tc.want {
				t.Fatalf("Host(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.valid)
			}
		})
	}
}

func TestRegister_rejectsAnUnparseableNodeID(t *testing.T) {
	// The node id is client-supplied and becomes this row's primary key, so a
	// value nothing downstream can parse must not be stored.
	for _, id := range []string{"not-a-peer-id", "node-10.0.0.5", "'; DROP TABLE wireguard_peers;--"} {
		t.Run(id, func(t *testing.T) {
			db := &fakeDB{}
			p := newPeer()
			p.NodeID = id
			if _, err := Register(context.Background(), db, p); err == nil {
				t.Fatalf("expected %q to be refused", id)
			}
			if db.inserts != 0 {
				t.Fatal("an invalid node id reached the database")
			}
		})
	}
}

func TestRegister_doesNotRetryAConflictOnTheKey(t *testing.T) {
	// A duplicate public key is not a lost allocation race: every retry would
	// pick a new address and fail on the same column.
	db := &fakeDB{execErr: errors.New("UNIQUE constraint failed: wireguard_peers.public_key")}

	if _, err := Register(context.Background(), db, newPeer()); err == nil {
		t.Fatal("expected an error")
	}
	if db.inserts != 0 {
		t.Fatalf("expected no retries, got %d insert attempts", db.inserts)
	}
}

func TestConflictsOn(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		column string
		want   bool
	}{
		{"address conflict", errors.New("UNIQUE constraint failed: wireguard_peers.wg_ip"), "wg_ip", true},
		{"key conflict is not an address conflict", errors.New("UNIQUE constraint failed: wireguard_peers.public_key"), "wg_ip", false},
		{"node id conflict is not an address conflict", errors.New("UNIQUE constraint failed: wireguard_peers.node_id"), "wg_ip", false},
		{"unrelated error", errors.New("database is locked"), "wg_ip", false},
		{"nil", nil, "wg_ip", false},
		// An unrecognised message must not be treated as a race: not retrying
		// costs a clear error, retrying costs a spin on something unfixable.
		{"unique but unnamed column", errors.New("UNIQUE constraint failed"), "wg_ip", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := conflictsOn(tc.err, tc.column); got != tc.want {
				t.Fatalf("conflictsOn = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRegister_leavesTheRowUnconfirmed(t *testing.T) {
	// The whole ghost-collection design rests on a freshly registered row being
	// unconfirmed: a join that fails after this point must leave something the
	// membership reconciler can tell apart from a live peer.
	var columns string
	shim := &columnCapturingDB{fakeDB: &fakeDB{}, query: &columns}

	if _, err := Register(context.Background(), shim, newPeer()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if strings.Contains(columns, "confirmed_at") {
		t.Fatalf("Register must not set confirmed_at; statement was:\n%s", columns)
	}
}

type columnCapturingDB struct {
	*fakeDB
	query *string
}

func (d *columnCapturingDB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	*d.query = query
	return d.fakeDB.Exec(ctx, query, args...)
}
