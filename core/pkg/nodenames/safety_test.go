package nodenames

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

func insertRow(t *testing.T, db *sql.DB, fqdn, typ, value, namespace string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value, namespace) VALUES (?,?,?,?)`, fqdn, typ, value, namespace); err != nil {
		t.Fatal(err)
	}
}

// A claimed name must not add an address next to a host the cluster publishes: the name's fqdn
// already has a row of another owner, so it is not published, and what the sync had published for
// it is taken away.
func TestSync_aNameThatShadowsAnotherOwnersHostIsNotPublished(t *testing.T) {
	db := newRegistry(t)
	insertRow(t, db, "push.nodes.stagenet.orama.network.", "A", "93.184.216.9", "system")
	insertRow(t, db, "ns-victim.nodes.stagenet.orama.network.", "A", "93.184.216.10", "namespace:victim")
	chain := &fakeChain{names: []Named{
		named("alice", "93.184.216.34"),
		named("push", "1.2.3.4"),      // not reserved on the chain; the host exists in dns_records
		named("ns-victim", "1.2.3.4"), // a namespace gateway's host
	}}
	stats, err := syncer(db, chain).Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows(t, db) {
		if r.namespace == RecordNamespace && !strings.HasPrefix(r.fqdn, "alice.") {
			t.Errorf("the sync published %v next to another owner's record", r)
		}
		if r.value == "1.2.3.4" {
			t.Errorf("the attacker's address reached dns_records: %v", r)
		}
	}
	if len(stats.Refused) != 2 {
		t.Errorf("refused = %v, want the two shadowing names", stats.Refused)
	}
	// Rows published before the other owner appeared are withdrawn on the next pass.
	insertRow(t, db, "alice.nodes.stagenet.orama.network.", "A", "9.9.9.9", "system")
	if _, err := syncer(db, chain).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows(t, db) {
		if r.namespace == RecordNamespace && strings.HasPrefix(r.fqdn, "alice.") {
			t.Errorf("a name another owner took is still published: %v", r)
		}
	}
}

// A chain node that is still catching up answers from an old height: it may list fewer names than
// the chain has. Nothing is removed on its word.
func TestSync_removesNothingWhileTheChainNodeIsCatchingUp(t *testing.T) {
	db := newRegistry(t)
	chain := &fakeChain{names: []Named{named("alice", "93.184.216.34"), named("bob", "1.1.1.1")}}
	if _, err := syncer(db, chain).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.names, chain.catchingUp = []Named{named("carol", "8.8.8.8")}, true
	stats, err := syncer(db, chain).Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !stats.CatchingUp || stats.Removed != 0 || stats.RemovalsHeld != 2 || stats.Added != 1 {
		t.Fatalf("stats = %+v, want 1 added, 2 removals held", stats)
	}
	if got := rows(t, db); len(got) != 3 {
		t.Fatalf("rows = %v", got)
	}
	chain.catchingUp = false
	stats, err = syncer(db, chain).Sync(context.Background())
	if err != nil || stats.Removed != 2 {
		t.Fatalf("after catching up: %+v, %v", stats, err)
	}
}

func TestSync_aStatusThatCannotBeReadChangesNothing(t *testing.T) {
	db := newRegistry(t)
	chain := &fakeChain{names: []Named{named("alice", "93.184.216.34")}}
	if _, err := syncer(db, chain).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.names, chain.statusErr = nil, errors.New("status unreachable")
	if _, err := syncer(db, chain).Sync(context.Background()); err == nil || !strings.Contains(err.Error(), "catching up") {
		t.Fatalf("err = %v", err)
	}
	if len(rows(t, db)) != 1 {
		t.Fatal("an unreadable status let rows go")
	}
}

// One pass removes at most a bounded share of the rows it owns, so a truncated read cannot empty
// the zone in a minute.
func TestSync_aPassRemovesOnlyABoundedShareOfTheZone(t *testing.T) {
	db := newRegistry(t)
	const total = 400
	var names []Named
	for i := 0; i < total; i++ {
		names = append(names, named(fmt.Sprintf("host-%04d", i), fmt.Sprintf("93.184.%d.%d", 1+i/250, 1+i%250)))
	}
	chain := &fakeChain{names: names}
	if _, err := syncer(db, chain).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.names = nil // a truncated read: the chain "lost" every name
	stats, err := syncer(db, chain).Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantRemoved := total * MaxRemovalsNumerator / MaxRemovalsDenominator
	if stats.Removed != wantRemoved || stats.RemovalsHeld != total-wantRemoved {
		t.Fatalf("stats = %+v, want %d removed and %d held", stats, wantRemoved, total-wantRemoved)
	}
	if got := rows(t, db); len(got) != total-wantRemoved {
		t.Fatalf("%d rows left, want %d", len(got), total-wantRemoved)
	}
	// The read is corrected: what was removed is back.
	chain.names = names
	if _, err := syncer(db, chain).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rows(t, db); len(got) != total {
		t.Fatalf("%d rows after the corrected read, want %d", len(got), total)
	}
}

func TestSync_aSmallZoneIsRemovedWholeWithinTheFloor(t *testing.T) {
	db := newRegistry(t)
	var names []Named
	for i := 0; i < MinRemovalsPerPass; i++ {
		names = append(names, named(fmt.Sprintf("host-%03d", i), fmt.Sprintf("93.184.1.%d", 1+i)))
	}
	chain := &fakeChain{names: names}
	if _, err := syncer(db, chain).Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.names = nil
	stats, err := syncer(db, chain).Sync(context.Background())
	if err != nil || stats.Removed != MinRemovalsPerPass || stats.RemovalsHeld != 0 {
		t.Fatalf("stats %+v err %v", stats, err)
	}
}

func TestSync_aCancelledPassReportsOneError(t *testing.T) {
	db := newRegistry(t)
	var names []Named
	for i := 0; i < 30; i++ {
		names = append(names, named(fmt.Sprintf("host-%02d", i), fmt.Sprintf("93.184.2.%d", 1+i)))
	}
	ctx, cancel := context.WithCancel(context.Background())
	chain := &cancellingChain{fakeChain: fakeChain{names: names}, cancel: cancel}
	_, err := syncer(db, chain).Sync(ctx)
	if err == nil || strings.Count(err.Error(), "\n") > 1 {
		t.Fatalf("err = %v, want one error and not one per row", err)
	}
}

// cancellingChain cancels the pass once it has answered, before any row is written.
type cancellingChain struct {
	fakeChain
	cancel func()
}

func (c *cancellingChain) NodeNames(ctx context.Context, key string) (Page, error) {
	page, err := c.fakeChain.NodeNames(ctx, key)
	c.cancel()
	return page, err
}

// floatDriver answers dns_records reads the way the rqlite driver does: a boolean column is the
// JSON number 1.
type floatDriver struct{}

func (floatDriver) Open(string) (driver.Conn, error) { return floatConn{}, nil }

type floatConn struct{}

func (floatConn) Prepare(string) (driver.Stmt, error) { return floatStmt{}, nil }
func (floatConn) Close() error                        { return nil }
func (floatConn) Begin() (driver.Tx, error)           { return nil, errors.New("no transactions") }

type floatStmt struct{}

func (floatStmt) Close() error                               { return nil }
func (floatStmt) NumInput() int                              { return -1 }
func (floatStmt) Exec([]driver.Value) (driver.Result, error) { return nil, errors.New("read only") }
func (floatStmt) Query([]driver.Value) (driver.Rows, error)  { return &floatRows{}, nil }

type floatRows struct{ done bool }

func (*floatRows) Columns() []string { return []string{"fqdn", "record_type", "value", "is_active"} }
func (*floatRows) Close() error      { return nil }
func (r *floatRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	copy(dest, []driver.Value{"alice.nodes.stagenet.orama.network.", "A", "93.184.216.34", float64(1)})
	return nil
}

func init() { sql.Register("nodenames-float", floatDriver{}) }

// The rqlite driver returns is_active as a float64; scanning it into a bool failed on every pass
// after the first, so nothing was ever removed or moved.
func TestOwned_readsABooleanColumnAsTheRqliteDriverReturnsIt(t *testing.T) {
	db, err := sql.Open("nodenames-float", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	have, err := owned(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	want := Record{"alice.nodes.stagenet.orama.network.", "A", "93.184.216.34"}
	if active, ok := have[want]; !ok || !active {
		t.Fatalf("have = %v", have)
	}
}

func TestTruthy(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{true, true}, {false, false}, {int64(1), true}, {int64(0), false}, {float64(1), true}, {float64(0), false},
		{"1", true}, {"0", false}, {[]byte("true"), true},
	}
	for _, c := range cases {
		got, err := truthy(c.v)
		if err != nil || got != c.want {
			t.Errorf("truthy(%v) = %v, %v", c.v, got, err)
		}
	}
	for _, v := range []any{nil, "maybe", struct{}{}} {
		if _, err := truthy(v); err == nil {
			t.Errorf("truthy(%v) succeeded", v)
		}
	}
}

// statusServer answers /status on the RPC or the gateway route.
func statusServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" && r.URL.Path != "/v1/chain/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const blockTime = "2026-10-10T12:00:00Z"

var clockAt = func(offset time.Duration) func() time.Time {
	at, _ := time.Parse(time.RFC3339Nano, blockTime)
	return func() time.Time { return at.Add(offset) }
}

func status(catchingUp bool, latest string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":-1,"result":{"sync_info":{"catching_up":%v,"latest_block_time":%q}}}`, catchingUp, latest)
}

