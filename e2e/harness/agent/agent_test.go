package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startCfg(t *testing.T, mode, rwScript string) StartConfig {
	t.Helper()
	rw, agentBin := fakeBins(t, mode, rwScript)
	orama := filepath.Join(t.TempDir(), "orama")
	writeScript(t, orama, "#!/bin/sh\n")
	return StartConfig{
		RWBin: rw, AgentBin: agentBin, BaseDir: shortBase(t), ReadyTimeout: 5 * time.Second,
		Approvals: []Approval{{Binary: orama, Caps: OramaCaps}},
	}
}

func TestStart_happyPathAndStop(t *testing.T) {
	fakeHome(t)
	a, err := Start(context.Background(), startCfg(t, modeOK, okRW))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if a.Address != fakeAddress || !strings.HasPrefix(filepath.Base(a.Dir), DirPrefix) {
		t.Fatalf("agent %+v", a)
	}
	if env := strings.Join(a.Env(), " "); !containsAll(env, "HOME="+a.Dir, "RW_AGENT_SOCK="+a.Sock) {
		t.Fatalf("Env = %s", env)
	}
	if _, err := os.Stat(filepath.Join(a.Dir, mnemonicName)); !os.IsNotExist(err) {
		t.Fatalf("the mnemonic survived wallet creation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.Dir, rootwalletDirName, "wallet.enc")); err != nil {
		t.Fatalf("rw init did not run in the agent home: %v", err)
	}
	pid := a.PID
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !waitGone(pid) {
		t.Fatal("the agent is still running after Stop")
	}
	if _, err := os.Stat(a.Dir); !os.IsNotExist(err) {
		t.Fatalf("the agent directory survived Stop: %v", err)
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("a second Stop: %v", err)
	}
}

func TestStart_refusedCapabilityNamesTheRootWalletTask(t *testing.T) {
	fakeHome(t)
	cfg := startCfg(t, modeRefuseCap, okRW)
	_, err := Start(context.Background(), cfg)
	if err == nil || !containsAll(err.Error(), "cannot be pre-approved", RootWalletHeadlessTask) {
		t.Fatalf("Start with a refused capability: %v", err)
	}
	assertNoAgentDirs(t, cfg.BaseDir)
}

func TestStart_readyTimeoutStopsAndCleans(t *testing.T) {
	fakeHome(t)
	cfg := startCfg(t, modeNeverReady, okRW)
	cfg.ReadyTimeout = 300 * time.Millisecond
	if _, err := Start(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("Start with no ready file: %v", err)
	}
	assertNoAgentDirs(t, cfg.BaseDir)
}

func TestStart_lockedAgentIsRefused(t *testing.T) {
	fakeHome(t)
	if _, err := Start(context.Background(), startCfg(t, modeLocked, okRW)); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("Start with a locked agent: %v", err)
	}
}

func TestStart_readyFileForAnotherSocket(t *testing.T) {
	fakeHome(t)
	if _, err := Start(context.Background(), startCfg(t, modeWrongSock, okRW)); err == nil || !strings.Contains(err.Error(), "not the socket") {
		t.Fatalf("Start with a foreign ready file: %v", err)
	}
}

func TestStart_rwInitFailure(t *testing.T) {
	fakeHome(t)
	cfg := startCfg(t, modeOK, failingRW)
	if _, err := Start(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "wallet already exists") {
		t.Fatalf("Start with rw init failing: %v", err)
	}
	assertNoAgentDirs(t, cfg.BaseDir)
}

func TestStart_refusesRoot(t *testing.T) {
	fakeHome(t)
	prev := geteuid
	geteuid = func() int { return 0 }
	defer func() { geteuid = prev }()
	cfg := startCfg(t, modeOK, okRW)
	if _, err := Start(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "as root") {
		t.Fatalf("Start as root: %v", err)
	}
	assertNoAgentDirs(t, cfg.BaseDir)
}

func TestStart_refusesDirInsideRealWallet(t *testing.T) {
	home := fakeHome(t)
	real := filepath.Join(home, rootwalletDirName)
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := startCfg(t, modeOK, okRW)
	cfg.BaseDir = real
	if _, err := Start(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "real RootWallet") {
		t.Fatalf("Start inside the real wallet dir: %v", err)
	}
}

func TestStart_refusesLongSocketPath(t *testing.T) {
	fakeHome(t)
	cfg := startCfg(t, modeOK, okRW)
	long := filepath.Join(cfg.BaseDir, strings.Repeat("d", maxSocketPath))
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg.BaseDir = long
	if _, err := Start(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Fatalf("Start with a long socket path: %v", err)
	}
}

func TestStart_badConfig(t *testing.T) {
	fakeHome(t)
	cfg := startCfg(t, modeOK, okRW)
	noApprovals := cfg
	noApprovals.Approvals = nil
	relative := cfg
	relative.Approvals = []Approval{{Binary: "orama", Caps: OramaCaps}}
	missing := cfg
	missing.AgentBin = filepath.Join(t.TempDir(), "absent")
	for name, c := range map[string]StartConfig{"no approvals": noApprovals, "relative": relative, "missing": missing} {
		if _, err := Start(context.Background(), c); err == nil {
			t.Errorf("%s: Start accepted it", name)
		}
	}
}

func TestStop_killsAnAgentIgnoringTerm(t *testing.T) {
	fakeHome(t)
	a, err := Start(context.Background(), startCfg(t, modeIgnoreTerm, okRW))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(); err == nil || !strings.Contains(err.Error(), "ignored SIGTERM") {
		t.Fatalf("Stop of a stubborn agent: %v", err)
	}
	if !waitGone(a.PID) {
		t.Fatal("the stubborn agent survived")
	}
}

func assertNoAgentDirs(t *testing.T, base string) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(base, DirPrefix+"*"))
	if len(matches) != 0 {
		t.Fatalf("agent directories left behind: %v", matches)
	}
}
