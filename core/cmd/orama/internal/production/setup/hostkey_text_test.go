package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubCommands makes every helper binary "found" and runs each through run.
func stubCommands(t *testing.T, run func(bin string, args ...string) (string, error)) {
	t.Helper()
	oldFind, oldRun, oldStdin := findBinary, runCommand, runCommandWithEnvStdin
	findBinary = func(name string) (string, error) { return "/stub/" + name, nil }
	runCommand = run
	t.Cleanup(func() { findBinary, runCommand, runCommandWithEnvStdin = oldFind, oldRun, oldStdin })
}

// What a machine prints when its command fails goes into an error shown on the operator's
// terminal: it must stay on one line, its lines apart and its escape sequences gone.
func TestScanHostKey_aMultiLineFailureIsOnOneLine(t *testing.T) {
	stubCommands(t, func(string, ...string) (string, error) {
		return "# 203.0.113.9:22 SSH-2.0\nconnect: Connection refused\n\x1b[2Jforged line\n", errors.New("exit status 1")
	})
	_, err := scanHostKey("203.0.113.9")
	if err == nil {
		t.Fatal("a failed scan was accepted")
	}
	if strings.ContainsAny(err.Error(), "\r\n\x1b") {
		t.Errorf("the error is not on one line: %q", err)
	}
	if want := "# 203.0.113.9:22 SSH-2.0 | connect: Connection refused | [2Jforged line"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err, want)
	}
}

func TestScanHostKey_aFingerprintFailureIsOnOneLine(t *testing.T) {
	calls := 0
	stubCommands(t, func(bin string, args ...string) (string, error) {
		calls++
		if strings.HasSuffix(bin, "ssh-keyscan") {
			return "203.0.113.9 ssh-ed25519 AAAA\n", nil
		}
		return "line one\nline two\n", errors.New("exit status 1")
	})
	_, err := scanHostKey("203.0.113.9")
	if err == nil || strings.Contains(err.Error(), "\n") || !strings.Contains(err.Error(), "line one | line two") {
		t.Fatalf("err = %v", err)
	}
	if calls != 2 {
		t.Errorf("ran %d commands, want the scan and the fingerprint", calls)
	}
}

func TestInstallPublicKeyWithKey_aMultiLineAnswerIsOnOneLine(t *testing.T) {
	key := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	stubCommands(t, nil)
	runCommandWithEnvStdin = func(string, []string, string, ...string) (string, error) { return "denied\nbecause\n", nil }
	err := installPublicKeyWithKey("203.0.113.9", "root", key, "ssh-ed25519 AAAA", "/dev/null")
	if err == nil || strings.Contains(err.Error(), "\n") || !strings.Contains(err.Error(), "denied | because") {
		t.Fatalf("err = %v", err)
	}
}
