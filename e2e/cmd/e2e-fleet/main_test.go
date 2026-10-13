package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/report"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func fullEnv() map[string]string {
	return map[string]string{"HCLOUD_TOKEN": "h-token-value", "CF_API_TOKEN": "cf-token-value", "CF_ZONE": "dbrsteting.bid"}
}

func TestPreflight_passes(t *testing.T) {
	if err := preflight(preflightInput{lookup: env(fullEnv()), realHome: "/home/o", runID: "ab12cd34"}); err != nil {
		t.Fatal(err)
	}
}

func TestPreflight_refusals(t *testing.T) {
	cases := map[string]struct {
		mutate func(m map[string]string) string
		want   string
	}{
		"missing secrets": {func(m map[string]string) string { delete(m, "HCLOUD_TOKEN"); delete(m, "CF_ZONE"); return "ab12" }, "CF_ZONE, HCLOUD_TOKEN"},
		"testnet zone":    {func(m map[string]string) string { m["CF_ZONE"] = "orama-testnet.network"; return "ab12" }, "testnet"},
		"mainnet run id":  {func(map[string]string) string { return "mainnet1" }, "mainnet"},
		"real wallet":     {func(m map[string]string) string { m["RW_AGENT_SOCK"] = "/home/o/.rootwallet/agent.sock"; return "ab12" }, "real"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := fullEnv()
			runID := c.mutate(m)
			err := preflight(preflightInput{lookup: env(m), realHome: "/home/o", runID: runID})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			for _, v := range fullEnv() {
				if strings.Contains(err.Error(), v) && v != "dbrsteting.bid" {
					t.Fatalf("preflight printed a secret value: %v", err)
				}
			}
		})
	}
}

func TestCheckProvisioned_guards(t *testing.T) {
	ok := &fleet.State{Env: "e2e-ab12", BaseDomain: "e2e-ab12.dbrsteting.bid", ChainID: "orama-devnet-e2e-ab12", Home: "/tmp/e2e-rw-test0001", RWSock: "/tmp/e2e-rw-test0001/a.sock"}
	if err := checkProvisioned(ok, "/home/o"); err != nil {
		t.Fatal(err)
	}
	bad := *ok
	bad.Env, bad.RWSock = "testnet", ""
	err := checkProvisioned(&bad, "/home/o")
	if err == nil || !strings.Contains(err.Error(), "testnet") || !strings.Contains(err.Error(), "RW_AGENT_SOCK") {
		t.Fatalf("err %v", err)
	}
}

func TestModuleRoot_findsE2EModule(t *testing.T) {
	wd, _ := os.Getwd()
	root, err := moduleRoot(wd)
	if err != nil || filepath.Base(root) != "e2e" {
		t.Fatalf("root %s err %v", root, err)
	}
	if _, err := moduleRoot(t.TempDir()); err == nil {
		t.Fatal("found a module above a temp dir")
	}
}

func TestDispatch_usageErrors(t *testing.T) {
	if code := dispatch(context.Background(), nil); code != exitUsage {
		t.Fatalf("no args: %d", code)
	}
	if code := dispatch(context.Background(), []string{"nope"}); code != exitUsage {
		t.Fatalf("unknown: %d", code)
	}
	if code := dispatch(context.Background(), []string{"coverage", "--bogus"}); code != exitUsage {
		t.Fatalf("bad flag: %d", code)
	}
}

func TestCmdCoverage_enforceToggle(t *testing.T) {
	t.Setenv(config.EnvCoverageEnforce, "0")
	code, err := cmdCoverage(context.Background(), nil)
	if err != nil || code != exitOK {
		t.Fatalf("non-enforced: code %d err %v", code, err)
	}
	t.Setenv(config.EnvCoverageEnforce, "banana")
	if code, _ := cmdCoverage(context.Background(), nil); code != exitUsage {
		t.Fatalf("bad toggle: %d", code)
	}
}

func TestCoverageExit_cases(t *testing.T) {
	if coverageExit(true, true) != exitOK || coverageExit(false, true) != exitFail || coverageExit(false, false) != exitOK {
		t.Fatal("coverage exit codes wrong")
	}
}

func TestExitCode_roundTrip(t *testing.T) {
	for _, v := range []string{report.VerdictPass, report.VerdictFail, report.VerdictIncomplete} {
		if verdictOf(exitCode(v)) != v {
			t.Errorf("%s does not round-trip", v)
		}
	}
}

func TestHookMember_validation(t *testing.T) {
	st := &fleet.State{RunID: "ab12"}
	path := filepath.Join(t.TempDir(), "state.json")
	called := ""
	act := func(_ context.Context, _ *fleet.State, host string) error { called = host; return nil }
	if code, err := hookMember(context.Background(), st, path, nil, act, true); code != exitUsage || !errors.Is(err, errUsage) {
		t.Fatalf("no host: %d %v", code, err)
	}
	if code, _ := hookMember(context.Background(), st, path, []string{"not-an-ip"}, act, true); code != exitUsage || called != "" {
		t.Fatalf("bad host accepted: %d", code)
	}
	if code, err := hookMember(context.Background(), st, path, []string{"203.0.113.5"}, act, true); code != exitOK || err != nil || called != "203.0.113.5" {
		t.Fatalf("valid host: %d %v", code, err)
	}
	if _, err := fleet.Load(path); err != nil {
		t.Fatalf("state not saved after a state-changing hook: %v", err)
	}
	failing := func(context.Context, *fleet.State, string) error { return errors.New("hetzner said no") }
	if code, err := hookMember(context.Background(), st, path, []string{"203.0.113.5"}, failing, false); code != exitFail || err == nil {
		t.Fatalf("failing act: %d %v", code, err)
	}
}

func TestDefaultLocation_fallback(t *testing.T) {
	if defaultLocation(&fleet.State{}) == "" {
		t.Fatal("empty default location")
	}
	if got := defaultLocation(&fleet.State{Nodes: []fleet.Node{{Location: "hel1"}}}); got != "hel1" {
		t.Fatalf("got %s", got)
	}
}

func TestCheckSweepAge_floorUnlessForced(t *testing.T) {
	if err := checkSweepAge(30*time.Minute, false); err == nil || !errors.Is(err, errUsage) {
		t.Fatalf("a 30m sweep was accepted: %v", err)
	}
	if err := checkSweepAge(30*time.Minute, true); err != nil {
		t.Fatal(err)
	}
	if err := checkSweepAge(minSweepAge, false); err != nil {
		t.Fatal(err)
	}
}
