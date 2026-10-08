package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/tlsstore"
	_ "github.com/mattn/go-sqlite3"
)

const tlsStoreTestSecret = "cluster-secret-for-tls-store-tests"

func tlsStoreGateway(t *testing.T) *Gateway {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	migration, err := os.ReadFile("../../migrations/076_tls_store.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	log, err := logging.NewColoredLogger(logging.ComponentGateway, false)
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{logger: log, sqlDB: db, cfg: &Config{ClusterSecret: tlsStoreTestSecret, BaseDomain: "dbrs.space"}}
	g.tlsStoreReady.Store(true)
	return g
}

func tlsStoreKeys(t *testing.T, secret string) tlsstore.Keys {
	t.Helper()
	k, err := tlsstore.KeysFromClusterSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// tlsStoreCall signs req the way Caddy's storage module does and serves it.
func tlsStoreCall(t *testing.T, g *Gateway, macKey []byte, req tlsStoreRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/tls-store", bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:41000"
	if macKey != nil {
		if err := nodeauth.SignCoordination(macKey, r, time.Now(), tlsstore.MACAudience); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	g.tlsStoreHandler(w, r)
	return w
}

func decodeTLSStore(t *testing.T, w *httptest.ResponseRecorder) tlsStoreResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp tlsStoreResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestTLSStoreHandler_storeAndLoad(t *testing.T) {
	g := tlsStoreGateway(t)
	k := tlsStoreKeys(t, tlsStoreTestSecret)
	key := "certificates/acme-v02.api.letsencrypt.org-directory/wildcard_.dbrs.space/wildcard_.dbrs.space.key"
	sealed, err := tlsstore.Seal(k.Seal, key, []byte("PRIVATE KEY"))
	if err != nil {
		t.Fatal(err)
	}
	decodeTLSStore(t, tlsStoreCall(t, g, k.MAC, tlsStoreRequest{Op: "store", Key: key, Value: sealed}))

	resp := decodeTLSStore(t, tlsStoreCall(t, g, k.MAC, tlsStoreRequest{Op: "load", Key: key}))
	if resp.Exists == nil || !*resp.Exists || resp.Value != sealed {
		t.Fatalf("load = %+v, want the sealed value back", resp)
	}
	stat := decodeTLSStore(t, tlsStoreCall(t, g, k.MAC, tlsStoreRequest{Op: "stat", Key: key}))
	if stat.Exists == nil || !*stat.Exists || !stat.Terminal || stat.Size != int64(len(sealed)) {
		t.Fatalf("stat = %+v", stat)
	}
	list := decodeTLSStore(t, tlsStoreCall(t, g, k.MAC, tlsStoreRequest{Op: "list", Key: "certificates", Recursive: true}))
	if len(list.Keys) != 1 || list.Keys[0] != key {
		t.Fatalf("list = %v", list.Keys)
	}
}

// Absence is an answer with exists=false, never an error status: Caddy must
// be able to tell "no certificate yet" from "the store refused me".
func TestTLSStoreHandler_absentKeyIsAnAnswer(t *testing.T) {
	g := tlsStoreGateway(t)
	k := tlsStoreKeys(t, tlsStoreTestSecret)
	for _, op := range []string{"load", "stat"} {
		resp := decodeTLSStore(t, tlsStoreCall(t, g, k.MAC, tlsStoreRequest{Op: op, Key: "certificates/none"}))
		if resp.Exists == nil || *resp.Exists {
			t.Errorf("%s of a missing key = %+v, want exists=false", op, resp)
		}
	}
}

func TestTLSStoreHandler_refusesWhatIsNotCaddy(t *testing.T) {
	g := tlsStoreGateway(t)
	other := tlsStoreKeys(t, "another cluster")
	acme, _ := nodeauth.ACMEChallengeKey(tlsStoreTestSecret)
	for name, key := range map[string][]byte{"unsigned": nil, "another cluster's key": other.MAC, "the ACME key": acme} {
		if w := tlsStoreCall(t, g, key, tlsStoreRequest{Op: "load", Key: "a"}); w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", name, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/internal/tls-store", nil)
	w := httptest.NewRecorder()
	g.tlsStoreHandler(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET: status %d, want 404", w.Code)
	}
}

// A namespace gateway's database is its tenant's: it never serves the store.
func TestTLSStoreHandler_namespaceGatewayRefuses(t *testing.T) {
	g := tlsStoreGateway(t)
	g.cfg.RQLiteDSN, g.cfg.GlobalRQLiteDSN = "http://tenant:5001", "http://registry:5001"
	k := tlsStoreKeys(t, tlsStoreTestSecret)
	if w := tlsStoreCall(t, g, k.MAC, tlsStoreRequest{Op: "load", Key: "a"}); w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
}

func TestTLSStoreHandler_aReplayedCallIsRefused(t *testing.T) {
	g := tlsStoreGateway(t)
	k := tlsStoreKeys(t, tlsStoreTestSecret)
	body, _ := json.Marshal(tlsStoreRequest{Op: "delete", Key: "certificates/x"})
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/tls-store", bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:41000"
	if err := nodeauth.SignCoordination(k.MAC, r, time.Now(), tlsstore.MACAudience); err != nil {
		t.Fatal(err)
	}
	replay := httptest.NewRequest(http.MethodPost, "/v1/internal/tls-store", bytes.NewReader(body))
	replay.RemoteAddr = r.RemoteAddr
	replay.Header = r.Header.Clone()

	w := httptest.NewRecorder()
	g.tlsStoreHandler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("first call: status %d", w.Code)
	}
	w = httptest.NewRecorder()
	g.tlsStoreHandler(w, replay)
	if w.Code != http.StatusNotFound {
		t.Fatalf("replayed call: status %d, want 404", w.Code)
	}
}

func TestTLSStoreHandler_validatesRequests(t *testing.T) {
	g := tlsStoreGateway(t)
	k := tlsStoreKeys(t, tlsStoreTestSecret)
	holder := "0123456789abcdef0123456789abcdef"
	for name, req := range map[string]tlsStoreRequest{
		"unknown op":       {Op: "drop", Key: "a"},
		"traversal key":    {Op: "load", Key: "certificates/../secrets"},
		"empty key":        {Op: "load"},
		"plaintext value":  {Op: "store", Key: "a", Value: "-----BEGIN PRIVATE KEY-----"},
		"bad holder":       {Op: "lock", Key: "l", Holder: "me", LeaseMS: 60000},
		"no lease":         {Op: "lock", Key: "l", Holder: holder},
		"lease too long":   {Op: "renew", Key: "l", Holder: holder, LeaseMS: (3 * time.Hour).Milliseconds()},
		"unlock no holder": {Op: "unlock", Key: "l"},
	} {
		if w := tlsStoreCall(t, g, k.MAC, req); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, w.Code)
		}
	}
}