func TestReaderChain_catchingUpFromTheRPCAndFromTheGateway(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		now  time.Duration
		want bool
	}{
		"caught up, a fresh block":              {status(false, blockTime), 10 * time.Second, false},
		"the node says it is catching up":       {status(true, blockTime), 10 * time.Second, true},
		"caught up but the block is stale":      {status(false, blockTime), MaxBlockAge + time.Second, true},
		"a block exactly at the limit is fresh": {status(false, blockTime), MaxBlockAge, false},
		"a block from the future is fresh":      {status(false, blockTime), -time.Minute, false},
	} {
		srv := statusServer(t, tc.body)
		for route, reader := range map[string]*chainread.Reader{"rpc": {RPC: srv.URL}, "gateway": {Gateway: srv.URL}} {
			got, err := ReaderChain{Reader: reader, Now: clockAt(tc.now)}.CatchingUp(context.Background())
			if err != nil || got != tc.want {
				t.Errorf("%s via %s: %v, %v", name, route, got, err)
			}
		}
	}
}

func TestReaderChain_catchingUpRefusesAStatusItCannotRead(t *testing.T) {
	for name, body := range map[string]string{
		"no sync info":                 `{"result":{"node_info":{}}}`,
		"not an object":                `[1]`,
		"no block time":                `{"result":{"sync_info":{"catching_up":false}}}`,
		"a block time that is not one": `{"result":{"sync_info":{"catching_up":false,"latest_block_time":"yesterday\u001b[2J"}}}`,
	} {
		srv := statusServer(t, body)
		_, err := (ReaderChain{Reader: &chainread.Reader{Gateway: srv.URL}, Now: clockAt(0)}).CatchingUp(context.Background())
		if err == nil {
			t.Errorf("%s accepted", name)
		} else if strings.ContainsRune(err.Error(), 0x1b) {
			t.Errorf("%s: the error carries an escape sequence: %q", name, err.Error())
		}
	}
	if _, err := (ReaderChain{Reader: &chainread.Reader{RPC: "http://127.0.0.1:1"}}).CatchingUp(context.Background()); err == nil {
		t.Error("an unreachable node was reported as caught up")
	}
}

