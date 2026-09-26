package deployments

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// registryWith is a migrated deployments registry holding the given
// (namespace, name) rows, and a service reading it. The collision check is one
// SQL expression, so it runs against the real schema rather than a fake that
// would decide the answer itself.
func registryWith(t *testing.T, rows ...[2]string) *DeploymentService {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	for i, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO deployments (id, namespace, name, type, deployed_by) VALUES (?, ?, ?, 'go-backend', 'test')`,
			string(rune('a'+i)), r[0], r[1]); err != nil {
			t.Fatalf("insert %v: %v", r, err)
		}
	}
	return &DeploymentService{db: rqlite.NewClient(db), logger: zap.NewNop(), envCodec: testEnvCodec()}
}

// Namespace "a" + name "b-c" and namespace "a-b" + name "c" are one unit,
// one directory and one set of staged secrets. The second must be refused.
func TestCheckNewDeploymentName_refusesAnotherNamespacesInstance(t *testing.T) {
	svc := registryWith(t, [2]string{"a-b", "c"})

	err := svc.CheckNewDeploymentName(context.Background(), "a", "b-c")
	var taken *instanceTakenError
	if !errors.As(err, &taken) {
		t.Fatalf("a/b-c collides with a-b/c on instance a-b-c, got %v", err)
	}
	if taken.exists || taken.instance != "a-b-c" {
		t.Errorf("got %+v, want a conflict on a-b-c with another deployment", taken)
	}
	if strings.Contains(err.Error(), `"a-b"`) || strings.Contains(err.Error(), "namespace a-b") {
		t.Errorf("the error names the other tenant's namespace: %v", err)
	}
}

// A dotted legacy name maps like InstanceName does: "my.app" is "my-app".
func TestCheckNewDeploymentName_matchesDottedLegacyNames(t *testing.T) {
	svc := registryWith(t, [2]string{"acme", "my.app"})

	var taken *instanceTakenError
	if err := svc.CheckNewDeploymentName(context.Background(), "acme", "my-app"); !errors.As(err, &taken) {
		t.Fatalf("acme/my-app is the same instance as acme/my.app, got %v", err)
	}
}

// Creating a deployment that already exists would extract over its files
// before UNIQUE(namespace, name) stopped the insert.
func TestCheckNewDeploymentName_existingDeploymentIsAnUpdate(t *testing.T) {
	svc := registryWith(t, [2]string{"acme", "web"})

	err := svc.CheckNewDeploymentName(context.Background(), "acme", "web")
	var taken *instanceTakenError
	if !errors.As(err, &taken) || !taken.exists {
		t.Fatalf("got %v, want the deployment reported as existing", err)
	}
	if !strings.Contains(err.Error(), "--update") {
		t.Errorf("the error does not say how to update: %v", err)
	}
}

func TestCheckNewDeploymentName_freeInstance(t *testing.T) {
	svc := registryWith(t, [2]string{"a-b", "c"}, [2]string{"acme", "web"})

	for _, c := range [][2]string{{"a", "b"}, {"a-b", "c-d"}, {"acme", "web2"}, {"other", "web"}} {
		if err := svc.CheckNewDeploymentName(context.Background(), c[0], c[1]); err != nil {
			t.Errorf("%s/%s: %v", c[0], c[1], err)
		}
	}
}

// An empty registry has nothing to collide with.
func TestCheckNewDeploymentName_emptyRegistry(t *testing.T) {
	if err := registryWith(t).CheckNewDeploymentName(context.Background(), "a", "b-c"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckNewDeploymentName_invalidName(t *testing.T) {
	svc := registryWith(t)
	for _, name := range []string{"", "my.app", "-x", strings.Repeat("a", process.MaxNameLength+1)} {
		if err := svc.CheckNewDeploymentName(context.Background(), "acme", name); !errors.Is(err, errInvalidDeploymentName) {
			t.Errorf("%q: got %v, want an invalid-name error", name, err)
		}
	}
}

// A registry that cannot be read is neither "free" nor "taken".
func TestCheckNewDeploymentName_registryFailure(t *testing.T) {
	svc := &DeploymentService{
		db: &mockRQLiteClient{QueryFunc: func(context.Context, interface{}, string, ...interface{}) error {
			return errors.New("leader not found")
		}},
		logger: zap.NewNop(),
	}
	err := svc.CheckNewDeploymentName(context.Background(), "acme", "web")
	var taken *instanceTakenError
	if err == nil || errors.Is(err, errInvalidDeploymentName) || errors.As(err, &taken) {
		t.Fatalf("got %v, want the registry error", err)
	}
	rr := httptest.NewRecorder()
	writeDeploymentNameError(rr, zap.NewNop(), err)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

// The subdomain is <name>-<suffix>, one DNS label.
func TestMaxNameLength_fitsOneDNSLabel(t *testing.T) {
	const maxDNSLabel = 63
	if n := process.MaxNameLength + 1 + subdomainSuffixLength; n > maxDNSLabel {
		t.Fatalf("the longest name makes a %d-byte subdomain label; the limit is %d", n, maxDNSLabel)
	}
}

// The check runs before anything is written: a refused upload must not reach
// IPFS or touch the directory the owner runs from.
func TestGoHandler_conflictingNameIsRefusedBeforeExtracting(t *testing.T) {
	svc := registryWith(t, [2]string{"a-b", "c"})
	base := t.TempDir()
	ownerDir := process.DeployDir(base, "a-b", "c")
	if err := os.MkdirAll(ownerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownerDir, "app"), []byte("owner"), 0o755); err != nil {
		t.Fatal(err)
	}
	ipfsClient := &mockIPFSClient{AddFunc: func(context.Context, io.Reader, string) (*ipfs.AddResponse, error) {
		t.Error("a refused deployment was uploaded to IPFS")
		return &ipfs.AddResponse{Cid: "QmX"}, nil
	}}
	h := NewGoHandler(svc, nil, ipfsClient, zap.NewNop(), base)

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("name", "b-c")
	part, _ := w.CreateFormFile("tarball", "app.tar.gz")
	_, _ = part.Write([]byte("not read"))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/deployments/go/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "a"))
	rr := httptest.NewRecorder()

	h.HandleUpload(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d (%s), want 409", rr.Code, rr.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(ownerDir, "app")); string(got) != "owner" {
		t.Errorf("the owner's binary was touched: %q", got)
	}
}

func TestUpdateHandler_invalidNameIs400(t *testing.T) {
	h := NewUpdateHandler(&DeploymentService{db: &mockRQLiteClient{}, logger: zap.NewNop()}, nil, nil, nil, zap.NewNop())
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("name", "../etc")
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/deployments/go/update", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "acme"))
	rr := httptest.NewRecorder()

	h.HandleUpdate(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (%s), want 400", rr.Code, rr.Body.String())
	}
}

// A deployment that predates ValidateName keeps being updatable: a dotted
// legacy name maps to a valid instance. Only a pair that could never start is
// refused.
func TestUpdateHandler_legacyDottedNameIsNotRefused(t *testing.T) {
	db := &mockRQLiteClient{}
	h := NewUpdateHandler(&DeploymentService{db: db, logger: zap.NewNop()}, nil, nil, nil, zap.NewNop())
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("name", "my.app")
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/deployments/go/update", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "acme"))
	rr := httptest.NewRecorder()

	h.HandleUpdate(rr, req)

	if rr.Code == http.StatusBadRequest {
		t.Fatalf("a legacy dotted name was refused: %s", rr.Body.String())
	}
}

// Replica requests carry the name from the primary; one that would put a path
// separator into DeployDir is refused before anything touches the disk.
func TestReplicaHandler_invalidInstanceIs400(t *testing.T) {
	base := t.TempDir()
	h := NewReplicaHandler(&DeploymentService{db: &mockRQLiteClient{}, logger: zap.NewNop()}, nil, nil, zap.NewNop(), base)
	for _, handle := range []func(http.ResponseWriter, *http.Request){h.HandleSetup, h.HandleUpdate, h.HandleTeardown} {
		req := httptest.NewRequest(http.MethodPost, "/v1/internal/deployments/replica/x",
			strings.NewReader(`{"deployment_id":"d1","namespace":"acme","name":"web/../../x"}`))
		req.Header.Set("X-Orama-Internal-Auth", "replica-coordination")
		req.RemoteAddr = "10.0.0.2:4000"
		rr := httptest.NewRecorder()

		handle(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d (%s), want 400", rr.Code, rr.Body.String())
		}
	}
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Errorf("a refused replica request wrote %v", entries)
	}
}

// With a legacy collision already in the table, a second create of one of the
// colliding deployments is still reported as "exists — update it".
func TestCheckNewDeploymentName_ownRowWinsOverALegacyCollision(t *testing.T) {
	svc := registryWith(t, [2]string{"a-b", "c"}, [2]string{"a", "b-c"})

	for _, c := range [][2]string{{"a", "b-c"}, {"a-b", "c"}} {
		var taken *instanceTakenError
		if err := svc.CheckNewDeploymentName(context.Background(), c[0], c[1]); !errors.As(err, &taken) || !taken.exists {
			t.Errorf("%s/%s: got %v, want it reported as existing", c[0], c[1], err)
		}
	}
}
