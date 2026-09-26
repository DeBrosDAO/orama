package deployments

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"go.uber.org/zap"
)

func markerOf(t *testing.T, dir string) instanceOwner {
	t.Helper()
	owner, err := readOwnerMarker(dir)
	if err != nil {
		t.Fatalf("read marker in %s: %v", dir, err)
	}
	return owner
}

func wantTaken(t *testing.T, err error) *instanceTakenError {
	t.Helper()
	var taken *instanceTakenError
	if !errors.As(err, &taken) {
		t.Fatalf("got %v, want an instance conflict", err)
	}
	return taken
}

func TestClaimInstance_createsTheDirectoryAndMarksIt(t *testing.T) {
	svc := registryWith(t)
	base := filepath.Join(t.TempDir(), "deployments") // created on demand

	claim, err := svc.claimInstance(context.Background(), base, "acme", "web")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !claim.created || claim.dir != process.DeployDir(base, "acme", "web") {
		t.Fatalf("claim = %+v", claim)
	}
	if got := markerOf(t, claim.dir); got != (instanceOwner{Namespace: "acme", Name: "web"}) {
		t.Errorf("marker = %+v", got)
	}
	info, err := os.Stat(filepath.Join(claim.dir, ownerMarkerName))
	if err != nil || info.Mode().Perm() != ownerMarkerMode {
		t.Errorf("marker mode = %v (%v), want %v", info.Mode().Perm(), err, ownerMarkerMode)
	}
	if entries, _ := os.ReadDir(claim.dir); len(entries) != 1 {
		t.Errorf("the claim left temporary files behind: %v", entries)
	}
}

// The directory holding every tenant's files is the gateway's alone. At 0755
// any user on the host could read every deployment; the units that need one
// get their own directory bound into their mount namespace instead.
func TestClaim_createsTheDeploymentsDirectoryPrivate(t *testing.T) {
	svc := registryWith(t)
	claims := map[string]func(base string) error{
		"claimInstance": func(base string) error {
			_, err := svc.claimInstance(context.Background(), base, "acme", "web")
			return err
		},
		"claimNewInstance": func(base string) error {
			_, err := svc.claimNewInstance(context.Background(), base, "acme", "web")
			return err
		},
	}
	for name, claim := range claims {
		t.Run(name, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "deployments")
			if err := claim(base); err != nil {
				t.Fatalf("claim: %v", err)
			}
			info, err := os.Stat(base)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o700 {
				t.Errorf("new deployments directory mode = %v, want 0700", got)
			}
		})
		// A node before this release created it 0755; the next claim narrows it.
		t.Run(name+"/existing", func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "deployments")
			if err := os.Mkdir(base, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := claim(base); err != nil {
				t.Fatalf("claim: %v", err)
			}
			info, err := os.Stat(base)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o700 {
				t.Errorf("deployments directory mode = %v, want 0700", got)
			}
		})
	}
}

// The same deployment finding its own directory carries on: an update, a
// replica set up again, a create retried after a failed cleanup.
func TestClaimInstance_sameOwnerProceedsWithoutTakingOwnershipOfRemoval(t *testing.T) {
	svc := registryWith(t)
	base := t.TempDir()
	if _, err := svc.claimInstance(context.Background(), base, "acme", "web"); err != nil {
		t.Fatal(err)
	}

	again, err := svc.claimInstance(context.Background(), base, "acme", "web")
	if err != nil {
		t.Fatalf("second claim by the owner: %v", err)
	}
	if again.created {
		t.Fatal("a claim on an existing directory reported creating it")
	}
	if err := again.release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(again.dir); err != nil {
		t.Fatalf("releasing a claim that did not create the directory removed it: %v", err)
	}
}

// Namespace gateways have their own deployments tables. Gateway A's registry
// has a/b-c; gateway B's, on the same host, is empty, so the registry check
// passes there — and before the claim, B's a-b/c would have been extracted
// into a/b-c's directory and run as its unit.
func TestClaimInstance_collisionAcrossTwoGatewaysSharingADeploymentsDir(t *testing.T) {
	base := t.TempDir()
	gatewayA := registryWith(t)
	gatewayB := registryWith(t)
	ctx := context.Background()

	if err := gatewayA.CheckNewDeploymentName(ctx, "a", "b-c"); err != nil {
		t.Fatal(err)
	}
	if _, err := gatewayA.claimInstance(ctx, base, "a", "b-c"); err != nil {
		t.Fatalf("a/b-c: %v", err)
	}

	if err := gatewayB.CheckNewDeploymentName(ctx, "a-b", "c"); err != nil {
		t.Fatalf("gateway B's registry cannot see a/b-c, so its check must pass: %v", err)
	}
	_, err := gatewayB.claimInstance(ctx, base, "a-b", "c")
	taken := wantTaken(t, err)
	if taken.exists || taken.unowned || taken.instance != "a-b-c" {
		t.Errorf("got %+v, want a conflict with another deployment on a-b-c", taken)
	}
	if got := markerOf(t, process.DeployDir(base, "a", "b-c")); got.Namespace != "a" || got.Name != "b-c" {
		t.Errorf("the owner's marker changed: %+v", got)
	}
}

