package nodeapi

// The stamp level against a real registry: the handler's SQL, migration 081 and
// the floor's read together, with the statements a build from before the
// capability runs. A fake that records queries cannot show that a rolled-back
// node stops counting as nonced, because that depends on what the old build's
// fixed SQL leaves behind.

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/nodeapi"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// The register and heartbeat statements of a build from before the stamp level:
// they name neither stamp_level nor stamp_level_at.
const (
	preCapabilityRegisterSQL = `
		INSERT INTO dns_nodes (id, ip_address, internal_ip, region, status, ssh_user, environment, operator_wallet, role, last_seen, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', ?, ?, ?, ?, datetime('now'), datetime('now'), datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			ip_address = excluded.ip_address,
			internal_ip = excluded.internal_ip,
			region = excluded.region,
			status = 'active',
			ssh_user = COALESCE(NULLIF(excluded.ssh_user, ''), dns_nodes.ssh_user),
			environment = COALESCE(NULLIF(excluded.environment, ''), dns_nodes.environment),
			operator_wallet = COALESCE(NULLIF(excluded.operator_wallet, ''), dns_nodes.operator_wallet),
			role = COALESCE(NULLIF(excluded.role, ''), dns_nodes.role),
			last_seen = datetime('now'),
			updated_at = datetime('now')`
	preCapabilityHeartbeatSQL = `UPDATE dns_nodes SET status = 'active', last_seen = datetime('now'), updated_at = datetime('now'),
		role = COALESCE(NULLIF(?, ''), role),
		environment = COALESCE(NULLIF(?, ''), environment)
		WHERE id = ?`
)

// registry is a migrated in-memory registry behind the real handler.
type registry struct {
	t       *testing.T
	raw     *sql.DB
	handler *Handler
	floor   *auth.LegacyFloor
}

// sqlBackedDB answers writes from a real database and the credential and
// admission lookups from the recording fake.
type sqlBackedDB struct {
	*recordingDB
	real rqlite.Client
}

func (d sqlBackedDB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.real.Exec(ctx, query, args...)
}

func newRegistry(t *testing.T) *registry {
	t.Helper()
	raw, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), raw, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	real := rqlite.NewClient(raw)
	db := sqlBackedDB{recordingDB: &recordingDB{affected: 1}, real: real}
	return &registry{
		t:       t,
		raw:     raw,
		handler: NewHandler(zap.NewNop(), db, NewCredentials(db), nil),
		floor:   auth.RegistryLegacyFloor(real, nil),
	}
}

func (g *registry) register(nodeID, internalIP string, level int) {
	g.t.Helper()
	req := validRegistration()
	req.InternalIP, req.IPAddress, req.StampLevel = internalIP, internalIP, level
	w := httptest.NewRecorder()
	g.handler.HandleRegister(w, post(g.t, "/v1/internal/node/register", nodeID, req))
	if w.Code != http.StatusNoContent {
		g.t.Fatalf("register %s: status %d, body %q", nodeID, w.Code, w.Body.String())
	}
}

func (g *registry) heartbeat(nodeID string, beat nodeapi.HeartbeatRequest) {
	g.t.Helper()
	w := httptest.NewRecorder()
	g.handler.HandleHeartbeat(w, post(g.t, "/v1/internal/node/heartbeat", nodeID, beat))
	if w.Code != http.StatusOK {
		g.t.Fatalf("heartbeat %s: status %d, body %q", nodeID, w.Code, w.Body.String())
	}
}

func (g *registry) exec(query string, args ...any) {
	g.t.Helper()
	if _, err := g.raw.Exec(query, args...); err != nil {
		g.t.Fatalf("exec %q: %v", query, err)
	}
}

// pass moves the clock on for everything recorded so far: the registry stamps
// seconds, and a restart takes longer than one.
func (g *registry) pass() {
	g.t.Helper()
	g.exec(`UPDATE dns_nodes SET last_seen = datetime(last_seen, '-1 minute'), stamp_level_at = CASE WHEN stamp_level_at = '' THEN '' ELSE datetime(stamp_level_at, '-1 minute') END`)
}

// levels is what the floor reads.
func (g *registry) levels() []int {
	g.t.Helper()
	got, err := g.floor.Read(context.Background())
	if err != nil {
		g.t.Fatalf("read the floor: %v", err)
	}
	return got
}

func (g *registry) storedLevel(nodeID string) int {
	g.t.Helper()
	var level int
	if err := g.raw.QueryRow(`SELECT stamp_level FROM dns_nodes WHERE id = ?`, nodeID).Scan(&level); err != nil {
		g.t.Fatalf("read the stored level: %v", err)
	}
	return level
}

func requireLevels(t *testing.T, got []int, want ...int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("levels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("levels = %v, want %v", got, want)
		}
	}
}

