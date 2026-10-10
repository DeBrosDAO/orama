//go:build e2e_fleet

package relayedfetch

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	fixtureDir   = "testdata/fetchcapfn"
	fixtureFn    = "e2e-fetchcap"
	fixturePerm  = 0o644
	deployBudget = 2 * 60 * 1000 * 1000 * 1000 // 2 minutes
)

// TestStorageFetchCapMint_hostFunctionMintsForADeviceBoundCaller: a deployed
// function mints capabilities with storage_fetch_cap_mint when its caller's
// session is bound to a device, and they download the object; a caller with no
// device gets nothing (website/src/docs/developer/functions.mdx#storage-fetch-capabilities).
func TestStorageFetchCapMint_hostFunctionMintsForADeviceBoundCaller(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tinygo"); err != nil {
		harness.SkipNotApplicable(t, "tinygo is not on the runner's PATH; `orama function deploy` builds functions with it")
	}
	fx := setupWithCLI(t)
	deployFixture(t, fx)

	invoke := func(bearer string, body string) *gw.Response {
		return fx.c.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/functions/" + fixtureFn + "/invoke",
			Query: url.Values{"namespace": {fx.n.Name}}, Bearer: bearer,
			Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(body)})
	}
	req := `{"cid":"` + fx.cid + `","count":2,"ttl":7200}`

	var out mintBody
	if err := invoke(fx.token(), req).Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Namespace != fx.n.Name || out.CID != fx.cid || len(out.Caps) != 2 {
		t.Fatalf("the function minted %+v", out)
	}
	for _, c := range out.Caps {
		protect(t, fx.c, c.Token)
		got := fx.direct(t, fx.cid, c.Token, nil).Expect(t, http.StatusOK)
		if len(got.Body) != len(fx.content) {
			t.Errorf("a function-minted capability downloaded %d bytes, want %d", len(got.Body), len(fx.content))
		}
	}

	var refused struct {
		Error string `json:"error"`
		Caps  []any  `json:"caps"`
	}
	if err := invoke(fx.token(), `{"cid":"`+fx.cid+`","count":0,"ttl":7200}`).Decode(&refused); err != nil {
		t.Fatal(err)
	}
	if refused.Error == "" || len(refused.Caps) != 0 {
		t.Errorf("a count of zero was minted: %+v", refused)
	}
}

// deployFixture deploys the fixture function as the namespace operator's
// CLI would and deletes it at the end.
func deployFixture(t *testing.T, fx *fixture) {
	t.Helper()
	dir := t.TempDir()
	for _, file := range []string{"function.go", "go.mod"} {
		src, err := os.ReadFile(filepath.Join(fixtureDir, file))
		if err != nil {
			t.Fatalf("fixture %s: %v", file, err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), src, fixturePerm); err != nil {
			t.Fatal(err)
		}
	}
	yaml := "name: " + fixtureFn + "\npublic: false\nmemory: 64\ntimeout: 30\n"
	if err := os.WriteFile(filepath.Join(dir, "function.yaml"), []byte(yaml), fixturePerm); err != nil {
		t.Fatal(err)
	}
	fx.n.CLI.MustOK(t, "function", "deploy", dir)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), deployBudget)
		defer cancel()
		if res, err := fx.n.CLI.Run(ctx, "function", "delete", fixtureFn, "--force"); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: failed to delete function %s: %v %s", fixtureFn, err, res.Stderr)
		}
	})
}