// Two gateways creating colliding deployments at the same moment: exactly one
// wins, and the directory is marked with the winner.
func TestClaimInstance_concurrentCollidingCreates(t *testing.T) {
	ctx := context.Background()
	gatewayA := registryWith(t)
	gatewayB := registryWith(t)
	pairs := []struct {
		svc      *DeploymentService
		ns, name string
	}{{gatewayA, "a", "b-c"}, {gatewayB, "a-b", "c"}}

	for round := 0; round < 50; round++ {
		base := t.TempDir()
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, len(pairs))
		for i, p := range pairs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = p.svc.claimInstance(ctx, base, p.ns, p.name)
			}()
		}
		close(start)
		wg.Wait()

		winners := 0
		var winner instanceOwner
		for i, err := range errs {
			if err == nil {
				winners++
				winner = instanceOwner{Namespace: pairs[i].ns, Name: pairs[i].name}
				continue
			}
			wantTaken(t, err)
		}
		if winners != 1 {
			t.Fatalf("round %d: %d claims succeeded (%v), want exactly one", round, winners, errs)
		}
		if got := markerOf(t, process.DeployDir(base, "a", "b-c")); got != winner {
			t.Fatalf("round %d: marker %+v, winner %+v", round, got, winner)
		}
	}
}

// A directory from before owner markers belongs to the deployment it is named
// after only if this gateway's registry has that deployment.
func TestClaimInstance_unmarkedDirectory(t *testing.T) {
	ctx := context.Background()
	unmarked := func(t *testing.T, base string) string {
		dir := process.DeployDir(base, "a", "b-c")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "app"), []byte("legacy"), 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("adopted by the registered deployment", func(t *testing.T) {
		base := t.TempDir()
		dir := unmarked(t, base)
		svc := registryWith(t, [2]string{"a", "b-c"})
		claim, err := svc.claimInstance(ctx, base, "a", "b-c")
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if claim.created {
			t.Error("adopting a legacy directory reported creating it")
		}
		if got := markerOf(t, dir); got != (instanceOwner{Namespace: "a", Name: "b-c"}) {
			t.Errorf("marker = %+v", got)
		}
	})

	t.Run("refused when no registry here has it", func(t *testing.T) {
		base := t.TempDir()
		dir := unmarked(t, base)
		_, err := registryWith(t).claimInstance(ctx, base, "a", "b-c")
		if taken := wantTaken(t, err); !taken.unowned {
			t.Errorf("got %+v, want an unowned-directory conflict", taken)
		}
		if _, err := readOwnerMarker(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("a refused claim marked the directory: %v", err)
		}
	})

	t.Run("refused for the other pair that maps to it", func(t *testing.T) {
		base := t.TempDir()
		unmarked(t, base)
		svc := registryWith(t, [2]string{"a", "b-c"})
		if taken := wantTaken(t, mustFail(svc.claimInstance(ctx, base, "a-b", "c"))); !taken.unowned {
			t.Errorf("got %+v, want an unowned-directory conflict", taken)
		}
	})

	t.Run("registry failure is not a verdict", func(t *testing.T) {
		base := t.TempDir()
		unmarked(t, base)
		svc := &DeploymentService{db: &mockRQLiteClient{QueryFunc: func(context.Context, interface{}, string, ...interface{}) error {
			return errors.New("leader not found")
		}}, logger: zap.NewNop()}
		_, err := svc.claimInstance(ctx, base, "a", "b-c")
		var taken *instanceTakenError
		if err == nil || errors.As(err, &taken) {
			t.Fatalf("got %v, want the registry error", err)
		}
	})
}

func mustFail(_ *instanceClaim, err error) error { return err }

// The marker is the gateway's. One that is a symlink or is not the gateway's
// JSON is refused rather than trusted.
func TestReadOwnerMarker_refusesWhatTheGatewayDidNotWrite(t *testing.T) {
	for name, plant := range map[string]func(dir string) error{
		"symlink": func(dir string) error {
			target := filepath.Join(dir, "elsewhere")
			if err := os.WriteFile(target, []byte(`{"namespace":"a","name":"b-c"}`), 0o644); err != nil {
				return err
			}
			return os.Symlink(target, filepath.Join(dir, ownerMarkerName))
		},
		"malformed": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, ownerMarkerName), []byte("a/b-c"), 0o644)
		},
		"missing fields": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, ownerMarkerName), []byte(`{"namespace":"a"}`), 0o644)
		},
		"oversized": func(dir string) error {
			big, _ := json.Marshal(instanceOwner{Namespace: "a", Name: strings.Repeat("x", ownerMarkerMaxBytes)})
			return os.WriteFile(filepath.Join(dir, ownerMarkerName), big, 0o644)
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := plant(dir); err != nil {
				t.Fatal(err)
			}
			if owner, err := readOwnerMarker(dir); err == nil || errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("got %+v, %v; want the marker refused", owner, err)
			}
		})
	}
}