func TestStampLevel_aNodeThatRegistersAndBeatsAtTheNoncedLevelIsNonced(t *testing.T) {
	g := newRegistry(t)
	g.register(testNodeID, "10.0.0.7", auth.StampLevelNonced)
	requireLevels(t, g.levels(), auth.StampLevelNonced)

	g.pass()
	g.heartbeat(testNodeID, nodeapi.HeartbeatRequest{StampLevel: auth.StampLevelNonced})
	requireLevels(t, g.levels(), auth.StampLevelNonced)
}

// The point of the capability over the release number: a node that never
// reported, whatever release it runs, holds the older stamps accepted.
func TestStampLevel_aNodeThatNeverReportedIsLegacyAmongNoncedNodes(t *testing.T) {
	g := newRegistry(t)
	g.register(testNodeID, "10.0.0.7", auth.StampLevelNonced)
	g.exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip) VALUES (?, '203.0.113.9', '10.0.0.9')`, otherNodeID)
	if got := g.levels(); len(got) != 2 {
		t.Fatalf("levels = %v, want two nodes", got)
	}
	if !g.floor.Accepts() {
		t.Fatal("the older stamps were refused with a node that never reported its level")
	}
}

// A node rolled back to a build from before the capability keeps the level it
// last reported (that build's statements do not name the column), so the floor
// must count it as legacy from the fact that the node was seen again without
// reporting.
func TestStampLevel_aRolledBackNodeCountsAsLegacyAgain(t *testing.T) {
	t.Run("its heartbeat", func(t *testing.T) {
		g := newRegistry(t)
		g.register(testNodeID, "10.0.0.7", auth.StampLevelNonced)
		g.pass()
		g.exec(preCapabilityHeartbeatSQL, "node", "devnet", testNodeID)
		if got := g.storedLevel(testNodeID); got != auth.StampLevelNonced {
			t.Fatalf("the pre-capability heartbeat changed the stored level to %d; the test no longer models a build that leaves it", got)
		}
		requireLevels(t, g.levels(), auth.StampLevelLegacy)
		if !g.floor.Accepts() {
			t.Fatal("the older stamps are refused while a rolled-back node signs only them")
		}
	})
	t.Run("its registration after the restart", func(t *testing.T) {
		g := newRegistry(t)
		g.register(testNodeID, "10.0.0.7", auth.StampLevelNonced)
		g.pass()
		req := validRegistration()
		g.exec(preCapabilityRegisterSQL, testNodeID, req.IPAddress, req.InternalIP, req.Region, req.SSHUser, req.Environment, req.OperatorWallet, "node")
		requireLevels(t, g.levels(), auth.StampLevelLegacy)
	})
	t.Run("upgraded again", func(t *testing.T) {
		g := newRegistry(t)
		g.register(testNodeID, "10.0.0.7", auth.StampLevelNonced)
		g.pass()
		g.exec(preCapabilityHeartbeatSQL, "node", "devnet", testNodeID)
		g.pass()
		g.heartbeat(testNodeID, nodeapi.HeartbeatRequest{StampLevel: auth.StampLevelNonced})
		requireLevels(t, g.levels(), auth.StampLevelNonced)
	})
}

// A build that reports a lower level says so, and a heartbeat that does not
// carry the field is the older build: both replace a recorded higher level.
func TestStampLevel_aHeartbeatReplacesTheRecordedLevel(t *testing.T) {
	g := newRegistry(t)
	g.register(testNodeID, "10.0.0.7", auth.StampLevelNonced)
	g.pass()
	g.heartbeat(testNodeID, nodeapi.HeartbeatRequest{StampLevel: auth.StampLevelLegacy})
	requireLevels(t, g.levels(), auth.StampLevelLegacy)

	g.heartbeat(testNodeID, nodeapi.HeartbeatRequest{StampLevel: auth.StampLevelNonced})
	requireLevels(t, g.levels(), auth.StampLevelNonced)

	g.pass()
	w := httptest.NewRecorder()
	g.handler.HandleHeartbeat(w, post(t, "/v1/internal/node/heartbeat", testNodeID, map[string]string{"role": "node"}))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	requireLevels(t, g.levels(), auth.StampLevelLegacy)
}

func TestStampLevel_aRetiredNodeIsLeftOutAndAnEmptyRegistryRefusesTheOlderStamps(t *testing.T) {
	g := newRegistry(t)
	if len(g.levels()) != 0 || g.floor.Accepts() {
		t.Fatal("an empty registry must hold nothing old")
	}
	g.register(testNodeID, "10.0.0.7", auth.StampLevelNonced)
	g.exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, last_seen) VALUES (?, '203.0.113.9', '10.0.0.9', ?)`, otherNodeID, constants.RetiredNodeLastSeen)
	requireLevels(t, g.levels(), auth.StampLevelNonced)
}
