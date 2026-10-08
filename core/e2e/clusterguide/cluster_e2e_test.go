//go:build e2e_cluster

package clusterguide

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Environment the run reads. Nothing is read from a file in the repo, and the
// test is skipped unless E2E_CLUSTER_BASE_DOMAIN is set, so `go test -tags
// e2e_cluster ./...` on a laptop does nothing.
const (
	envBaseDomain = "E2E_CLUSTER_BASE_DOMAIN"
	envIPs        = "E2E_CLUSTER_IPS"
	envArchive    = "E2E_CLUSTER_ARCHIVE"
	envRelease    = "E2E_CLUSTER_RELEASE"
	envRepo       = "E2E_CLUSTER_RELEASE_REPO"
	envRoot       = "E2E_CLUSTER_RELEASE_ROOT"
	envEnvName    = "E2E_CLUSTER_ENV"
	envToken      = "E2E_CLUSTER_CLOUDFLARE_TOKEN_FILE"
	envOrama      = "E2E_CLUSTER_ORAMA"
	envMode       = "E2E_CLUSTER_MODE"
	envWait       = "E2E_CLUSTER_DELEGATION_WAIT"
	envHostKeys   = "E2E_CLUSTER_HOST_KEYS"

	modeFull    = "full"
	modeUseOnly = "use-only"

	defaultEnvName = "e2eguide"
	// runBudget bounds the whole run: three installs at ~5 minutes each, the
	// delegation wait, a namespace's cluster and a deploy.
	runBudget = 90 * time.Minute
)

func TestRunYourOwnClusterGuide_executedStepByStep(t *testing.T) {
	domain := os.Getenv(envBaseDomain)
	if domain == "" {
		t.Skipf("%s is not set: no cluster fixture (see docs/DEV_DEPLOY.md, Cluster guide e2e)", envBaseDomain)
	}
	fx := fixtureFromEnv(t, domain)
	if err := fx.Validate(); err != nil {
		t.Fatalf("the fixture cannot run the guide: %v", err)
	}
	cmds, err := ParseGuide(readGuide(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runBudget)
	defer cancel()

	runner := Runner{Exec: OramaExecutor{Bin: oramaBinary(t)}, Fx: fx, Logf: t.Logf}
	results, err := runner.Execute(ctx, CoveredCommands(cmds), Plan())
	for _, r := range results {
		if r.Output != "" {
			t.Logf("--- %s\n%s", r.Step, r.Output)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
}

func fixtureFromEnv(t *testing.T, domain string) *Fixture {
	t.Helper()
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<!doctype html><title>guide e2e</title><p>ok</p>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx := &Fixture{
		BaseDomain:  domain,
		EnvName:     envOr(envEnvName, defaultEnvName),
		Archive:     os.Getenv(envArchive),
		Release:     os.Getenv(envRelease),
		ReleaseRepo: os.Getenv(envRepo),
		ReleaseRoot: os.Getenv(envRoot),
		TokenFile:   os.Getenv(envToken),
		SiteDir:     site,
		UseOnly:     os.Getenv(envMode) == modeUseOnly,
		HostKey:     ScanHostKey,
		LookupNS:    LookupNS,
		CertServed:  CertServed,
	}
	if mode := os.Getenv(envMode); mode != "" && mode != modeFull && mode != modeUseOnly {
		t.Fatalf("%s=%q: want %s or %s", envMode, mode, modeFull, modeUseOnly)
	}
	for _, ip := range strings.Split(os.Getenv(envIPs), ",") {
		if ip = strings.TrimSpace(ip); ip != "" {
			fx.IPs = append(fx.IPs, ip)
		}
	}
	if keys := os.Getenv(envHostKeys); keys != "" {
		fx.HostKey = pinnedHostKeys(t, fx.IPs, strings.Split(keys, ","))
	}
	if w := os.Getenv(envWait); w != "" {
		d, err := time.ParseDuration(w)
		if err != nil {
			t.Fatalf("%s=%q: %v", envWait, w, err)
		}
		fx.DelegationWait = d
	}
	return fx
}

// pinnedHostKeys answers --host-key from the operator's list instead of
// scanning the machines.
func pinnedHostKeys(t *testing.T, ips, keys []string) func(string) (string, error) {
	t.Helper()
	if len(keys) != len(ips) {
		t.Fatalf("%s has %d fingerprints for %d machines", envHostKeys, len(keys), len(ips))
	}
	byIP := map[string]string{}
	for i, ip := range ips {
		byIP[ip] = strings.TrimSpace(keys[i])
	}
	return func(ip string) (string, error) {
		if k, ok := byIP[ip]; ok {
			return k, nil
		}
		return ScanHostKey(ip)
	}
}

func oramaBinary(t *testing.T) string {
	t.Helper()
	bin := envOr(envOrama, "orama")
	path, err := exec.LookPath(bin)
	if err != nil {
		t.Fatalf("no orama binary (%s=%q): build it with `make -C core build` and set %s to its path: %v", envOrama, bin, envOrama, err)
	}
	return path
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