// namesAnswer is a QueryNodeNamesResponse in protobuf: the names and the next page key.
func namesAnswer(nextKey []byte, names ...[2]string) []byte {
	var out []byte
	for _, n := range names {
		var node []byte
		node = protowire.AppendTag(node, 1, protowire.BytesType)
		node = protowire.AppendString(node, n[0])
		node = protowire.AppendTag(node, 4, protowire.BytesType)
		node = protowire.AppendString(node, n[1])
		out = protowire.AppendTag(out, 1, protowire.BytesType)
		out = protowire.AppendBytes(out, node)
	}
	if nextKey != nil {
		var page []byte
		page = protowire.AppendTag(page, 1, protowire.BytesType)
		page = protowire.AppendBytes(page, nextKey)
		out = protowire.AppendTag(out, 2, protowire.BytesType)
		out = protowire.AppendBytes(out, page)
	}
	return out
}

// Production reads the names through the node's RPC (abci_query), not the gateway route.
func TestReaderChain_readsNamesThroughTheNodesRPC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Path string `json:"path"`
			} `json:"params"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if req.Params.Path != "/orama.nodes.v1.Query/NodeNames" {
			t.Errorf("abci_query path = %q", req.Params.Path)
		}
		value := namesAnswer([]byte("next"), [2]string{"alice", "93.184.216.34"})
		out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
			"response": map[string]any{"code": 0, "value": base64.StdEncoding.EncodeToString(value)},
		}})
		_, _ = w.Write(out)
	}))
	defer srv.Close()
	page, err := ReaderChain{Reader: &chainread.Reader{RPC: srv.URL}}.NodeNames(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Nodes) != 1 || page.Nodes[0].Name != "alice" || page.Nodes[0].IPs[0] != "93.184.216.34" || page.NextKey != base64.StdEncoding.EncodeToString([]byte("next")) {
		t.Fatalf("page = %+v", page)
	}
}
