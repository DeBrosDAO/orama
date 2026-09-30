package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStopDir_stopsAnAgentFromAnotherProcessView(t *testing.T) {
	fakeHome(t)
	a, err := Start(context.Background(), startCfg(t, modeOK, okRW))
	if err != nil {
		t.Fatal(err)
	}
	if err := StopDir(context.Background(), a.Dir); err != nil {
		t.Fatalf("StopDir: %v", err)
	}
	if !waitGone(a.PID) {
		t.Fatal("StopDir left the agent running")
	}
	if _, err := os.Stat(a.Dir); !os.IsNotExist(err) {
		t.Fatalf("StopDir left the directory: %v", err)
	}
	if err := StopDir(context.Background(), a.Dir); err != nil {
		t.Fatalf("a second StopDir: %v", err)
	}
}

func TestStopDir_refusesForeignDirectory(t *testing.T) {
	fakeHome(t)
	dir := t.TempDir()
	if err := StopDir(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "not a test agent directory") {
		t.Fatalf("StopDir on a foreign dir: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the foreign dir was touched: %v", err)
	}
}

func TestStopDir_neverSignalsAnUnrelatedPID(t *testing.T) {
	fakeHome(t)
	dir := filepath.Join(shortBase(t), DirPrefix+"x")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Our own pid: alive, but its command line does not name dir.
	writeFakeReadyPID(t, dir, os.Getpid())
	if err := StopDir(context.Background(), dir); err != nil {
		t.Fatalf("StopDir: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("StopDir did not remove the directory")
	}
}

func TestShredTree_zeroesAndRemoves(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "k"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := shredTree(dir); err != nil {
		t.Fatalf("shredTree: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("shredTree left the directory")
	}
	if _, err := os.Stat("/etc/hosts"); err != nil {
		t.Fatal("shredTree followed a symlink out of the tree")
	}
}

func TestNewMnemonic_twelveDistinctPhrases(t *testing.T) {
	a, err := newMnemonic()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newMnemonic()
	if len(strings.Fields(a)) != 12 || a == b {
		t.Fatalf("mnemonics %q / %q", a, b)
	}
}

func TestWriteSecret_refusesExistingPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s")
	if err := writeSecret(p, "one"); err != nil {
		t.Fatal(err)
	}
	if err := writeSecret(p, "two"); err == nil {
		t.Fatal("writeSecret overwrote an existing file")
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != secretFileMode {
		t.Fatalf("mode %o", fi.Mode().Perm())
	}
}

func writeFakeReadyPID(t *testing.T, dir string, pid int) {
	t.Helper()
	raw := []byte(`{"pid":` + strconv.Itoa(pid) + `,"socket":"x","address":"` + fakeAddress + `"}`)
	if err := os.WriteFile(filepath.Join(dir, readyName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPassesHome(t *testing.T) {
	dir := "/tmp/e2e-rw-abc"
	cases := map[string]bool{
		"rw-agent-headless --home /tmp/e2e-rw-abc --socket /tmp/e2e-rw-abc/a.sock": true,
		"rw-agent-headless --home /tmp/e2e-rw-abcdef --socket x":                   false,
		"tail -f /tmp/e2e-rw-abc/agent.log":                                        false,
		"rw-agent-headless --socket /tmp/e2e-rw-abc/a.sock":                        false,
		"sh -c sleep --home": false,
	}
	for cmdline, want := range cases {
		if got := passesHome(cmdline, dir); got != want {
			t.Errorf("passesHome(%q) = %v, want %v", cmdline, got, want)
		}
	}
}

func TestStopDir_neverSignalsAProcessThatOnlyMentionsTheDir(t *testing.T) {
	fakeHome(t)
	dir := filepath.Join(shortBase(t), DirPrefix+"x")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Its command line holds dir as a substring (--home <dir>-other), but it
	// is not the agent of dir. "; :" stops sh from exec'ing sleep.
	bystander := exec.Command("sh", "-c", "sleep 30; :", "--home", dir+"-other")
	if err := bystander.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bystander.Process.Kill(); _ = bystander.Wait() })
	writeFakeReadyPID(t, dir, bystander.Process.Pid)
	if err := StopDir(context.Background(), dir); err != nil {
		t.Fatalf("StopDir: %v", err)
	}
	if !alive(bystander.Process.Pid) {
		t.Fatal("StopDir signalled a process whose command line only mentions the dir")
	}
}
