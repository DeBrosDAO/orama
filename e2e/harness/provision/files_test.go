package provision

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/agent"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

func TestSaveState_firstSaveNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), stateFileName)
	if err := os.WriteFile(path, []byte("other run"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveState(&fleet.State{RunID: "testrun1"}, path, true); err == nil {
		t.Fatal("the first save overwrote an existing state")
	}
	if raw, _ := os.ReadFile(path); string(raw) != "other run" {
		t.Fatalf("the existing state changed: %q", raw)
	}
	assertNoStateTemps(t, filepath.Dir(path))
}

func TestSaveState_replacesASymlinkWithoutFollowingIt(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, stateFileName)
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}
	if err := saveState(&fleet.State{RunID: "testrun1"}, path, false); err != nil {
		t.Fatalf("saveState: %v", err)
	}
	if raw, _ := os.ReadFile(victim); string(raw) != "keep" {
		t.Fatalf("saveState wrote through a symlink: %q", raw)
	}
	st, err := fleet.Load(path)
	if err != nil || st.RunID != "testrun1" {
		t.Fatalf("saved state: %+v %v", st, err)
	}
	if fi, _ := os.Lstat(path); fi.Mode().Perm() != secretMode || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("state file mode %v", fi.Mode())
	}
	assertNoStateTemps(t, dir)
}

func assertNoStateTemps(t *testing.T, dir string) {
	t.Helper()
	if m, _ := filepath.Glob(filepath.Join(dir, stateTempPattern)); len(m) != 0 {
		t.Fatalf("temp state files left: %v", m)
	}
}

func TestWriteNewFile_refusesExistingAndSymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "key")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{victim, link} {
		if err := writeNewFile(p, []byte("secret")); err == nil {
			t.Errorf("writeNewFile(%s) wrote over what was there", p)
		}
	}
	if raw, _ := os.ReadFile(victim); string(raw) != "keep" {
		t.Fatalf("the target changed: %q", raw)
	}
	fresh := filepath.Join(dir, "fresh")
	if err := writeNewFile(fresh, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(fresh); fi.Mode().Perm() != secretMode {
		t.Fatalf("mode %v", fi.Mode())
	}
}

func TestMakeSSHKey_refusesAPlantedSymlink(t *testing.T) {
	e := newTestEnv(t)
	r := newRun(e.cfg, &testLogger{}, e.d)
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.st.SSHKeyFile, r.st.KnownHostsFile = filepath.Join(dir, sshKeyName), filepath.Join(dir, knownHostsName)
	if err := os.Symlink(victim, r.st.SSHKeyFile); err != nil {
		t.Fatal(err)
	}
	if err := r.makeSSHKey(context.Background()); err == nil {
		t.Fatal("makeSSHKey wrote the key through a planted symlink")
	}
	if raw, _ := os.ReadFile(victim); string(raw) != "keep" {
		t.Fatalf("the symlink target changed: %q", raw)
	}
}

func TestUp_failureClosesTheAgentLog(t *testing.T) {
	e := newTestEnv(t)
	var log *os.File
	start := e.d.startAgent
	e.d.startAgent = func(ctx context.Context, cfg agent.StartConfig) (*agentRun, error) {
		log = cfg.Log
		return start(ctx, cfg)
	}
	e.cmd.fail["--join-via"] = 1
	if _, err := up(context.Background(), e.cfg, &testLogger{}, e.d); err == nil {
		t.Fatal("up with a failing join succeeded")
	}
	if log == nil {
		t.Fatal("the agent was started without a log file")
	}
	if _, err := log.WriteString("x"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("the agent log is still open after the failed Up: %v", err)
	}
}