func TestTLSStoreHandler_locks(t *testing.T) {
	g := tlsStoreGateway(t)
	k := tlsStoreKeys(t, tlsStoreTestSecret)
	a, b := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	lock := func(holder string) bool {
		return decodeTLSStore(t, tlsStoreCall(t, g, k.MAC, tlsStoreRequest{Op: "lock", Key: "issue_cert_x", Holder: holder, LeaseMS: 60000})).Acquired
	}
	if !lock(a) {
		t.Fatal("a free lock was not acquired")
	}
	if lock(b) {
		t.Fatal("a held lock was acquired by another holder")
	}
	if w := tlsStoreCall(t, g, k.MAC, tlsStoreRequest{Op: "unlock", Key: "issue_cert_x", Holder: b}); w.Code != http.StatusConflict {
		t.Fatalf("unlock by another holder: status %d, want 409", w.Code)
	}
	decodeTLSStore(t, tlsStoreCall(t, g, k.MAC, tlsStoreRequest{Op: "renew", Key: "issue_cert_x", Holder: a, LeaseMS: 60000}))
	decodeTLSStore(t, tlsStoreCall(t, g, k.MAC, tlsStoreRequest{Op: "unlock", Key: "issue_cert_x", Holder: a}))
	if !lock(b) {
		t.Fatal("a released lock was not acquired")
	}
}

// A correctly stamped call from another node — over the overlay, or a stamp
// captured on one node and sent to another — is refused: only this host's
// Caddy calls the store.
func TestTLSStoreHandler_onlyFromLoopback(t *testing.T) {
	g := tlsStoreGateway(t)
	k := tlsStoreKeys(t, tlsStoreTestSecret)
	for _, remote := range []string{"10.0.0.2:41000", "203.0.113.7:41000", "[::ffff:10.0.0.2]:41000"} {
		body, _ := json.Marshal(tlsStoreRequest{Op: "load", Key: "a"})
		r := httptest.NewRequest(http.MethodPost, "/v1/internal/tls-store", bytes.NewReader(body))
		r.RemoteAddr = remote
		if err := nodeauth.SignCoordination(k.MAC, r, time.Now(), tlsstore.MACAudience); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		g.tlsStoreHandler(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("from %s: status %d, want 404", remote, w.Code)
		}
	}
}

// Until this node's on-disk certificates are imported the store answers
// nothing, so no Caddy finds it empty and orders certificates the cluster has.
func TestTLSStoreHandler_closedUntilImported(t *testing.T) {
	g := tlsStoreGateway(t)
	g.tlsStoreReady.Store(false)
	k := tlsStoreKeys(t, tlsStoreTestSecret)
	if w := tlsStoreCall(t, g, k.MAC, tlsStoreRequest{Op: "load", Key: "a"}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d before the import, want 503", w.Code)
	}
	// Unstamped callers learn nothing either way.
	if w := tlsStoreCall(t, g, nil, tlsStoreRequest{Op: "load", Key: "a"}); w.Code != http.StatusNotFound {
		t.Fatalf("unstamped: status %d, want 404", w.Code)
	}
}

// The import runs once per node: it records a marker and is skipped after.
func TestImportLegacyTLS_recordsTheImport(t *testing.T) {
	g := tlsStoreGateway(t)
	g.cfg.DataDir = t.TempDir()
	k := tlsStoreKeys(t, tlsStoreTestSecret)
	if !g.importLegacyTLS(context.Background(), k.Seal) {
		t.Fatal("the import did not complete")
	}
	marker := filepath.Join(g.cfg.DataDir, "data", "tls", legacyImportMarker)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("no import marker: %v", err)
	}
	if !g.importLegacyTLS(context.Background(), k.Seal) {
		t.Fatal("a recorded import was not taken as done")
	}
}
