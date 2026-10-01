package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/broker"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

func TestFeatureBaseEnv_resolvesGoCachesWithoutHome(t *testing.T) {
	t.Setenv("GOCACHE", "/explicit/cache")
	t.Setenv("HCLOUD_TOKEN", "hc-secret-value")
	env, err := featureBaseEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(stages.FeatureEnv(env), "\n")
	if !strings.Contains(got, "GOCACHE=/explicit/cache") || strings.Count(got, "GOCACHE=") != 1 {
		t.Fatalf("GOCACHE not kept exactly once: %q", got)
	}
	for _, name := range []string{"GOPATH=", "GOMODCACHE="} {
		if !strings.Contains(got, name) {
			t.Errorf("%s not resolved for the feature environment", name)
		}
	}
	if strings.Contains(got, "hc-secret-value") || strings.Contains(got, "\nHOME=") {
		t.Fatalf("a secret or HOME reached the feature environment: %q", got)
	}
}

func TestStartBroker_refusesWithoutCredentials(t *testing.T) {
	t.Setenv("CF_API_TOKEN", "")
	st := &fleet.State{RunID: "abcd1234"}
	if _, err := startBroker(context.Background(), st, filepath.Join(t.TempDir(), "state.json")); err == nil ||
		!strings.Contains(err.Error(), "CF_API_TOKEN") {
		t.Fatalf("err %v", err)
	}
}

// TestStartBroker_servesAfterTheRunnerIsCancelled: an interrupt cancels
// the runner's context, but features clean up through the broker during
// their stop grace, so it keeps serving until closed.
func TestStartBroker_servesAfterTheRunnerIsCancelled(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "hc-test-token-1")
	t.Setenv("CF_API_TOKEN", "cf-test-token-1")
	t.Setenv("CF_ZONE", "dbrsteting.bid")
	statePath := brokerTestState(t)
	st := &fleet.State{RunID: "ab12"}
	ctx, cancel := context.WithCancel(context.Background())
	l, err := startBroker(ctx, st, statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBroker(l)
	cancel()
	c, err := broker.New(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ccancel()
	if err := c.SetTXT(cctx, "x.example.com", "v"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("the broker stopped serving with the runner's context: %v", err)
	}
}

func TestBrokerMaxServers_parse(t *testing.T) {
	env := map[string]string{}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	if n, err := brokerMaxServers(lookup); n != 0 || err != nil {
		t.Fatalf("unset: %d %v", n, err)
	}
	env[broker.EnvMaxServers] = "7"
	if n, err := brokerMaxServers(lookup); n != 7 || err != nil {
		t.Fatalf("7: %d %v", n, err)
	}
	for _, bad := range []string{"0", "-2", "x"} {
		env[broker.EnvMaxServers] = bad
		if _, err := brokerMaxServers(lookup); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestFeatureHome_twoStatesInOneDirectoryKeepTheirOwn: two runs whose state
// files share a directory ran at once, and the second emptied and failed to
// recreate the first's HOME.
func TestFeatureHome_twoStatesInOneDirectoryKeepTheirOwn(t *testing.T) {
	work := t.TempDir()
	a, err := featureHome(filepath.Join(work, "a.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, "in-use"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := featureHome(filepath.Join(work, "b.json"))
	if err != nil {
		t.Fatalf("the second run could not make its HOME: %v", err)
	}
	if a == b {
		t.Fatalf("both runs got %s", a)
	}
	if _, err := os.Stat(filepath.Join(a, "in-use")); err != nil {
		t.Fatalf("the second run emptied the first run's HOME: %v", err)
	}
}

// TestFeatureHome_emptyPrivateNotTheRealHome: feature processes get an
// empty 0700 HOME inside the work dir, recreated empty each time; it is
// neither the owner's home nor anything under it.
func TestFeatureHome_emptyPrivateNotTheRealHome(t *testing.T) {
	work := t.TempDir()
	state := filepath.Join(work, "state.json")
	home, err := featureHome(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "stale"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if home, err = featureHome(state); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(home)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v err %v", info.Mode(), err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("the feature HOME kept %d entries", len(entries))
	}
	real, err := secrets.RealHome()
	if err != nil {
		t.Fatal(err)
	}
	if inside, _ := secrets.InsideByIdentity(home, real); inside && !strings.HasPrefix(work, real) {
		t.Fatalf("the feature HOME %s is inside the real home %s", home, real)
	}
	env := strings.Join(stages.FeatureEnv(append(os.Environ(), "HOME="+real)), "\n")
	if strings.Contains(env, "HOME="+real) {
		t.Fatal("the real HOME passed the feature filter")
	}
}
