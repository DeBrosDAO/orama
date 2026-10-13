package deployments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

// siteArchive is a .tar.gz of one index.html.
func siteArchive(t *testing.T, html string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "site.tar.gz")
	writeTarGz(t, path, map[string]string{"index.html": html})
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func siteUpdateRequest(t *testing.T, namespace, name string, archive []byte) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("name", name)
	part, err := w.CreateFormFile("tarball", "site.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(archive)
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/deployments/static/update", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, namespace))
}

// siteIPFS records what was added: a directory holding site/index.html is a
// site; a file added whole (the tarball) is not.
func siteIPFS(t *testing.T, sawIndex *string, addedFile *bool) *mockIPFSClient {
	return &mockIPFSClient{
		AddDirectoryFunc: func(_ context.Context, dir string) (*ipfs.AddResponse, error) {
			b, err := os.ReadFile(filepath.Join(dir, "site", "index.html"))
			if err != nil {
				t.Errorf("the directory added has no site/index.html: %v", err)
			}
			*sawIndex = string(b)
			return &ipfs.AddResponse{Cid: "QmSiteDir"}, nil
		},
		AddFunc: func(context.Context, io.Reader, string) (*ipfs.AddResponse, error) {
			*addedFile = true
			return &ipfs.AddResponse{Cid: "QmTarballFile"}, nil
		},
	}
}

// An update used to add the tarball itself, so the new content CID was a file
// and no path under it resolved: the site stopped serving (stagenet e2e audit,
// 2026-09-30). It must store the extracted directory, as create does, and say
// where the site is.
func TestUpdateHandler_staticUpdateStoresTheExtractedSite(t *testing.T) {
	svc := registryWith(t)
	svc.baseDomain = "example.test"
	if _, err := svc.db.Exec(context.Background(),
		`INSERT INTO deployments (id, namespace, name, type, version, content_cid, build_cid, home_node_id, port, subdomain, environment, deployed_by)
		 VALUES ('d1', 'acme', 'site', 'static', 1, 'QmOld', '', '', 0, 'site-abc123', '', 'test')`); err != nil {
		t.Fatal(err)
	}
	var sawIndex string
	addedFile := false
	static := NewStaticDeploymentHandler(svc, siteIPFS(t, &sawIndex, &addedFile), zap.NewNop())
	h := NewUpdateHandler(svc, static, nil, nil, zap.NewNop())

	rr := httptest.NewRecorder()
	h.HandleUpdate(rr, siteUpdateRequest(t, "acme", "site", siteArchive(t, "v2")))

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	if addedFile || sawIndex != "v2" {
		t.Fatalf("the update stored the tarball as a file (%v) or not the new site (%q)", addedFile, sawIndex)
	}
	var resp struct {
		ContentCID string   `json:"content_cid"`
		Version    int      `json:"version"`
		URLs       []string `json:"urls"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ContentCID != "QmSiteDir" || resp.Version != 2 {
		t.Errorf("answered cid %q version %d, want QmSiteDir 2", resp.ContentCID, resp.Version)
	}
	if len(resp.URLs) == 0 {
		t.Error("the update did not say where the site is")
	}
}

func TestUploadSite_badArchiveIsAnError(t *testing.T) {
	var sawIndex string
	addedFile := false
	_, err := uploadSite(context.Background(), siteIPFS(t, &sawIndex, &addedFile), bytes.NewReader([]byte("not a tarball")))
	if err == nil {
		t.Fatal("a body that is not a tarball was stored")
	}
}

// Static sites claim no instance directory, so two creates of one name both
// passed the name check and the loser's insert failed on UNIQUE: a 500, and
// the subdomain it had registered was left behind for nobody.
func TestCreateDeployment_lostInsertRaceIsTakenAndReleasesTheSubdomain(t *testing.T) {
	svc := registryWith(t, [2]string{"acme", "site"})
	svc.nodePeerID = "peer-1"
	d := &deployments.Deployment{ID: "d2", Namespace: "acme", Name: "site", Type: deployments.DeploymentTypeStatic,
		Version: 1, Status: deployments.DeploymentStatusActive, Environment: map[string]string{}, DeployedBy: "acme"}

	err := svc.CreateDeployment(context.Background(), d)

	var taken *instanceTakenError
	if !errors.As(err, &taken) || !taken.exists {
		t.Fatalf("err %v, want the deployment-exists conflict", err)
	}
	var rows []struct {
		N int `db:"n"`
	}
	if err := svc.db.Query(context.Background(), &rows,
		`SELECT COUNT(*) AS n FROM global_deployment_subdomains WHERE deployment_id = 'd2'`); err != nil {
		t.Fatal(err)
	}
	if rows[0].N != 0 {
		t.Errorf("%d subdomain rows left for the deployment that was not created", rows[0].N)
	}
}

func TestIsDeploymentNameConflict(t *testing.T) {
	for msg, want := range map[string]bool{
		"UNIQUE constraint failed: deployments.namespace, deployments.name": true,
		"UNIQUE constraint failed: deployments.subdomain":                   false,
		"database is locked": false,
	} {
		if got := isDeploymentNameConflict(errors.New(msg)); got != want {
			t.Errorf("%q: %v, want %v", msg, got, want)
		}
	}
}
