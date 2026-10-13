//go:build linux

package orchardproc

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// envParentMode makes TestMain run as a node that starts a verifier child, prints the child's pid and
// waits to be killed.
const envParentMode = "ORAMA_TEST_PARENT_MODE_FILE"

func init() {
	parentModeHook = func() (int, bool) {
		if modeFile := os.Getenv(envParentMode); modeFile != "" {
			return runParent(modeFile), true
		}
		return 0, false
	}
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func runParent(modeFile string) int {
	exe, err := os.Executable()
	if err != nil {
		return 2
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		return 2
	}
	v, err := New(Config{Path: exe, ChainID: "c", SHA256: sha256Hex(raw), StartTimeout: 20 * time.Second,
		Env: []string{fakeModeFileEnv + "=" + modeFile, fakeIgnoreEOFEnv + "=1"}})
	if err != nil {
		return 2
	}
	if err := v.Warm(); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		return 3
	}
	os.Stdout.WriteString(strconv.Itoa(v.proc.cmd.Process.Pid) + "\n")
	select {}
}

// The child dies with the node, even when the node is killed outright and never closes its pipe
// politely. The fake child ignores the EOF of its stdin here, so Pdeathsig is the only thing that can
// end it: the test proves the signal and not the pipe.
func TestChild_diesWhenTheNodeIsKilled(t *testing.T) {
	modeFile := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(modeFile, []byte(modeAccept), 0o600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(exe)
	parent.Env = []string{envParentMode + "=" + modeFile}
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Process.Kill(); _ = parent.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("the node printed no child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	if !alive(pid) {
		t.Fatalf("the child %d is not running", pid)
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the child %d outlived the node that was killed", pid)
}

// alive reports a process that is running: one that has exited but was not reaped (a zombie, which
// is what an orphan is in a container whose init does not reap) is not.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(raw)
	rest := strings.Fields(s[strings.LastIndex(s, ")")+1:])
	return len(rest) > 0 && rest[0] != "Z"
}