func TestInstanceClaim_release(t *testing.T) {
	svc := registryWith(t)
	base := t.TempDir()
	claim, err := svc.claimInstance(context.Background(), base, "acme", "web")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claim.dir, "extracted"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := claim.release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(claim.dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the claimed directory survived its release: %v", err)
	}
	// Released, the name is free for anyone.
	if _, err := registryWith(t).claimInstance(context.Background(), base, "acme-web", "x"); err != nil {
		t.Fatal(err)
	}
	var none *instanceClaim
	if err := none.release(); err != nil {
		t.Errorf("releasing no claim: %v", err)
	}
}

// A staged directory replaces the claimed one on update, so its marker is
// replaced, not refused.
func TestWriteOwnerMarker_exclusiveAndReplacing(t *testing.T) {
	dir := t.TempDir()
	if err := writeOwnerMarker(dir, "a", "b-c", true); err != nil {
		t.Fatal(err)
	}
	if err := writeOwnerMarker(dir, "a-b", "c", true); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("an exclusive write over a marker: %v, want fs.ErrExist", err)
	}
	if got := markerOf(t, dir); got.Namespace != "a" {
		t.Fatalf("a refused exclusive write changed the marker: %+v", got)
	}
	if err := writeOwnerMarker(dir, "a", "b-c-2", false); err != nil {
		t.Fatal(err)
	}
	if got := markerOf(t, dir); got.Name != "b-c-2" {
		t.Fatalf("replacing write: %+v", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
	if err := writeOwnerMarker(filepath.Join(dir, "missing"), "a", "b", true); err == nil {
		t.Error("a marker was written into a directory that does not exist")
	}
}

func TestOwnsInstanceDir(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	svc := registryWith(t)
	if _, err := svc.claimInstance(ctx, base, "a", "b-c"); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		ns, n string
		want  bool
	}{
		"its own":            {"a", "b-c", true},
		"another deployment": {"a-b", "c", false},
		"no directory":       {"acme", "web", true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := svc.ownsInstanceDir(ctx, process.DeployDir(base, tc.ns, tc.n), tc.ns, tc.n)
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// Deleting a/b-c must not stop or delete a-b/c, which holds the instance on
// this host. The process manager is nil: reaching Stop would panic.
func TestRemoveLocalInstance_leavesAnotherDeploymentsInstanceAlone(t *testing.T) {
	base := t.TempDir()
	owner := registryWith(t)
	if _, err := owner.claimInstance(context.Background(), base, "a-b", "c"); err != nil {
		t.Fatal(err)
	}
	h := NewListHandler(registryWith(t, [2]string{"a", "b-c"}), nil, nil, zap.NewNop(), base)
	dep := &deployments.Deployment{Namespace: "a", Name: "b-c"}

	if err := h.removeLocalInstance(context.Background(), dep); err != nil {
		t.Fatalf("removeLocalInstance: %v", err)
	}
	if got := markerOf(t, process.DeployDir(base, "a-b", "c")); got.Namespace != "a-b" {
		t.Fatalf("the other deployment's directory changed: %+v", got)
	}
}

// A create that fails before its registry row exists gives the instance back.
func TestGoHandler_failedCreateReleasesTheClaim(t *testing.T) {
	base := t.TempDir()
	ipfsClient := &mockIPFSClient{AddFunc: func(context.Context, io.Reader, string) (*ipfs.AddResponse, error) {
		return nil, errors.New("ipfs down")
	}}
	h := NewGoHandler(registryWith(t), nil, ipfsClient, zap.NewNop(), base)

	rr := httptest.NewRecorder()
	h.HandleUpload(rr, goUpload(t, "acme", "web"))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%s), want 500", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(process.DeployDir(base, "acme", "web")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a failed create kept its claim: %v", err)
	}
}

