//go:build e2e_fleet

package docsexamples

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/pace"
)

// exampleDone is the last line each SDK example prints when every step
// worked. The examples end in `main().catch(console.error)`, which exits 0
// after a failure too, so the line, not the exit code, says it succeeded.
var exampleDone = map[string]string{
	"basic-usage.ts":   "--- Example completed successfully ---",
	"database-crud.ts": "--- CRUD operations completed successfully ---",
	"pubsub-chat.ts":   "--- Chat example completed ---",
}

// TestDocsExamples_sdkExamplesRun runs each program in sdk/examples the way
// its header says (GATEWAY_BASE_URL and ORAMA_API_KEY for a real namespace,
// `pnpm example`/tsx) against a fresh namespace with an admin key, and
// requires its completion line and no error output.
func TestDocsExamples_sdkExamplesRun(t *testing.T) {
	t.Parallel()
	sdk := requireNode(t)
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	key := tenancy.APIKey(t, n, "admin")
	if err := n.Client.Protect(key); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "LANG=C.UTF-8", "CI=1",
		"NODE_EXTRA_CA_CERTS=" + f.State.CAFile, "GATEWAY_BASE_URL=" + n.URL, "ORAMA_API_KEY=" + key}
	for _, file := range sdkExamples(t, sdk) {
		name := filepath.Base(file)
		t.Run(strings.TrimSuffix(name, ".ts"), func(t *testing.T) {
			done, ok := exampleDone[name]
			if !ok {
				t.Fatalf("sdk/examples/%s has no expected completion line here: add it to exampleDone", name)
			}
			chargeKeyExchange(t)
			out, err := runTool(t, sdk, env, "pnpm", "--dir", sdk, "exec", "tsx", file)
			out = f.Redact(out)
			if err != nil || !strings.Contains(out, done) {
				t.Errorf("sdk/examples/%s did not complete (%v):\n%s", name, err, out)
			}
		})
	}
}

// chargeKeyExchange draws the one credential token an example's API-key
// exchange (/v1/auth/token) spends: the SDK's request is not paced itself.
func chargeKeyExchange(t testing.TB) {
	t.Helper()
	p, err := pace.FromEnv(os.LookupEnv)
	if err != nil {
		t.Fatalf("the run's pacer: %v", err)
	}
	if err := p.Wait(t.Context(), pace.BucketCred); err != nil {
		t.Fatalf("pacing the key exchange: %v", err)
	}
}
