//go:build e2e_fleet

package sdkts

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/pace"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	// vitestBudget bounds the whole TypeScript run.
	vitestBudget = 20 * time.Minute
	// featureTestsDir holds this feature's TypeScript tests.
	featureTestsDir = "e2e/features/sdk-ts/tests"
	// The TypeScript tests' credential calls are not paced by the harness
	// (they are the SDK's own fetches), so their tokens are drawn up front:
	// verify, refresh and a replayed verify on the public gateway; refresh,
	// the device link start and claim on the namespace gateway.
	mainCredentialCalls = 4
	nsCredentialCalls   = 4
)

// tools finds node and pnpm; without them the suite cannot run here.
func tools(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		harness.SkipNotApplicable(t, "node is not installed on the runner: install Node 20+ to run the TypeScript SDK suite")
	}
	pnpm, err := exec.LookPath("pnpm")
	if err != nil {
		harness.SkipNotApplicable(t, "pnpm is not installed on the runner: install pnpm to run the TypeScript SDK suite")
	}
	return pnpm
}

// TestSDKTypeScript_suiteAgainstFleet runs the TypeScript SDK's e2e suite
// (sdk/tests/e2e) and this feature's additions (auth sessions, device link,
// storage getBinary, db Repository and dropTable, functions.invoke, the
// chain read proxy, key profiles, the workload client, WebSocket revocation)
// through vitest against a fresh namespace, and reports each vitest test as
// a Go subtest.
func TestSDKTypeScript_suiteAgainstFleet(t *testing.T) {
	t.Parallel()
	pnpm := tools(t)
	root := cliconf.RepoRoot(t)
	sdkDir := filepath.Join(root, "sdk")
	if _, err := os.Stat(filepath.Join(sdkDir, "node_modules", "vitest")); err != nil {
		harness.SkipNotApplicable(t, "sdk/node_modules has no vitest: run `pnpm install --frozen-lockfile` in sdk/ before the run")
	}
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	env := suiteEnv(t, f, n)
	preCharge(t, f, n)
	dir := t.TempDir()
	cfg := writeVitestConfig(t, dir, sdkDir, filepath.Join(root, featureTestsDir))
	report := filepath.Join(dir, "vitest-report.json")
	ctx, cancel := context.WithTimeout(t.Context(), vitestBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, pnpm, "--dir", sdkDir, "exec", "vitest", "run",
		"--config", cfg, "--reporter=json", "--outputFile="+report)
	cmd.Env, cmd.Dir = env, dir
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("failed to run vitest: %v", err)
	}
	if _, statErr := os.Stat(report); statErr != nil {
		t.Fatalf("vitest exited without a report (%v):\n%s", err, f.Redact(string(out)))
	}
	replay(t, readReport(t, report), f.Redact)
}

// suiteEnv is the whole environment of the vitest process: nothing of the
// runner's leaks into it but PATH and the locale.
func suiteEnv(t *testing.T, f *fleet.Fleet, n *ns.Namespace) []string {
	t.Helper()
	key := tenancy.APIKey(t, n, "app-runtime")
	if err := n.Client.Protect(key); err != nil {
		t.Fatal(err)
	}
	member, wsUser := tenancy.Member(t, n, "admin"), tenancy.Member(t, n, "admin")
	w, msg, sig := lobbySignIn(t)
	env := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "LANG=C.UTF-8", "CI=1",
		"NODE_EXTRA_CA_CERTS=" + f.State.CAFile, "E2E_FLEET=1",
		"GATEWAY_BASE_URL=" + n.URL, "GATEWAY_API_KEY=" + key, "GATEWAY_JWT=" + n.Owner.Token(),
		"E2E_MAIN_GATEWAY_URL=" + f.State.GatewayURL, "E2E_NAMESPACE=" + n.Name,
		"E2E_SIWE_WALLET=" + w, "E2E_SIWE_MESSAGE=" + msg, "E2E_SIWE_SIGNATURE=" + sig,
		"E2E_MEMBER_REFRESH=" + member.Session.RefreshToken,
		"E2E_WS_JWT=" + wsUser.Token(), "E2E_WS_REFRESH=" + wsUser.Session.RefreshToken,
	}
	if f.State.ChainID != "" {
		env = append(env, "E2E_CHAIN=1")
	}
	return env
}

// lobbySignIn asks the public gateway for a lobby challenge for a fresh
// wallet and signs it, so the TypeScript side can call verify with a real
// signature (it has no secp256k1/keccak of its own). The TypeScript test
// logs the session out.
func lobbySignIn(t *testing.T) (address, message, signature string) {
	t.Helper()
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	ch, _, err := harness.GW(t).Challenge(t.Context(), gw.ChallengeRequest{Wallet: w.Address()})
	if err != nil {
		t.Fatalf("lobby challenge: %v", err)
	}
	sig, err := w.Sign(ch.Message)
	if err != nil {
		t.Fatal(err)
	}
	return w.Address(), ch.Message, sig
}

// preCharge draws the TypeScript tests' credential tokens from the run's
// buckets before they run (see mainCredentialCalls).
func preCharge(t *testing.T, f *fleet.Fleet, n *ns.Namespace) {
	t.Helper()
	p, err := pace.FromEnv(os.LookupEnv)
	if err != nil {
		t.Fatalf("the run's pacer: %v", err)
	}
	for host, calls := range map[string]int{hostOf(t, f.State.GatewayURL): mainCredentialCalls, hostOf(t, n.URL): nsCredentialCalls} {
		for range calls {
			if err := p.Wait(t.Context(), host, pace.BucketCred); err != nil {
				t.Fatalf("pacing %s: %v", host, err)
			}
		}
	}
}

func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		t.Fatalf("%q is not a URL with a host: %v", raw, err)
	}
	return u.Hostname()
}