// The registry check cannot see a deployment made through another gateway;
// the host claim can, and it refuses before anything is uploaded.
func TestGoHandler_instanceHeldByAnotherGatewayIs409(t *testing.T) {
	base := t.TempDir()
	if _, err := registryWith(t).claimInstance(context.Background(), base, "a-b", "c"); err != nil {
		t.Fatal(err)
	}
	ipfsClient := &mockIPFSClient{AddFunc: func(context.Context, io.Reader, string) (*ipfs.AddResponse, error) {
		t.Error("a refused deployment was uploaded to IPFS")
		return &ipfs.AddResponse{Cid: "QmX"}, nil
	}}
	h := NewGoHandler(registryWith(t), nil, ipfsClient, zap.NewNop(), base)

	rr := httptest.NewRecorder()
	h.HandleUpload(rr, goUpload(t, "a", "b-c"))

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d (%s), want 409", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"a-b-c"`) {
		t.Errorf("the conflict does not name the instance: %s", rr.Body.String())
	}
	if got := markerOf(t, process.DeployDir(base, "a-b", "c")); got.Namespace != "a-b" {
		t.Errorf("the owner's marker changed: %+v", got)
	}
}

func goUpload(t *testing.T, namespace, name string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("name", name)
	part, err := w.CreateFormFile("tarball", "app.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("archive"))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/deployments/go/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, namespace))
}

// An archive cannot write the owner marker: extraction skips it at any depth,
// so a tenant cannot hand its directory to — or take it from — someone else.
func TestTarExtractArgs_neverExtractsTheOwnerMarker(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar is not installed")
	}
	archive := filepath.Join(t.TempDir(), "app.tar.gz")
	writeTarGz(t, archive, map[string]string{
		"./" + ownerMarkerName:   `{"namespace":"victim","name":"x"}`,
		ownerMarkerName:          `{"namespace":"victim","name":"x"}`,
		"sub/" + ownerMarkerName: "x",
		"./app":                  "binary",
	})
	dest := t.TempDir()
	if err := writeOwnerMarker(dest, "acme", "web", true); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("tar", tarExtractArgs(archive, dest)...).CombinedOutput(); err != nil {
		t.Fatalf("tar: %v: %s", err, out)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "app")); err != nil || string(got) != "binary" {
		t.Fatalf("the archive's content was not extracted: %q, %v", got, err)
	}
	if got := markerOf(t, dest); got != (instanceOwner{Namespace: "acme", Name: "web"}) {
		t.Errorf("the archive rewrote the owner: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "sub", ownerMarkerName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a nested marker was extracted: %v", err)
	}
}

func writeTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

// An update replaces the directory on this host; when another deployment
// holds it, the update is refused before anything is staged.
func TestUpdateHandler_instanceHeldByAnotherDeploymentIs409(t *testing.T) {
	base := t.TempDir()
	if _, err := registryWith(t).claimInstance(context.Background(), base, "a-b", "c"); err != nil {
		t.Fatal(err)
	}
	svc := registryWith(t)
	if _, err := svc.db.Exec(context.Background(),
		`INSERT INTO deployments (id, namespace, name, type, content_cid, build_cid, home_node_id, port, subdomain, environment, deployed_by)
		 VALUES ('d1', 'a', 'b-c', 'go-backend', '', '', '', 0, '', '', 'test')`); err != nil {
		t.Fatal(err)
	}
	ipfsClient := &mockIPFSClient{AddFunc: func(context.Context, io.Reader, string) (*ipfs.AddResponse, error) {
		return &ipfs.AddResponse{Cid: "QmNew"}, nil
	}}
	nextjs := NewNextJSHandler(svc, nil, ipfsClient, zap.NewNop(), base)
	h := NewUpdateHandler(svc, nil, nextjs, nil, zap.NewNop())

	req := goUpload(t, "a", "b-c")
	rr := httptest.NewRecorder()
	h.HandleUpdate(rr, req)

	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"a-b-c"`) {
		t.Fatalf("status = %d (%s), want 409 naming the instance", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(process.DeployDir(base, "a", "b-c") + ".new"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused update staged files: %v", err)
	}
	if got := markerOf(t, process.DeployDir(base, "a-b", "c")); got.Namespace != "a-b" {
		t.Errorf("the owner's marker changed: %+v", got)
	}
}

// Two creates of the same deployment at once both passed the owner check and
// extracted into one directory; only the registry insert noticed, after the
// files were already mixed. A create must be the request that made it.
func TestClaimNewInstance_concurrentCreatesOfOneDeployment(t *testing.T) {
	ctx := context.Background()
	svc := registryWith(t)
	for round := 0; round < 50; round++ {
		base := t.TempDir()
		const creators = 4
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, creators)
		for i := 0; i < creators; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = svc.claimNewInstance(ctx, base, "acme", "web")
			}()
		}
		close(start)
		wg.Wait()

		winners := 0
		for _, err := range errs {
			switch {
			case err == nil:
				winners++
			case !errors.Is(err, errInstanceNotNew):
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}
		if winners != 1 {
			t.Fatalf("round %d: %d creates claimed the instance, want exactly one", round, winners)
		}
	}
}

func TestWriteDeploymentNameError_existingInstanceIs409(t *testing.T) {
	rec := httptest.NewRecorder()
	writeDeploymentNameError(rec, zap.NewNop(), errInstanceNotNew)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}
