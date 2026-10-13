package nodeapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/constants"
	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/overlay"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// A node no join admitted — a key its caller made up, no peer row, a registry
// that already has nodes — is refused, writes nothing and is audited.
func TestRegister_aNodeNoJoinAdmittedIsRefused(t *testing.T) {
	db := &recordingDB{affected: 1, admission: &admissionRow{Registry: 3}}
	h := newHandler(db)
	var audited int
	h.recorder = func(*http.Request, gwauth.AuditEvent) { audited++ }
	w := httptest.NewRecorder()
	h.HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, validRegistration()))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d (%q), want 403 for a node never admitted", w.Code, w.Body.String())
	}
	if len(db.calls) != 0 {
		t.Fatalf("wrote %d rows for a node never admitted", len(db.calls))
	}
	if audited != 1 {
		t.Fatalf("%d audit lines for the refusal, want 1", audited)
	}
}

// Not knowing is not admission.
func TestRegister_anUnreadableAdmissionIsRefusedAsUnavailable(t *testing.T) {
	db := &recordingDB{affected: 1, admissionErr: errUnreadable}
	w := httptest.NewRecorder()
	newHandler(db).HandleRegister(w, post(t, "/v1/internal/node/register", testNodeID, validRegistration()))
	if w.Code != http.StatusServiceUnavailable || len(db.calls) != 0 {
		t.Fatalf("status = %d with %d writes, want 503 and none", w.Code, len(db.calls))
	}
}

// A request from a process on this host is attributable to this host's overlay
// address; one across the mesh to the sender's.
func TestSourceOverlayIP_loopbackIsThisHostAndTheMeshIsTheSender(t *testing.T) {
	h := newHandler(&recordingDB{})
	h.SetLocalOverlayIP(func() (string, error) { return "10.0.0.7", nil })
	local := httptest.NewRequest(http.MethodPost, "/", nil)
	local.RemoteAddr = loopbackCaller
	if got := h.sourceOverlayIP(local); got != "10.0.0.7" {
		t.Errorf("loopback attributed to %q, want this host's 10.0.0.7", got)
	}
	mesh := httptest.NewRequest(http.MethodPost, "/", nil)
	mesh.RemoteAddr = "10.0.0.3:41000"
	if got := h.sourceOverlayIP(mesh); got != "10.0.0.3" {
		t.Errorf("mesh request attributed to %q, want the sender's 10.0.0.3", got)
	}
	h.SetLocalOverlayIP(func() (string, error) { return "", errors.New("wg0 down") })
	if got := h.sourceOverlayIP(local); got != "" {
		t.Errorf("loopback with no readable overlay address attributed to %q, want none", got)
	}
}

// registryDB is a real SQLite with the migrations applied, for the admission
// rules themselves.
func registryDB(t *testing.T) (rqlite.Client, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err := rqlite.ApplyEmbeddedMigrations(context.Background(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return rqlite.NewClient(db), db
}

func addNode(t *testing.T, db *sql.DB, id, wgIP, lastSeen string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status, last_seen) VALUES (?, ?, ?, 'active', ?)`,
		id, "203.0.113."+wgIP[len("10.0.0."):], wgIP, lastSeen); err != nil {
		t.Fatal(err)
	}
}

func addPeer(t *testing.T, db *sql.DB, nodeID, wgIP string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO wireguard_peers (node_id, wg_ip, public_key, public_ip) VALUES (?, ?, ?, ?)`,
		nodeID, wgIP, "pk-"+wgIP, "198.51.100."+wgIP[len("10.0.0."):]); err != nil {
		t.Fatal(err)
	}
}

const liveSeen = "2026-10-04 00:00:00"

func TestAdmitted_rules(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		setup func(*sql.DB)
		reg   registration
		want  bool
	}{
		"genesis: empty registry": {func(*sql.DB) {}, registration{"12D3new", "10.0.0.1", "10.0.0.1"}, true},
		"registered node": {func(db *sql.DB) { addNode(t, db, "12D3old", "10.0.0.2", liveSeen) },
			registration{"12D3old", "10.0.0.2", "10.0.0.2"}, true},
		"joined node, peer row under its id": {func(db *sql.DB) {
			addNode(t, db, "12D3a", "10.0.0.1", liveSeen)
			addPeer(t, db, "12D3new", "10.0.0.5")
		}, registration{"12D3new", "10.0.0.5", "10.0.0.5"}, true},
		"made-up identity": {func(db *sql.DB) { addNode(t, db, "12D3a", "10.0.0.1", liveSeen) },
			registration{"12D3fake", "10.0.0.9", "10.0.0.1"}, false},
		"retired node does not bring itself back": {func(db *sql.DB) {
			addNode(t, db, "12D3a", "10.0.0.1", liveSeen)
			addNode(t, db, "12D3gone", "10.0.0.4", constants.RetiredNodeLastSeen)
		}, registration{"12D3gone", "10.0.0.4", "10.0.0.4"}, false},
		"OramaOS placeholder, from its own address": {func(db *sql.DB) {
			addNode(t, db, "12D3a", "10.0.0.1", liveSeen)
			addPeer(t, db, overlay.PlaceholderNodeID("10.0.0.6"), "10.0.0.6")
		}, registration{"12D3os", "10.0.0.6", "10.0.0.6"}, true},
		"OramaOS placeholder claimed from another address": {func(db *sql.DB) {
			addNode(t, db, "12D3a", "10.0.0.1", liveSeen)
			addPeer(t, db, overlay.PlaceholderNodeID("10.0.0.6"), "10.0.0.6")
		}, registration{"12D3fake", "10.0.0.6", "10.0.0.1"}, false},
		"OramaOS placeholder already held by a live node": {func(db *sql.DB) {
			addNode(t, db, "12D3a", "10.0.0.1", liveSeen)
			addPeer(t, db, overlay.PlaceholderNodeID("10.0.0.6"), "10.0.0.6")
			addNode(t, db, "12D3os", "10.0.0.6", liveSeen)
		}, registration{"12D3fake", "10.0.0.6", "10.0.0.6"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			client, db := registryDB(t)
			tc.setup(db)
			err := admitted(ctx, client, tc.reg)
			if tc.want && err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !tc.want && !errors.Is(err, errNotAdmitted) {
				t.Fatalf("admitted (err %v), want errNotAdmitted", err)
			}
		})
	}
}
